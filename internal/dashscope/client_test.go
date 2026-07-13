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
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path"
	"strings"
	"testing"
)

func TestModelFamilyPrefersFunASRFlashBeforeFunASR(t *testing.T) {
	tests := []struct {
		model string
		want  string
	}{
		{model: "fun-asr-flash-2026-06-15", want: "fun-asr-flash"},
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

func TestTranscribeFileUsesWebDAVAndCleansUp(t *testing.T) {
	var putCount, deleteCount, policyCount int
	var resourceURL string
	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPut && strings.HasPrefix(r.URL.Path, "/dav/"):
			putCount++
			username, password, ok := r.BasicAuth()
			if !ok || username != "user" || password != "pass@word" {
				t.Errorf("PUT basic auth = %q/%q, ok=%v", username, password, ok)
			}
			data, err := io.ReadAll(r.Body)
			if err != nil {
				t.Error(err)
			}
			if string(data) != "audio data" {
				t.Errorf("PUT body = %q", data)
			}
			resourceURL = server.URL + r.URL.RequestURI()
			w.WriteHeader(http.StatusCreated)
		case r.Method == http.MethodDelete && strings.HasPrefix(r.URL.Path, "/dav/"):
			deleteCount++
			username, password, ok := r.BasicAuth()
			if !ok || username != "user" || password != "pass@word" {
				t.Errorf("DELETE basic auth = %q/%q, ok=%v", username, password, ok)
			}
			w.WriteHeader(http.StatusNoContent)
		case r.Method == http.MethodGet && r.URL.Path == "/uploads":
			policyCount++
			w.WriteHeader(http.StatusInternalServerError)
		case r.Method == http.MethodPost && r.URL.Path == "/services/aigc/multimodal-generation/generation":
			var body map[string]any
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Fatal(err)
			}
			messages := body["input"].(map[string]any)["messages"].([]any)
			audioURL := messages[1].(map[string]any)["content"].([]any)[0].(map[string]any)["audio"].(string)
			if audioURL != resourceURL {
				t.Errorf("DashScope audio URL = %q want %q", audioURL, resourceURL)
			}
			if strings.Contains(audioURL, "user") || strings.Contains(audioURL, "pass") {
				t.Errorf("DashScope audio URL contains WebDAV credentials: %q", audioURL)
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
		APIKey:            "dashscope-key",
		BaseURL:           server.URL,
		WebDAVURL:         server.URL + "/dav",
		WebDAVCredentials: "user@pass@word",
		HTTPClient:        server.Client(),
	})
	text, err := client.TranscribeFile(context.Background(), file.Name(), "qwen3-asr-flash", ASROptions{}, "")
	if err != nil {
		t.Fatal(err)
	}
	if got, want := text, "recognized"; got != want {
		t.Fatalf("TranscribeFile()=%q want %q", got, want)
	}
	if putCount != 1 || deleteCount != 1 {
		t.Fatalf("WebDAV PUT/DELETE counts = %d/%d want 1/1", putCount, deleteCount)
	}
	if policyCount != 0 {
		t.Fatalf("DashScope upload policy requested %d times", policyCount)
	}
	if base := path.Base(strings.TrimPrefix(resourceURL, server.URL+"/dav/")); !strings.HasSuffix(base, ".ogg") {
		t.Errorf("WebDAV object name %q does not preserve audio extension", base)
	}
}
