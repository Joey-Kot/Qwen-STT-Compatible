// Copyright (C) 2026 Joey Kot <joey.kot.x@gmail.com>
// SPDX-License-Identifier: GPL-3.0-or-later

package dashscope

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"qwen-stt-compatible/internal/config"
	"qwen-stt-compatible/internal/requestlog"
)

type diagnosticLogs struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (b *diagnosticLogs) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.b.Write(p)
}

func (b *diagnosticLogs) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.b.String()
}

func captureDiagnosticLogs(t *testing.T) *diagnosticLogs {
	t.Helper()
	var output diagnosticLogs
	previous := log.Writer()
	log.SetOutput(&output)
	t.Cleanup(func() { log.SetOutput(previous) })
	return &output
}

func requireLogFields(t *testing.T, output string, fields ...string) {
	t.Helper()
	for _, field := range fields {
		if !strings.Contains(output, field) {
			t.Errorf("missing %q in logs:\n%s", field, output)
		}
	}
}

func TestHTTPDiagnosticsSeparateHeadersAndBody(t *testing.T) {
	logs := captureDiagnosticLogs(t)
	releaseBody := make(chan struct{})
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Request-Id", "upstream-http-id")
		w.WriteHeader(http.StatusOK)
		w.(http.Flusher).Flush()
		select {
		case <-releaseBody:
			_, _ = io.WriteString(w, "payload")
		case <-r.Context().Done():
		}
	}))
	server.StartTLS()
	defer server.Close()
	defer close(releaseBody)

	httpClient := newHTTPClient(time.Second)
	defer httpClient.CloseIdleConnections()
	httpClient.Transport.(*http.Transport).TLSClientConfig = &tls.Config{InsecureSkipVerify: true} // #nosec G402 -- local test server only.
	client := New(Config{HTTPClient: httpClient})
	ctx := requestlog.WithAttempt(requestlog.WithSegment(requestlog.WithRequestID(context.Background(), "incoming-1"), 3), 2)
	// localhost exercises DNS as well as TCP/TLS tracing against a local server.
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.Replace(server.URL, "127.0.0.1", "localhost", 1), nil)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := client.do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	beforeBody := logs.String()
	requireLogFields(t, beforeBody, "request=incoming-1 segment=3 attempt=2", "upstream dns", "upstream connect", "upstream tls", "conn_wait=", "write_duration=", "ttfb=", "response_wait=", `upstream_request_id="upstream-http-id"`)
	if strings.Contains(beforeBody, "upstream body") {
		t.Fatal("body was reported complete before it arrived")
	}
	releaseBody <- struct{}{}
	data, err := io.ReadAll(resp.Body)
	if err != nil || string(data) != "payload" {
		t.Fatalf("body=%q error=%v", data, err)
	}
	if err := resp.Body.Close(); err != nil {
		t.Fatal(err)
	}
	output := logs.String()
	requireLogFields(t, output, "outcome=eof bytes=7", "body_duration=", "total_duration=")
	if strings.Count(output, "upstream body") != 1 {
		t.Fatalf("body completion logged more than once:\n%s", output)
	}
}

func TestHTTPDiagnosticsLogBodyTimeout(t *testing.T) {
	logs := captureDiagnosticLogs(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, "{")
		w.(http.Flusher).Flush()
		<-r.Context().Done()
	}))
	defer server.Close()
	httpClient := newHTTPClient(100 * time.Millisecond)
	defer httpClient.CloseIdleConnections()
	client := New(Config{HTTPClient: httpClient})
	var payload any
	err := client.getJSON(context.Background(), server.URL+"?Signature=private-signature", &payload)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("expected body timeout, got %v", err)
	}
	output := logs.String()
	requireLogFields(t, output, "upstream response", "status=200", "upstream body", "outcome=error", "bytes=1", "deadline exceeded")
	if strings.Contains(output, "private-signature") {
		t.Fatalf("signed URL leaked:\n%s", output)
	}
}

