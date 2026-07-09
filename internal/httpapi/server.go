// Copyright (C) 2026 Joey Kot <joey.kot.x@gmail.com>
//
// This program is free software: you can redistribute it and/or modify
// it under the terms of the GNU General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.
//
// This program is distributed WITHOUT ANY WARRANTY; without even the
// implied warranty of MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE.
// See <https://www.gnu.org/licenses/> for more details.

package httpapi

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"

	smartaudio "github.com/Joey-Kot/ASR-Audio-Preprocess"

	"qwen-stt-compatible/internal/config"
	"qwen-stt-compatible/internal/dashscope"
	"qwen-stt-compatible/internal/models"
	"qwen-stt-compatible/internal/sse"
)

const (
	multipartMemoryLimit = 32 << 20
	tempRootName         = "qwen-stt-compatible"
)

type ASRClient interface {
	TranscribeFile(ctx context.Context, path, model string, options dashscope.ASROptions, prompt string) (string, error)
}

type Server struct {
	cfg    config.Config
	client ASRClient
	apiSem chan struct{}
}

type transcriptionResult struct {
	Status string `json:"status"`
	Text   string `json:"text"`
}

func New(cfg config.Config, client ASRClient) *Server {
	concurrency := cfg.APIConcurrency
	if concurrency <= 0 {
		concurrency = 1
	}
	return &Server{cfg: cfg, client: client, apiSem: make(chan struct{}, concurrency)}
}

func CleanupTempDirs() error {
	return cleanupTempDirs(filepath.Join(os.TempDir(), tempRootName))
}

func cleanupTempDirs(root string) error {
	entries, err := os.ReadDir(root)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	var errs []error
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		path := filepath.Join(root, entry.Name())
		if err := os.RemoveAll(path); err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", path, err))
		}
	}
	return errors.Join(errs...)
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.setCommonHeaders(w)
	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if r.URL.Path == "/health" {
		writeJSON(w, http.StatusOK, map[string]any{"status": "ok"})
		return
	}
	if !s.authorize(w, r) {
		return
	}
	path := strings.TrimRight(r.URL.Path, "/")
	switch path {
	case "/v1/audio/transcriptions":
		s.handleTranscriptions(w, r)
	case "/v1/models":
		s.handleModels(w, r)
	default:
		openAIError(w, http.StatusNotFound, "not found", "invalid_request_error")
	}
}

