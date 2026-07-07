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
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"qwen-stt-compatible/internal/config"
)

const (
	multimodalPath = "services/aigc/multimodal-generation/generation"
	transcribePath = "services/audio/asr/transcription"
	uploadsPath    = "uploads"
)

type Config struct {
	APIKey     string
	BaseURL    string
	Timeout    time.Duration
	Retry      config.RetryConfig
	HTTPClient *http.Client
}

type Client struct {
	apiKey string
	base   string
	http   *http.Client
	retry  config.RetryConfig
}

type ASROptions struct {
	EnableLID  bool   `json:"enable_lid"`
	EnableITN  bool   `json:"enable_itn"`
	Language   string `json:"language,omitempty"`
	SampleRate int    `json:"-"`
}

func New(cfg Config) *Client {
	httpClient := cfg.HTTPClient
	if httpClient == nil {
		httpClient = &http.Client{Timeout: cfg.Timeout}
	}
	return &Client{
		apiKey: strings.TrimSpace(cfg.APIKey),
		base:   strings.TrimRight(cfg.BaseURL, "/"),
		http:   httpClient,
		retry:  cfg.Retry,
	}
}

func (c *Client) TranscribeFile(ctx context.Context, path, model string, options ASROptions, prompt string) (string, error) {
	if c.apiKey == "" {
		return "", errors.New("DASHSCOPE_API_KEY 未配置")
	}
	var lastErr error
	delay := c.retry.InitialDelay
	attempts := c.retry.MaxAttempts
	if attempts <= 0 {
		attempts = 1
	}
	for attempt := 1; attempt <= attempts; attempt++ {
		text, err := c.transcribeOnce(ctx, path, model, options, prompt)
		if err == nil {
			return text, nil
		}
		lastErr = err
		if attempt < attempts {
			if delay <= 0 {
				delay = 500 * time.Millisecond
			}
			timer := time.NewTimer(minDuration(delay, c.retry.MaxDelay))
			select {
			case <-ctx.Done():
				timer.Stop()
				return "", ctx.Err()
			case <-timer.C:
			}
			factor := c.retry.Factor
			if factor <= 0 {
				factor = 2
			}
			delay = time.Duration(float64(delay) * factor)
		}
	}
	return "", lastErr
}

func (c *Client) transcribeOnce(ctx context.Context, path, model string, options ASROptions, prompt string) (string, error) {
	switch modelFamily(model) {
	case "qwen3-asr-flash":
		return c.transcribeQwen3Flash(ctx, path, model, options, prompt)
	case "fun-asr-flash":
		return c.transcribeFunASRFlash(ctx, path, model, options)
	case "fun-asr", "paraformer":
		return c.transcribeAsyncTask(ctx, path, model, options)
	default:
		return "", fmt.Errorf("不支持的模型前缀: %q", model)
	}
}

func (c *Client) transcribeQwen3Flash(ctx context.Context, path, model string, options ASROptions, prompt string) (string, error) {
	ossURL, err := c.upload(ctx, model, path)
	if err != nil {
		return "", err
	}
	body := map[string]any{
		"model": model,
		"input": map[string]any{
			"messages": []any{
				map[string]any{
					"role":    "system",
					"content": []any{map[string]any{"text": prompt}},
				},
				map[string]any{
					"role":    "user",
					"content": []any{map[string]any{"audio": ossURL}},
				},
			},
		},
		"parameters": map[string]any{
			"result_format": "message",
			"asr_options":   options,
		},
	}
	var response dashScopeResponse
	if err := c.doJSON(ctx, http.MethodPost, c.endpoint(multimodalPath), body, &response, map[string]string{
		"X-DashScope-OssResourceResolve": "enable",
		"X-DashScope-SSE":                "disable",
	}); err != nil {
		return "", err
	}
	if response.Code != "" {
		return "", fmt.Errorf("%s: %s", response.Code, response.Message)
	}
	text := extractText(response.Output)
	if strings.TrimSpace(text) == "" {
		return "【该段音频转录出错】", nil
	}
	return text, nil
}

func (c *Client) transcribeFunASRFlash(ctx context.Context, path, model string, options ASROptions) (string, error) {
	ossURL, err := c.upload(ctx, model, path)
	if err != nil {
		return "", err
	}
	sampleRate := options.SampleRate
	if sampleRate <= 0 {
		sampleRate = 16000
	}
	body := map[string]any{
		"model": model,
		"input": map[string]any{
			"messages": []any{
				map[string]any{
					"role": "user",
					"content": []any{
						map[string]any{
							"type": "input_audio",
							"input_audio": map[string]any{
								"data": ossURL,
							},
						},
					},
				},
			},
		},
		"parameters": map[string]any{
			"format":      audioFormat(path),
			"sample_rate": fmt.Sprintf("%d", sampleRate),
		},
	}
	var response dashScopeResponse
	if err := c.doJSON(ctx, http.MethodPost, c.endpoint(multimodalPath), body, &response, map[string]string{
		"X-DashScope-OssResourceResolve": "enable",
		"X-DashScope-SSE":                "disable",
	}); err != nil {
		return "", err
	}
	if response.Code != "" {
		return "", fmt.Errorf("%s: %s", response.Code, response.Message)
	}
	text := extractText(response.Output)
	if strings.TrimSpace(text) == "" {
		return "【该段音频转录出错】", nil
	}
	return text, nil
}

