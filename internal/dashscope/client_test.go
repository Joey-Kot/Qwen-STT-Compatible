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

package dashscope

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"qwen-stt-compatible/internal/config"
)

func TestNewHTTPClientUsesFreshHTTP2Connections(t *testing.T) {
	var protocols, remoteAddrs []string
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		protocols = append(protocols, r.Proto)
		remoteAddrs = append(remoteAddrs, r.RemoteAddr)
		w.WriteHeader(http.StatusNoContent)
	}))
	server.EnableHTTP2 = true
	server.StartTLS()
	defer server.Close()

	client := newHTTPClient(30 * time.Second)
	transport := client.Transport.(*http.Transport)
	transport.TLSClientConfig = &tls.Config{InsecureSkipVerify: true} // #nosec G402 -- local test server only.
	for range 2 {
		resp, err := client.Get(server.URL)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
	}

	if len(protocols) != 2 || protocols[0] != "HTTP/2.0" || protocols[1] != "HTTP/2.0" {
		t.Fatalf("protocols=%v want two HTTP/2.0 requests", protocols)
	}
	if remoteAddrs[0] == remoteAddrs[1] {
		t.Fatalf("requests reused a connection: remote addresses=%v", remoteAddrs)
	}
}

func TestDoLogsUpstreamRequestWithoutSecrets(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()

	var logs bytes.Buffer
	previousOutput := log.Writer()
	log.SetOutput(&logs)
	t.Cleanup(func() { log.SetOutput(previousOutput) })

	target, err := url.Parse(server.URL + "/upload?signature=secret")
	if err != nil {
		t.Fatal(err)
	}
	target.User = url.UserPassword("user", "password")
	req, err := http.NewRequest(http.MethodGet, target.String(), nil)
	if err != nil {
		t.Fatal(err)
	}
	client := New(Config{HTTPClient: server.Client()})
	resp, err := client.do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()

	output := logs.String()
	if !strings.Contains(output, "upstream request method=GET url="+server.URL+"/upload") {
		t.Fatalf("request log missing or unexpected: %s", output)
	}
	if !strings.Contains(output, "upstream response method=GET") || !strings.Contains(output, "status=204 protocol=HTTP/1.1") {
		t.Fatalf("response log missing or unexpected: %s", output)
	}
	if strings.Contains(output, "secret") || strings.Contains(output, "password") {
		t.Fatalf("log contains sensitive URL data: %s", output)
	}
}

func TestModelFamilyPrefersFunASRFlashBeforeFunASR(t *testing.T) {
	tests := []struct {
		model string
		want  string
	}{
		{model: "qwen-audio-3.0-asr-flash-filetrans", want: "audio3-asr-filetrans"},
		{model: "qwen-audio-3.0-asr-flash", want: "audio3-asr-flash"},
		{model: "qwen-audio-3.1-asr-flash-filetrans", want: "audio3-asr-filetrans"},
		{model: "qwen-audio-3.1-asr-flash", want: "audio3-asr-flash"},
		{model: "qwen-audio-3.1-asr-flash-message", want: "audio3-asr-flash"},
		{model: "fun-asr-flash-2026-06-15", want: "audio3-asr-flash"},
		{model: "fun-asr", want: "fun-asr"},
		{model: "paraformer-v1", want: "paraformer"},
		{model: "qwen3-asr-flash-2025-09-08", want: "qwen3-asr-flash"},
	}
	for _, tt := range tests {
		if got := modelFamily(tt.model); got != tt.want {
			t.Fatalf("modelFamily(%q)=%q want %q", tt.model, got, tt.want)
		}
	}
}

func TestSupportsLanguageHints(t *testing.T) {
	tests := []struct {
		model string
		want  bool
	}{
		{model: "paraformer-v2", want: true},
		{model: "paraformer-v2-2026-01-01", want: true},
		{model: "qwen-audio-3.0-asr-flash-filetrans", want: true},
		{model: "qwen-audio-3.1-asr-flash-filetrans", want: true},
		{model: "paraformer-v1", want: false},
		{model: "paraformer-8k-v1", want: false},
		{model: "paraformer-mtl-v1", want: false},
		{model: "fun-asr", want: true},
	}
	for _, tt := range tests {
		if got := supportsLanguageHints(tt.model); got != tt.want {
			t.Errorf("supportsLanguageHints(%q)=%v want %v", tt.model, got, tt.want)
		}
	}
}