func (s *Server) handleTranscriptions(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		methodNotAllowed(w)
		return
	}
	result, stream, err := s.processRequest(w, r)
	if err != nil {
		s.handleProcessError(w, err)
		return
	}
	if stream {
		s.writePseudoStream(w, r, result)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (s *Server) processRequest(w http.ResponseWriter, r *http.Request) (transcriptionResult, bool, error) {
	r.Body = http.MaxBytesReader(w, r.Body, s.cfg.MaxUploadBytes+multipartMemoryLimit)
	if err := r.ParseMultipartForm(multipartMemoryLimit); err != nil {
		return transcriptionResult{}, false, clientError("multipart/form-data 解析失败")
	}
	if r.MultipartForm != nil {
		defer r.MultipartForm.RemoveAll()
	}
	file, header, err := r.FormFile("file")
	if err != nil {
		return transcriptionResult{}, false, clientError("未选择文件")
	}
	defer file.Close()
	if header.Size > s.cfg.MaxUploadBytes {
		return transcriptionResult{}, false, clientError("文件超过上传大小限制")
	}

	modelName := strings.TrimSpace(r.FormValue("model"))
	if modelName == "" {
		return transcriptionResult{}, false, clientError("model is required")
	}
	sampleRate, err := models.SampleRate(modelName)
	if err != nil {
		return transcriptionResult{}, false, clientError(err.Error())
	}
	language := normalizeLanguageCode(r.FormValue("language"))
	prompt := r.FormValue("prompt")
	enableLID := boolForm(r.FormValue("enable_lid"), s.cfg.EnableLID)
	enableITN := boolForm(r.FormValue("enable_itn"), s.cfg.EnableITN)
	stream := boolForm(r.FormValue("stream"), false)

	requestID := dashscope.RequestID()
	tempDir := filepath.Join(os.TempDir(), tempRootName, requestID)
	if err := os.MkdirAll(tempDir, 0o755); err != nil {
		return transcriptionResult{}, stream, err
	}
	defer os.RemoveAll(tempDir)

	inputPath := filepath.Join(tempDir, "input_"+safeFilename(header.Filename))
	out, err := os.Create(inputPath)
	if err != nil {
		return transcriptionResult{}, stream, err
	}
	if _, err := io.Copy(out, file); err != nil {
		out.Close()
		return transcriptionResult{}, stream, err
	}
	if err := out.Close(); err != nil {
		return transcriptionResult{}, stream, err
	}

	log.Printf("request=%s endpoint=/v1/audio/transcriptions file=%s model=%s language=%s enable_lid=%v enable_itn=%v", requestID, sanitizeLogValue(header.Filename), modelName, language, enableLID, enableITN)
	segments, err := s.preprocess(r.Context(), inputPath, tempDir, sampleRate)
	if err != nil {
		return transcriptionResult{}, stream, err
	}
	if len(segments) == 0 {
		return transcriptionResult{}, stream, errors.New("未能生成分段")
	}
	options := dashscope.ASROptions{EnableLID: enableLID, EnableITN: enableITN, Language: language, SampleRate: sampleRate}
	text, err := s.recognizeSegments(r.Context(), segments, modelName, options, prompt)
	if err != nil {
		return transcriptionResult{}, stream, err
	}
	return transcriptionResult{Status: "success", Text: text}, stream, nil
}

func (s *Server) preprocess(ctx context.Context, inputPath, tempDir string, sampleRate int) ([]smartaudio.Segment, error) {
	cfg := smartaudio.DefaultConfig()
	cfg.Silence.MinSilence = s.cfg.SilentInterval
	cfg.Silence.Padding = s.cfg.Padding
	cfg.FixedTrim.SliceLength = s.cfg.FixedSliceLength
	cfg.FixedTrim.Workers = s.cfg.FixedSliceWorkers
	cfg.FixedTrim.TempDir = filepath.Join(tempDir, "fixed_slice_temp")
	cfg.Segments.Workers = s.cfg.SegmentWorkers
	cfg.Segments.MaxLength = s.cfg.APISegmentLength
	cfg.Segments.OutputFormat = "ogg"
	cfg.Segments.OutputCodec = "libopus"
	cfg.Segments.OutputBitrate = s.cfg.OutputBitrate
	cfg.Segments.OutputSampleRate = sampleRate
	cfg.Segments.OutputSampleFormat = "s16"
	cfg.Segments.OutDir = filepath.Join(tempDir, "out_segments")
	cfg.Segments.KeepTempWAV = smartaudio.Bool(true)
	cfg.Segments.PreserveInternalSilence = smartaudio.Bool(true)
	cfg.Libav.CodecThreads = s.cfg.LibavCodecThreads
	processor, err := smartaudio.NewProcessor(smartaudio.WithConfig(cfg))
	if err != nil {
		return nil, err
	}
	wavPath := filepath.Join(tempDir, "converted.wav")
	convertInfo, err := processor.PreconvertToWAV(ctx, inputPath, wavPath, sampleRate)
	if err != nil {
		return nil, err
	}
	mergedWAV := filepath.Join(tempDir, "converted_sliced_merged.wav")
	merged, trimInfo, err := processor.RemoveSilenceByFixedSlicesAndMerge(ctx, wavPath, mergedWAV)
	if err != nil {
		log.Printf("fixed trim failed, fallback to converted wav: %v", err)
		merged = wavPath
	} else {
		log.Printf("fixed trim input_duration=%s fixed_slice_length=%s slices=%d trimmed_slices=%d", convertInfo.InputDuration, s.cfg.FixedSliceLength, trimInfo.FixedSliceSucceeded, countTrimmedSliceFiles(cfg.FixedTrim.TempDir))
	}
	segments, splitInfo, err := processor.SplitWAVBySilenceGroups(ctx, merged)
	if err != nil {
		return nil, err
	}
	log.Printf("segments merged_duration=%s asr_segments=%d", splitInfo.InputDuration, splitInfo.SegmentCount)
	return segments, nil
}

func countTrimmedSliceFiles(tempRoot string) int {
	runDirs, err := filepath.Glob(filepath.Join(tempRoot, "run-*"))
	if err != nil {
		return 0
	}
	total := 0
	for _, runDir := range runDirs {
		entries, err := os.ReadDir(filepath.Join(runDir, "trimmed_slices"))
		if err != nil {
			continue
		}
		for _, entry := range entries {
			if !entry.IsDir() {
				total++
			}
		}
	}
	return total
}

func (s *Server) recognizeSegments(ctx context.Context, segments []smartaudio.Segment, model string, options dashscope.ASROptions, prompt string) (string, error) {
	type result struct {
		index int
		text  string
		err   error
	}
	results := make([]result, len(segments))
	var wg sync.WaitGroup
	for i, segment := range segments {
		i := i
		segment := segment
		wg.Add(1)
		go func() {
			defer wg.Done()
			release, err := s.acquireAPISlot(ctx)
			if err != nil {
				results[i] = result{index: segment.Index, err: err}
				return
			}
			defer release()
			text, err := s.client.TranscribeFile(ctx, segment.File, model, options, prompt)
			results[i] = result{index: segment.Index, text: text, err: err}
		}()
	}
	wg.Wait()
	sort.Slice(results, func(i, j int) bool { return results[i].index < results[j].index })
	var builder strings.Builder
	for _, res := range results {
		if res.err != nil {
			return "", fmt.Errorf("分片识别失败: index=%d, msg=%v", res.index, res.err)
		}
		builder.WriteString(res.text)
	}
	return builder.String(), nil
}

func (s *Server) acquireAPISlot(ctx context.Context) (func(), error) {
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case s.apiSem <- struct{}{}:
		return func() { <-s.apiSem }, nil
	}
}