func TestWaitTaskLogsLifecycleAndFailureDetails(t *testing.T) {
	for _, terminal := range []string{"SUCCEEDED", "FAILED"} {
		t.Run(terminal, func(t *testing.T) {
			logs := captureDiagnosticLogs(t)
			polls := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				polls++
				status := terminal
				if polls == 1 {
					status = "PENDING"
				} else if polls == 2 {
					status = "RUNNING"
				}
				output := asyncTaskOutput{TaskID: "task-1", TaskStatus: status, SubmitTime: "2026-09-28 18:44:23.000"}
				if polls >= 2 {
					output.ScheduledTime = "2026-09-28 18:44:26.000"
				}
				if polls == 3 {
					output.EndTime = "2026-09-28 18:44:28.000"
					if terminal == "FAILED" {
						output.Code = "FILE_DOWNLOAD_FAILED"
						output.Message = "cannot fetch https://reader:private-password@files.example.com/audio.ogg?signature=private-signature"
						output.Results = []asyncResult{{SubtaskStatus: "FAILED", Code: "FILE_403_FORBIDDEN", Message: "anonymous download denied"}}
					}
				}
				_ = json.NewEncoder(w).Encode(asyncTaskResponse{RequestID: fmt.Sprintf("upstream-%d", polls), Output: output})
			}))
			defer server.Close()
			client := New(Config{BaseURL: server.URL, HTTPClient: server.Client()})
			ctx := requestlog.WithRequestID(context.Background(), "incoming-1")
			_, err := client.waitTask(ctx, "task-1")
			if terminal == "SUCCEEDED" && err != nil {
				t.Fatal(err)
			}
			if terminal == "FAILED" && (err == nil || !strings.Contains(err.Error(), "FILE_DOWNLOAD_FAILED")) {
				t.Fatalf("missing task failure: %v", err)
			}
			output := logs.String()
			requireLogFields(t, output, "request=incoming-1", `task_status="PENDING"`, `task_status="RUNNING"`, `task_status="`+terminal+`"`, "poll=3", "polls=3", "next_poll=1s", "next_poll=0s", "queue_duration=3s", "execution_duration=2s", `upstream_request_id="upstream-3"`)
			if terminal == "FAILED" {
				requireLogFields(t, output, "FILE_DOWNLOAD_FAILED", "FILE_403_FORBIDDEN", "anonymous download denied", "outcome=error")
			}
			for _, secret := range []string{"private-password", "private-signature"} {
				if strings.Contains(output, secret) {
					t.Errorf("secret %q leaked in logs:\n%s", secret, output)
				}
			}
		})
	}
}

func TestWaitTaskLogsCancellationDuringPollingDelay(t *testing.T) {
	logs := captureDiagnosticLogs(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, `{"output":{"task_status":"RUNNING"}}`)
	}))
	defer server.Close()
	client := New(Config{BaseURL: server.URL, HTTPClient: server.Client()})
	done := make(chan error, 1)
	go func() {
		_, err := client.waitTask(ctx, "cancel-task")
		done <- err
	}()
	deadline := time.After(2 * time.Second)
	ticker := time.NewTicker(time.Millisecond)
	defer ticker.Stop()
	for !strings.Contains(logs.String(), "next_poll=1s") {
		select {
		case <-ticker.C:
		case <-deadline:
			t.Fatal("task never entered polling delay")
		}
	}
	cancel()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatalf("got %v, want cancellation", err)
	}
	requireLogFields(t, logs.String(), "async task complete", `task_status="RUNNING"`, "polls=1", "outcome=canceled")
}