func (c *Client) transcribeAsyncTask(ctx context.Context, path, model string, options ASROptions) (string, error) {
	ossURL, err := c.upload(ctx, model, path)
	if err != nil {
		return "", err
	}
	parameters := map[string]any{}
	if options.Language != "" {
		parameters["language_hints"] = []string{options.Language}
	}
	body := map[string]any{
		"model": model,
		"input": map[string]any{
			"file_urls": []string{ossURL},
		},
		"parameters": parameters,
	}
	var launched asyncTaskResponse
	if err := c.doJSON(ctx, http.MethodPost, c.endpoint(transcribePath), body, &launched, map[string]string{
		"X-DashScope-Async":              "enable",
		"X-DashScope-OssResourceResolve": "enable",
	}); err != nil {
		return "", err
	}
	if launched.Code != "" {
		return "", fmt.Errorf("%s: %s", launched.Code, launched.Message)
	}
	taskID := launched.taskID()
	if taskID == "" {
		return "", errors.New("DashScope 异步任务缺少 task_id")
	}
	done, err := c.waitTask(ctx, taskID)
	if err != nil {
		return "", err
	}
	return c.collectAsyncTaskText(ctx, done)
}

func (c *Client) waitTask(ctx context.Context, taskID string) (asyncTaskResponse, error) {
	wait := time.Second
	step := 0
	for {
		var response asyncTaskResponse
		if err := c.doJSON(ctx, http.MethodGet, c.endpoint("tasks/"+url.PathEscape(taskID)), nil, &response, nil); err != nil {
			return asyncTaskResponse{}, err
		}
		if response.Code != "" {
			return asyncTaskResponse{}, fmt.Errorf("%s: %s", response.Code, response.Message)
		}
		switch response.Output.TaskStatus {
		case "SUCCEEDED":
			return response, nil
		case "FAILED", "CANCELED", "UNKNOWN":
			message := response.Output.Message
			if message == "" {
				message = response.Message
			}
			return asyncTaskResponse{}, fmt.Errorf("DashScope 任务失败: task_id=%s status=%s message=%s", taskID, response.Output.TaskStatus, message)
		}

		timer := time.NewTimer(wait)
		select {
		case <-ctx.Done():
			timer.Stop()
			return asyncTaskResponse{}, ctx.Err()
		case <-timer.C:
		}
		step++
		if step%3 == 0 && wait < 5*time.Second {
			wait *= 2
			if wait > 5*time.Second {
				wait = 5 * time.Second
			}
		}
	}
}

func (c *Client) collectAsyncTaskText(ctx context.Context, response asyncTaskResponse) (string, error) {
	if len(response.Output.Results) == 0 {
		return "", errors.New("DashScope 异步任务结果为空")
	}
	var builder strings.Builder
	for _, item := range response.Output.Results {
		if item.SubtaskStatus != "" && item.SubtaskStatus != "SUCCEEDED" {
			return "", fmt.Errorf("DashScope 子任务失败: status=%s message=%s", item.SubtaskStatus, item.Message)
		}
		if item.TranscriptionURL == "" {
			continue
		}
		var payload any
		if err := c.getJSON(ctx, item.TranscriptionURL, &payload); err != nil {
			return "", err
		}
		builder.WriteString(extractTranscriptionText(payload))
	}
	text := strings.TrimSpace(builder.String())
	if text == "" {
		return "【该段音频转录出错】", nil
	}
	return text, nil
}

type dashScopeResponse struct {
	RequestID string         `json:"request_id"`
	Output    map[string]any `json:"output"`
	Usage     map[string]any `json:"usage"`
	Code      string         `json:"code"`
	Message   string         `json:"message"`
}

type asyncTaskResponse struct {
	RequestID string          `json:"request_id"`
	TaskID    string          `json:"task_id"`
	Output    asyncTaskOutput `json:"output"`
	Code      string          `json:"code"`
	Message   string          `json:"message"`
}

func (r asyncTaskResponse) taskID() string {
	if r.Output.TaskID != "" {
		return r.Output.TaskID
	}
	return r.TaskID
}