func TestSupportsAsyncContext(t *testing.T) {
	tests := []struct {
		model string
		want  bool
	}{
		{model: "qwen-audio-3.0-asr-flash-filetrans", want: true},
		{model: "qwen-audio-3.1-asr-flash-filetrans", want: true},
		{model: "fun-asr", want: true},
		{model: "fun-asr-mtl-2025-08-25", want: true},
		{model: "paraformer-v2", want: false},
	}
	for _, tt := range tests {
		if got := supportsAsyncContext(tt.model); got != tt.want {
			t.Errorf("supportsAsyncContext(%q)=%v want %v", tt.model, got, tt.want)
		}
	}
}

func TestAsyncTaskHeadersEnableOSSResolutionOnlyForOSSURLs(t *testing.T) {
	ossHeaders := asyncTaskHeaders("oss://bucket/audio.ogg")
	if ossHeaders["X-DashScope-Async"] != "enable" || ossHeaders["X-DashScope-OssResourceResolve"] != "enable" {
		t.Fatalf("OSS headers=%v", ossHeaders)
	}
	httpHeaders := asyncTaskHeaders("https://files.example.com/audio.ogg")
	if httpHeaders["X-DashScope-Async"] != "enable" {
		t.Fatalf("HTTP headers=%v", httpHeaders)
	}
	if _, exists := httpHeaders["X-DashScope-OssResourceResolve"]; exists {
		t.Fatalf("HTTP URL unexpectedly enables OSS resolution: %v", httpHeaders)
	}
}

func TestExtractTranscriptionTextPrefersTranscriptText(t *testing.T) {
	payload := map[string]any{
		"transcripts": []any{
			map[string]any{
				"text": "第一段",
				"sentences": []any{
					map[string]any{"text": "不应重复"},
				},
			},
			map[string]any{"text": "第二段"},
		},
	}
	if got := extractTranscriptionText(payload); got != "第一段第二段" {
		t.Fatalf("extractTranscriptionText()=%q", got)
	}
}

func TestExtractTranscriptionTextFallsBackToSentences(t *testing.T) {
	payload := map[string]any{
		"transcripts": []any{
			map[string]any{
				"sentences": []any{
					map[string]any{"text": "第一句"},
					map[string]any{"text": "第二句"},
				},
			},
		},
	}
	if got := extractTranscriptionText(payload); got != "第一句第二句" {
		t.Fatalf("extractTranscriptionText()=%q", got)
	}
}

func TestAsyncTaskResponseFallsBackToTopLevelTaskID(t *testing.T) {
	var response asyncTaskResponse
	if err := json.Unmarshal([]byte(`{"request_id":"req","task_id":"task-top"}`), &response); err != nil {
		t.Fatal(err)
	}
	if got := response.taskID(); got != "task-top" {
		t.Fatalf("taskID()=%q want %q", got, "task-top")
	}
}

func TestAsyncTaskResponsePrefersOutputTaskID(t *testing.T) {
	var response asyncTaskResponse
	if err := json.Unmarshal([]byte(`{"task_id":"task-top","output":{"task_id":"task-output"}}`), &response); err != nil {
		t.Fatal(err)
	}
	if got := response.taskID(); got != "task-output" {
		t.Fatalf("taskID()=%q want %q", got, "task-output")
	}
}