func (s *Server) writePseudoStream(w http.ResponseWriter, r *http.Request, result transcriptionResult) {
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
	flusher, _ := w.(http.Flusher)
	for _, chunk := range splitText(result.Text, 240) {
		if err := sse.Data(w, map[string]any{"type": "transcript.text.delta", "delta": chunk}); err != nil {
			log.Printf("write sse delta: %v", err)
			return
		}
		if flusher != nil {
			flusher.Flush()
		}
		select {
		case <-r.Context().Done():
			return
		default:
		}
	}
	_ = sse.Data(w, map[string]any{"type": "transcript.text.done", "text": result.Text})
	_ = sse.Data(w, "[DONE]")
	if flusher != nil {
		flusher.Flush()
	}
}

func (s *Server) handleModels(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		methodNotAllowed(w)
		return
	}
	data := make([]any, 0, len(models.List()))
	for _, model := range models.List() {
		data = append(data, map[string]any{"id": model, "object": "model", "owned_by": "dashscope"})
	}
	writeJSON(w, http.StatusOK, map[string]any{"object": "list", "data": data})
}

func (s *Server) authorize(w http.ResponseWriter, r *http.Request) bool {
	if len(s.cfg.APITokens) == 0 {
		openAIError(w, http.StatusInternalServerError, "服务器内部错误：API_TOKEN 未配置", "server_error")
		return false
	}
	if token := r.Header.Get("x-api-key"); token != "" && s.tokenMatches(token) {
		return true
	}
	auth := r.Header.Get("Authorization")
	const prefix = "Bearer "
	if !strings.HasPrefix(auth, prefix) {
		w.Header().Set("WWW-Authenticate", "Bearer")
		openAIError(w, http.StatusUnauthorized, "Missing API key or Authorization Bearer token", "authentication_error")
		return false
	}
	if s.tokenMatches(strings.TrimPrefix(auth, prefix)) {
		return true
	}
	w.Header().Set("WWW-Authenticate", "Bearer")
	openAIError(w, http.StatusUnauthorized, "Invalid authentication token", "authentication_error")
	return false
}

func (s *Server) tokenMatches(token string) bool {
	for _, expected := range s.cfg.APITokens {
		if subtle.ConstantTimeCompare([]byte(token), []byte(expected)) == 1 {
			return true
		}
	}
	return false
}

func (s *Server) setCommonHeaders(w http.ResponseWriter) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Access-Control-Allow-Methods", "*")
	w.Header().Set("Access-Control-Allow-Headers", "*")
}

type clientError string

func (e clientError) Error() string { return string(e) }

func (s *Server) handleProcessError(w http.ResponseWriter, err error) {
	var bad clientError
	if errors.As(err, &bad) {
		openAIError(w, http.StatusBadRequest, err.Error(), "invalid_request_error")
		return
	}
	openAIError(w, http.StatusBadGateway, err.Error(), "server_error")
}

func safeFilename(name string) string {
	base := filepath.Base(name)
	if base == "." || base == string(filepath.Separator) || base == "" {
		base = "unknown_file"
	}
	re := regexp.MustCompile(`[^a-zA-Z0-9._-]`)
	clean := re.ReplaceAllString(base, "")
	if clean == "" {
		return "unknown_file"
	}
	return clean
}

func sanitizeLogValue(value string) string {
	var builder strings.Builder
	for _, r := range value {
		switch r {
		case '\n':
			builder.WriteString(`\n`)
		case '\r':
			builder.WriteString(`\r`)
		case '\t':
			builder.WriteString(`\t`)
		default:
			if r < 0x20 || r == 0x7f {
				fmt.Fprintf(&builder, `\x%02x`, r)
				continue
			}
			builder.WriteRune(r)
		}
	}
	return builder.String()
}

func normalizeLanguageCode(lang string) string {
	lang = strings.ToLower(strings.TrimSpace(lang))
	if matched, _ := regexp.MatchString(`^[a-z]{2}$`, lang); matched {
		return lang
	}
	return ""
}

func boolForm(value string, fallback bool) bool {
	value = strings.TrimSpace(strings.ToLower(value))
	if value == "" {
		return fallback
	}
	parsed, err := strconv.ParseBool(value)
	if err != nil {
		return fallback
	}
	return parsed
}

func splitText(text string, maxRunes int) []string {
	if text == "" {
		return []string{""}
	}
	if maxRunes <= 0 {
		maxRunes = 240
	}
	runes := []rune(text)
	chunks := make([]string, 0, (len(runes)/maxRunes)+1)
	for start := 0; start < len(runes); start += maxRunes {
		end := start + maxRunes
		if end > len(runes) {
			end = len(runes)
		}
		chunks = append(chunks, string(runes[start:end]))
	}
	return chunks
}

func writeJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(payload); err != nil {
		log.Printf("write json response: %v", err)
	}
}

func openAIError(w http.ResponseWriter, status int, message, typ string) {
	writeJSON(w, status, map[string]any{"error": map[string]any{"message": message, "type": typ, "param": nil, "code": nil}})
}

func methodNotAllowed(w http.ResponseWriter) {
	openAIError(w, http.StatusMethodNotAllowed, "method not allowed", "invalid_request_error")
}