type asyncTaskOutput struct {
	TaskID     string         `json:"task_id"`
	TaskStatus string         `json:"task_status"`
	Message    string         `json:"message"`
	Results    []asyncResult  `json:"results"`
	Raw        map[string]any `json:"-"`
}

type asyncResult struct {
	FileURL          string `json:"file_url"`
	SubtaskStatus    string `json:"subtask_status"`
	TranscriptionURL string `json:"transcription_url"`
	Message          string `json:"message"`
}

type uploadPolicyResponse struct {
	Output  uploadPolicy `json:"output"`
	Data    uploadPolicy `json:"data"`
	Code    string       `json:"code"`
	Message string       `json:"message"`
}

type uploadPolicy struct {
	OSSAccessKeyID      string `json:"oss_access_key_id"`
	Signature           string `json:"signature"`
	Policy              string `json:"policy"`
	UploadDir           string `json:"upload_dir"`
	XOSSObjectACL       string `json:"x_oss_object_acl"`
	XOSSForbidOverwrite string `json:"x_oss_forbid_overwrite"`
	UploadHost          string `json:"upload_host"`
}

func (c *Client) upload(ctx context.Context, model, path string) (string, error) {
	policy, err := c.getUploadPolicy(ctx, model)
	if err != nil {
		return "", err
	}
	key := strings.TrimRight(policy.UploadDir, "/") + "/" + filepath.Base(path)
	body := &bytes.Buffer{}
	writer := multipart.NewWriter(body)
	fields := map[string]string{
		"OSSAccessKeyId":         policy.OSSAccessKeyID,
		"Signature":              policy.Signature,
		"policy":                 policy.Policy,
		"key":                    key,
		"x-oss-object-acl":       policy.XOSSObjectACL,
		"x-oss-forbid-overwrite": policy.XOSSForbidOverwrite,
		"success_action_status":  "200",
		"x-oss-content-type":     contentType(path),
	}
	for name, value := range fields {
		if err := writer.WriteField(name, value); err != nil {
			return "", err
		}
	}
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer file.Close()
	part, err := writer.CreateFormFile("file", filepath.Base(path))
	if err != nil {
		return "", err
	}
	if _, err := io.Copy(part, file); err != nil {
		return "", err
	}
	if err := writer.Close(); err != nil {
		return "", err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, policy.UploadHost, body)
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", writer.FormDataContentType())
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Date", time.Now().UTC().Format(http.TimeFormat))
	req.Header.Set("User-Agent", "qwen-stt-compatible")
	resp, err := c.http.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		data, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return "", fmt.Errorf("上传 OSS 失败: status=%d body=%s", resp.StatusCode, strings.TrimSpace(string(data)))
	}
	return "oss://" + key, nil
}

func (c *Client) getUploadPolicy(ctx context.Context, model string) (uploadPolicy, error) {
	u, err := url.Parse(c.endpoint(uploadsPath))
	if err != nil {
		return uploadPolicy{}, err
	}
	q := u.Query()
	q.Set("action", "getPolicy")
	q.Set("model", model)
	u.RawQuery = q.Encode()
	var response uploadPolicyResponse
	if err := c.doJSON(ctx, http.MethodGet, u.String(), nil, &response, nil); err != nil {
		return uploadPolicy{}, err
	}
	if response.Code != "" {
		return uploadPolicy{}, fmt.Errorf("%s: %s", response.Code, response.Message)
	}
	policy := response.Output
	if policy.UploadHost == "" && response.Data.UploadHost != "" {
		policy = response.Data
	}
	if policy.UploadHost == "" || policy.UploadDir == "" {
		return uploadPolicy{}, errors.New("DashScope 上传策略缺少 upload_host 或 upload_dir")
	}
	return policy, nil
}