func TestTranscribeQwen3FlashUsesBase64DataURI(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/services/aigc/multimodal-generation/generation":
			if got := r.Header.Get("X-DashScope-OssResourceResolve"); got != "" {
				t.Errorf("X-DashScope-OssResourceResolve=%q want empty", got)
			}
			if got := r.Header.Get("X-DashScope-SSE"); got != "" {
				t.Errorf("X-DashScope-SSE=%q want empty", got)
			}
			var body map[string]any
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Fatal(err)
			}
			messages := body["input"].(map[string]any)["messages"].([]any)
			if len(messages) != 2 {
				t.Fatalf("messages length=%d want 2", len(messages))
			}
			systemContent := messages[0].(map[string]any)["content"].([]any)[0].(map[string]any)
			if systemContent["text"] != "专有词：通义千问" {
				t.Errorf("system content=%v", systemContent)
			}
			audioData := messages[1].(map[string]any)["content"].([]any)[0].(map[string]any)["audio"]
			if want := "data:audio/ogg;base64,YXVkaW8gZGF0YQ=="; audioData != want {
				t.Errorf("audio data=%q want %q", audioData, want)
			}
			parameters := body["parameters"].(map[string]any)
			if parameters["result_format"] != "message" {
				t.Errorf("result_format=%v", parameters["result_format"])
			}
			asrOptions := parameters["asr_options"].(map[string]any)
			if asrOptions["enable_itn"] != true || asrOptions["language"] != "zh" {
				t.Errorf("asr_options=%v", asrOptions)
			}
			if _, exists := asrOptions["enable_lid"]; exists {
				t.Errorf("undocumented enable_lid was sent: %v", asrOptions)
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"output":{"choices":[{"message":{"content":[{"text":"recognized"}]}}]}}`))
		default:
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()

	file, err := os.CreateTemp(t.TempDir(), "segment-*.ogg")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := file.WriteString("audio data"); err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}

	client := New(Config{
		APIKey:     "dashscope-key",
		BaseURL:    server.URL,
		HTTPClient: server.Client(),
	})
	text, err := client.TranscribeFile(
		context.Background(),
		file.Name(),
		"qwen3-asr-flash",
		ASROptions{EnableLID: true, EnableITN: true, Language: "zh"},
		" 专有词：通义千问 ",
	)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := text, "recognized"; got != want {
		t.Fatalf("TranscribeFile()=%q want %q", got, want)
	}
}

func TestTranscribeAudio3ASRFlashRequest(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/services/aigc/multimodal-generation/generation":
			if got := r.Header.Get("X-DashScope-SSE"); got != "disable" {
				t.Errorf("X-DashScope-SSE=%q want disable", got)
			}
			if got := r.Header.Get("X-DashScope-OssResourceResolve"); got != "" {
				t.Errorf("X-DashScope-OssResourceResolve=%q want empty", got)
			}
			var body map[string]any
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Fatal(err)
			}
			if got := body["model"]; got != "qwen-audio-3.1-asr-flash-message" {
				t.Errorf("model=%v", got)
			}
			messages := body["input"].(map[string]any)["messages"].([]any)
			if len(messages) != 2 {
				t.Fatalf("messages length=%d want 2", len(messages))
			}
			contextPart := messages[0].(map[string]any)["content"].([]any)[0].(map[string]any)
			if contextPart["type"] != "input_text" || contextPart["text"] != "专有词：通义千问" {
				t.Errorf("context content=%v", contextPart)
			}
			audioPart := messages[1].(map[string]any)["content"].([]any)[0].(map[string]any)
			if audioPart["type"] != "input_audio" {
				t.Errorf("audio type=%v", audioPart["type"])
			}
			audioData := audioPart["input_audio"].(map[string]any)["data"].(string)
			if want := "data:audio/ogg;base64,YXVkaW8gZGF0YQ=="; audioData != want {
				t.Errorf("audio data=%q want %q", audioData, want)
			}
			parameters := body["parameters"].(map[string]any)
			if parameters["format"] != "ogg" || parameters["sample_rate"] != "16000" {
				t.Errorf("parameters=%v", parameters)
			}
			languageHints := parameters["language_hints"].([]any)
			if len(languageHints) != 1 || languageHints[0] != "zh" {
				t.Errorf("language_hints=%v", languageHints)
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"output":{"text":"新模型识别结果"}}`))
		default:
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()

	file, err := os.CreateTemp(t.TempDir(), "segment-*.ogg")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := file.WriteString("audio data"); err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}

	client := New(Config{
		APIKey:            "dashscope-key",
		BaseURL:           server.URL,
		WebDAVURL:         server.URL + "/dav",
		WebDAVCredentials: "user@password",
		HTTPClient:        server.Client(),
	})
	text, err := client.TranscribeFile(
		context.Background(),
		file.Name(),
		"qwen-audio-3.1-asr-flash-message",
		ASROptions{Language: "zh", SampleRate: 16000},
		" 专有词：通义千问 ",
	)
	if err != nil {
		t.Fatal(err)
	}
	if text != "新模型识别结果" {
		t.Fatalf("TranscribeFile()=%q", text)
	}
}