func TestTranscribeLogsRetryAndFinalFailure(t *testing.T) {
	logs := captureDiagnosticLogs(t)
	file := filepath.Join(t.TempDir(), "audio.ogg")
	if err := os.WriteFile(file, []byte("audio"), 0o600); err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = io.WriteString(w, `{"code":"Unavailable","message":"try again private-api-key"}`)
	}))
	defer server.Close()
	client := New(Config{APIKey: "private-api-key", BaseURL: server.URL, HTTPClient: server.Client(), Retry: config.RetryConfig{MaxAttempts: 2, InitialDelay: time.Millisecond, MaxDelay: time.Millisecond}})
	ctx := requestlog.WithSegment(requestlog.WithRequestID(context.Background(), "incoming-2"), 4)
	_, err := client.TranscribeFile(ctx, file, "qwen3-asr-flash", ASROptions{}, "private-prompt")
	if err == nil {
		t.Fatal("expected final failure")
	}
	output := logs.String()
	requireLogFields(t, output, "request=incoming-2 segment=4 attempt=1", "asr retry next_attempt=2 delay=1ms", "attempt=2 asr attempt complete", "asr complete", "outcome=error", "Unavailable")
	for _, secret := range []string{"private-api-key", "private-prompt"} {
		if strings.Contains(output, secret) {
			t.Fatalf("secret %q leaked:\n%s", secret, output)
		}
	}
}

func TestTaskTimeDifferenceLeavesUnknownTimesUnset(t *testing.T) {
	for _, pair := range [][2]string{{"", ""}, {"invalid", "2026-09-28 18:44:23"}, {"2026-09-28 18:44:24", "2026-09-28 18:44:23"}} {
		if got := taskTimeDifference(pair[0], pair[1]); got != "-" {
			t.Errorf("%v: got %s, want unknown", pair, got)
		}
	}
}

func TestHTTPErrorLogsOnlyDiagnosticFields(t *testing.T) {
	for _, body := range []string{
		`{"code":"Forbidden","request_id":"up-error","message":"cannot fetch https:\/\/reader:private-password@files.example.com\/audio?signature=private-signature","output":{"text":"private-transcript"},"prompt":"private-prompt"}`,
		`<Error><Code>Forbidden</Code><RequestId>up-error</RequestId><Message>cannot fetch https://reader:private-password@files.example.com/audio?signature=private-signature</Message><Payload>private-transcript</Payload></Error>`,
	} {
		err := &httpResponseError{summary: "HTTP 403", body: []byte(body)}
		message := requestlog.Error(context.Background(), fmt.Errorf("segment failed: %w", err))
		requireLogFields(t, message, "HTTP 403", "Forbidden", "up-error", "cannot fetch", "https://files.example.com/audio")
		for _, secret := range []string{"private-password", "private-signature", "private-transcript", "private-prompt"} {
			if strings.Contains(message, secret) {
				t.Errorf("%q leaked: %s", secret, message)
			}
		}
	}
	err := &httpResponseError{summary: "HTTP 502", body: []byte("<html>private-proxy-payload</html>"), contentType: "text/html"}
	if message := requestlog.Error(context.Background(), err); strings.Contains(message, "private-proxy-payload") || !strings.Contains(message, "content_type=text/html") {
		t.Fatalf("unexpected proxy diagnostic: %s", message)
	}
}

func TestCleanupAfterCancellationKeepsRequestCorrelation(t *testing.T) {
	logs := captureDiagnosticLogs(t)
	deleted := make(chan struct{}, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodDelete {
			t.Errorf("unexpected method %s", r.Method)
		}
		deleted <- struct{}{}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()
	client := New(Config{WebDAVURL: server.URL, WebDAVCredentials: "reader@private-password", HTTPClient: server.Client()})
	ctx, cancel := context.WithCancel(requestlog.WithRequestID(context.Background(), "incoming-canceled"))
	cancel()
	client.cleanupUploadedFile(ctx, uploadedFile{webDAVURL: server.URL + "/audio.ogg"})
	select {
	case <-deleted:
	default:
		t.Fatal("cancellation prevented temporary file cleanup")
	}
	requireLogFields(t, logs.String(), "request=incoming-canceled upstream request method=DELETE", "status=204")
}