func (c *Client) doJSON(ctx context.Context, method, endpoint string, payload any, out any, extraHeaders map[string]string) error {
	var body io.Reader
	if payload != nil {
		data, err := json.Marshal(payload)
		if err != nil {
			return err
		}
		body = bytes.NewReader(data)
	}
	req, err := http.NewRequestWithContext(ctx, method, endpoint, body)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/json; charset=utf-8")
	req.Header.Set("Authorization", "Bearer "+c.apiKey)
	req.Header.Set("User-Agent", "qwen-stt-compatible")
	if payload != nil {
		req.Header.Set("Content-Type", "application/json; charset=utf-8")
	}
	for key, value := range extraHeaders {
		req.Header.Set(key, value)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		data, _ := io.ReadAll(io.LimitReader(resp.Body, 8192))
		return fmt.Errorf("DashScope HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(data)))
	}
	decoder := json.NewDecoder(resp.Body)
	decoder.UseNumber()
	return decoder.Decode(out)
}

func (c *Client) getJSON(ctx context.Context, endpoint string, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/json; charset=utf-8")
	req.Header.Set("User-Agent", "qwen-stt-compatible")
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		data, _ := io.ReadAll(io.LimitReader(resp.Body, 8192))
		return fmt.Errorf("下载 DashScope 转写结果失败: HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(data)))
	}
	decoder := json.NewDecoder(resp.Body)
	decoder.UseNumber()
	return decoder.Decode(out)
}

func (c *Client) endpoint(path string) string {
	return c.base + "/" + strings.TrimLeft(path, "/")
}

func modelFamily(model string) string {
	key := strings.ToLower(strings.TrimSpace(model))
	switch {
	case strings.HasPrefix(key, "fun-asr-flash"):
		return "fun-asr-flash"
	case strings.HasPrefix(key, "fun-asr"):
		return "fun-asr"
	case strings.HasPrefix(key, "paraformer"):
		return "paraformer"
	case strings.HasPrefix(key, "qwen3-asr-flash"):
		return "qwen3-asr-flash"
	default:
		return ""
	}
}

func contentType(path string) string {
	if typ := mime.TypeByExtension(strings.ToLower(filepath.Ext(path))); typ != "" {
		return typ
	}
	if strings.HasSuffix(strings.ToLower(path), ".ogg") {
		return "audio/ogg"
	}
	return "application/octet-stream"
}

func audioFormat(path string) string {
	ext := strings.TrimPrefix(strings.ToLower(filepath.Ext(path)), ".")
	switch ext {
	case "oga":
		return "ogg"
	case "m4a":
		return "mp4"
	case "":
		return "wav"
	default:
		return ext
	}
}

func extractText(output map[string]any) string {
	if output == nil {
		return ""
	}
	if choices, ok := output["choices"].([]any); ok && len(choices) > 0 {
		if choice, ok := choices[0].(map[string]any); ok {
			if message, ok := choice["message"].(map[string]any); ok {
				if content, ok := message["content"].([]any); ok {
					var builder strings.Builder
					for _, item := range content {
						if part, ok := item.(map[string]any); ok {
							if text, ok := part["text"].(string); ok {
								builder.WriteString(text)
							}
						}
					}
					if builder.Len() > 0 {
						return builder.String()
					}
				}
			}
		}
	}
	return deepFindText(output)
}

func extractTranscriptionText(value any) string {
	if text := collectTranscripts(value); text != "" {
		return text
	}
	if text := collectSentences(value); text != "" {
		return text
	}
	return deepFindText(value)
}

func collectTranscripts(value any) string {
	switch v := value.(type) {
	case map[string]any:
		if transcripts, ok := v["transcripts"].([]any); ok {
			var builder strings.Builder
			for _, item := range transcripts {
				if part, ok := item.(map[string]any); ok {
					if text, ok := part["text"].(string); ok {
						builder.WriteString(text)
					}
				}
			}
			if builder.Len() > 0 {
				return builder.String()
			}
			for _, item := range transcripts {
				builder.WriteString(collectSentences(item))
			}
			return builder.String()
		}
		for _, item := range v {
			if text := collectTranscripts(item); text != "" {
				return text
			}
		}
	case []any:
		var builder strings.Builder
		for _, item := range v {
			builder.WriteString(collectTranscripts(item))
		}
		return builder.String()
	}
	return ""
}

func collectSentences(value any) string {
	switch v := value.(type) {
	case map[string]any:
		if sentences, ok := v["sentences"].([]any); ok {
			var builder strings.Builder
			for _, item := range sentences {
				if part, ok := item.(map[string]any); ok {
					if text, ok := part["text"].(string); ok {
						builder.WriteString(text)
					}
				}
			}
			return builder.String()
		}
		for _, item := range v {
			if text := collectSentences(item); text != "" {
				return text
			}
		}
	case []any:
		var builder strings.Builder
		for _, item := range v {
			builder.WriteString(collectSentences(item))
		}
		return builder.String()
	}
	return ""
}

func deepFindText(value any) string {
	switch v := value.(type) {
	case map[string]any:
		if text, ok := v["text"].(string); ok {
			return text
		}
		for _, item := range v {
			if text := deepFindText(item); text != "" {
				return text
			}
		}
	case []any:
		for _, item := range v {
			if text := deepFindText(item); text != "" {
				return text
			}
		}
	}
	return ""
}

func minDuration(a, b time.Duration) time.Duration {
	if b <= 0 || a < b {
		return a
	}
	return b
}

func RequestID() string {
	var data [16]byte
	if _, err := rand.Read(data[:]); err != nil {
		return fmt.Sprintf("%d", time.Now().UnixNano())
	}
	return hex.EncodeToString(data[:])
}