func TestTranscribeAudio3FiletransUsesPublicURL(t *testing.T) {
	var resourceURL string
	var putCount, deleteCount int
	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPut && strings.HasPrefix(r.URL.Path, "/dav/"):
			putCount++
			resourceURL = server.URL + r.URL.RequestURI()
			w.WriteHeader(http.StatusCreated)
		case r.Method == http.MethodDelete && strings.HasPrefix(r.URL.Path, "/dav/"):
			deleteCount++
			w.WriteHeader(http.StatusNoContent)
		case r.Method == http.MethodPost && r.URL.Path == "/services/audio/asr/transcription":
			if got := r.Header.Get("X-DashScope-Async"); got != "enable" {
				t.Errorf("X-DashScope-Async=%q want enable", got)
			}
			if got := r.Header.Get("X-DashScope-OssResourceResolve"); got != "" {
				t.Errorf("X-DashScope-OssResourceResolve=%q want empty for HTTP URL", got)
			}
			var body map[string]any
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Fatal(err)
			}
			if got := body["model"]; got != "qwen-audio-3.1-asr-flash-filetrans" {
				t.Errorf("model=%v", got)
			}
			input := body["input"].(map[string]any)
			fileURLs := input["file_urls"].([]any)
			if len(fileURLs) != 1 || fileURLs[0] != resourceURL {
				t.Errorf("file_urls=%v want [%q]", fileURLs, resourceURL)
			}
			if strings.HasPrefix(fileURLs[0].(string), "data:") {
				t.Errorf("Filetrans unexpectedly used Base64: %v", fileURLs)
			}
			contextMessages := input["context"].([]any)
			contextPart := contextMessages[0].(map[string]any)["content"].([]any)[0].(map[string]any)
			if contextPart["type"] != "input_text" || contextPart["text"] != "专有词：通义千问" {
				t.Errorf("context=%v", contextPart)
			}
			parameters := body["parameters"].(map[string]any)
			languageHints := parameters["language_hints"].([]any)
			if len(languageHints) != 1 || languageHints[0] != "zh" {
				t.Errorf("language_hints=%v", languageHints)
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"output":{"task_id":"task-filetrans","task_status":"PENDING"}}`))
		case r.Method == http.MethodGet && r.URL.Path == "/tasks/task-filetrans":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"output":{"task_id":"task-filetrans","task_status":"SUCCEEDED","results":[{"subtask_status":"SUCCEEDED","transcription_url":"` + server.URL + `/result.json"}]}}`))
		case r.Method == http.MethodGet && r.URL.Path == "/result.json":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"transcripts":[{"text":"大文件识别结果"}]}`))
		default:
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()

	file, err := os.CreateTemp(t.TempDir(), "segment-*.ogg")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := file.WriteString("audio data"); err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}

	client := New(Config{
		APIKey:            "dashscope-key",
		BaseURL:           server.URL,
		WebDAVURL:         server.URL + "/dav",
		WebDAVCredentials: "user@password",
		HTTPClient:        server.Client(),
	})
	text, err := client.TranscribeFile(
		context.Background(),
		file.Name(),
		"qwen-audio-3.1-asr-flash-filetrans",
		ASROptions{Language: "zh", SampleRate: 16000},
		" 专有词：通义千问 ",
	)
	if err != nil {
		t.Fatal(err)
	}
	if text != "大文件识别结果" {
		t.Fatalf("TranscribeFile()=%q", text)
	}
	if putCount != 1 || deleteCount != 1 {
		t.Fatalf("WebDAV PUT/DELETE counts=%d/%d want 1/1", putCount, deleteCount)
	}
}

func TestAudioDataURI(t *testing.T) {
	file := filepath.Join(t.TempDir(), "sample.wav")
	if err := os.WriteFile(file, []byte{0x00, 0x01, 0x02, 0xff}, 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := audioDataURI(file)
	if err != nil {
		t.Fatal(err)
	}
	if want := "data:audio/wav;base64,AAEC/w=="; got != want {
		t.Fatalf("audioDataURI()=%q want %q", got, want)
	}
}

func TestContentTypeCanonicalAudioTypes(t *testing.T) {
	for _, tt := range []struct {
		path string
		want string
	}{
		{"sample.wav", "audio/wav"},
		{"sample.WAV", "audio/wav"},
		{"sample.ogg", "audio/ogg"},
		{"sample.OGG", "audio/ogg"},
		{"sample.oga", "audio/ogg"},
		{"sample.OGA", "audio/ogg"},
		{"sample", "application/octet-stream"},
		{"sample.qwen-unknown-extension", "application/octet-stream"},
	} {
		t.Run(tt.path, func(t *testing.T) {
			if got := contentType(tt.path); got != tt.want {
				t.Fatalf("contentType(%q)=%q want %q", tt.path, got, tt.want)
			}
		})
	}
}

func TestValidateBase64AudioSizeBoundary(t *testing.T) {
	maxRawSize := maxBase64AudioSize / 4 * 3
	if err := validateBase64AudioSize(maxRawSize); err != nil {
		t.Fatalf("exact limit rejected: %v", err)
	}
	if err := validateBase64AudioSize(maxRawSize + 1); err == nil {
		t.Fatal("one byte over the Base64 limit was accepted")
	}
}

func TestValidateAudioFileSizeUsesUploadMethodLimits(t *testing.T) {
	tests := []struct {
		name  string
		model string
		size  int64
		ok    bool
	}{
		{name: "Base64 at limit", model: "qwen3-asr-flash", size: maxBase64AudioSize / 4 * 3, ok: true},
		{name: "Base64 over limit", model: "fun-asr-flash", size: maxBase64AudioSize/4*3 + 1},
		{name: "URL at limit", model: "fun-asr", size: maxURLAudioSize, ok: true},
		{name: "URL over limit", model: "paraformer-v2", size: maxURLAudioSize + 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			file := filepath.Join(t.TempDir(), "segment.ogg")
			if err := os.WriteFile(file, nil, 0o600); err != nil {
				t.Fatal(err)
			}
			if err := os.Truncate(file, tt.size); err != nil {
				t.Fatal(err)
			}
			err := validateAudioFileSize(file, tt.model)
			if tt.ok && err != nil {
				t.Fatalf("validateAudioFileSize() error=%v", err)
			}
			if !tt.ok && err == nil {
				t.Fatal("validateAudioFileSize() accepted an oversized file")
			}
		})
	}
}

func TestAudioDataURIRejectsOversizedEncodedDataBeforeRead(t *testing.T) {
	file := filepath.Join(t.TempDir(), "oversized.ogg")
	if err := os.WriteFile(file, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Truncate(file, maxBase64AudioSize/4*3+1); err != nil {
		t.Fatal(err)
	}
	_, err := audioDataURI(file)
	if err == nil || !strings.Contains(err.Error(), "超过 10 MiB 限制") {
		t.Fatalf("audioDataURI() error=%v", err)
	}
}

func TestTranscribeFileDoesNotRetryOversizedBase64Audio(t *testing.T) {
	file := filepath.Join(t.TempDir(), "oversized.ogg")
	if err := os.WriteFile(file, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Truncate(file, maxBase64AudioSize/4*3+1); err != nil {
		t.Fatal(err)
	}

	client := New(Config{
		APIKey: "dashscope-key",
		Retry: config.RetryConfig{
			MaxAttempts:  4,
			InitialDelay: time.Hour,
			MaxDelay:     time.Hour,
		},
	})
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	_, err := client.TranscribeFile(ctx, file, "qwen3-asr-flash", ASROptions{}, "")
	if err == nil || !strings.Contains(err.Error(), "超过 10 MiB 限制") {
		t.Fatalf("TranscribeFile() error=%v", err)
	}
	if errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("oversized input entered retry backoff: %v", err)
	}
}
