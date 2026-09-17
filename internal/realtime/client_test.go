// Copyright (C) 2026 Joey Kot <joey.kot.x@gmail.com>
// SPDX-License-Identifier: GPL-3.0-or-later

package realtime

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

func upstreamServer(t *testing.T, serve func(*websocket.Conn, string, map[string]any)) *httptest.Server {
	t.Helper()
	u := websocket.Upgrader{}
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer test-key" || r.Header.Get("X-DashScope-WorkSpace") != "workspace" {
			t.Error("missing upstream credentials/workspace")
		}
		conn, err := u.Upgrade(w, r, nil)
		if err != nil {
			t.Error(err)
			return
		}
		defer conn.Close()
		conn.SetReadDeadline(time.Now().Add(3 * time.Second))
		var run struct {
			Header  struct{ Action, TaskID, Streaming string } `json:"-"`
			Payload map[string]any                             `json:"payload"`
		}
		var raw map[string]json.RawMessage
		if err := conn.ReadJSON(&raw); err != nil {
			t.Error(err)
			return
		}
		var header struct {
			Action    string `json:"action"`
			TaskID    string `json:"task_id"`
			Streaming string `json:"streaming"`
		}
		json.Unmarshal(raw["header"], &header)
		json.Unmarshal(raw["payload"], &run.Payload)
		if header.Action != "run-task" || header.Streaming != "duplex" || len(header.TaskID) != 36 {
			t.Errorf("invalid run header: %+v", header)
			return
		}
		serve(conn, header.TaskID, run.Payload)
	}))
}

func wire(conn *websocket.Conn, id, typ string, sentence any) error {
	payload := map[string]any{}
	if sentence != nil {
		payload["output"] = map[string]any{"sentence": sentence}
	}
	return conn.WriteJSON(map[string]any{"header": map[string]any{"event": typ, "task_id": id}, "payload": payload})
}

func testClient(url string) *Client {
	return New(Config{URL: strings.Replace(url, "http", "ws", 1), APIKey: "test-key", Workspace: "workspace", StartTimeout: 200 * time.Millisecond, FinishTimeout: 200 * time.Millisecond})
}

func nextEvent(t *testing.T, s Session) Event {
	t.Helper()
	select {
	case e, ok := <-s.Events():
		if !ok {
			t.Fatal("events closed")
		}
		return e
	case <-time.After(2 * time.Second):
		t.Fatal("event timeout")
		return Event{}
	}
}

func TestDuplexLifecycleAndFinalDeduplication(t *testing.T) {
	server := upstreamServer(t, func(conn *websocket.Conn, id string, run map[string]any) {
		parameters := run["parameters"].(map[string]any)
		if parameters["format"] != "pcm" || parameters["sample_rate"] != float64(24000) || parameters["heartbeat"] != true {
			t.Errorf("parameters=%v", parameters)
		}
		if _, exists := parameters["enable_itn"]; exists {
			t.Error("offline option leaked")
		}
		if run["input"].(map[string]any)["context"] == nil {
			t.Error("missing context")
		}
		wire(conn, id, "task-started", nil)
		kind, data, err := conn.ReadMessage()
		if err != nil || kind != websocket.BinaryMessage || len(data) != 4 {
			t.Errorf("audio: %d %v", kind, err)
			return
		}
		wire(conn, id, "result-generated", map[string]any{"sentence_id": 0, "heartbeat": true})
		wire(conn, id, "result-generated", map[string]any{"sentence_id": 1, "sentence_begin": true, "text": "草稿"})
		wire(conn, id, "result-generated", map[string]any{"sentence_id": 1, "sentence_end": true, "text": "最终文本"})
		wire(conn, id, "result-generated", map[string]any{"sentence_id": 1, "sentence_end": true, "text": "最终文本"})
		var finish map[string]any
		if err := conn.ReadJSON(&finish); err != nil {
			t.Error(err)
			return
		}
		if finish["header"].(map[string]any)["action"] != "finish-task" {
			t.Errorf("finish=%v", finish)
		}
		wire(conn, id, "result-generated", map[string]any{"sentence_id": 2, "sentence_end": true, "text": "尾句"})
		wire(conn, id, "task-finished", nil)
	})
	defer server.Close()
	s, err := testClient(server.URL).Start(context.Background(), Options{Model: "qwen-audio-3.0-asr-flash-streaming", SampleRate: 24000, Language: "zh", Prompt: "词表"})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if err := s.SendAudio([]byte{1, 0, 2, 0}); err != nil {
		t.Fatal(err)
	}
	if e := nextEvent(t, s); e.Sentence == nil || e.Sentence.Final {
		t.Fatalf("intermediate=%+v", e)
	}
	if e := nextEvent(t, s); e.Sentence == nil || e.Sentence.Text != "最终文本" {
		t.Fatalf("final=%+v", e)
	}
	if err := s.Finish(); err != nil {
		t.Fatal(err)
	}
	if err := s.SendAudio([]byte{1, 0}); err == nil {
		t.Fatal("audio accepted after finish")
	}
	if e := nextEvent(t, s); e.Sentence == nil || e.Sentence.Text != "尾句" {
		t.Fatalf("tail=%+v", e)
	}
	if e := nextEvent(t, s); !e.Finished {
		t.Fatalf("finish=%+v", e)
	}
}

func TestStartAndFinishTimeouts(t *testing.T) {
	for _, phase := range []string{"start", "finish"} {
		t.Run(phase, func(t *testing.T) {
			server := upstreamServer(t, func(conn *websocket.Conn, id string, _ map[string]any) {
				if phase == "finish" {
					wire(conn, id, "task-started", nil)
					conn.ReadMessage()
				}
				conn.ReadMessage() // Wait until client closes on timeout.
			})
			defer server.Close()
			s, err := testClient(server.URL).Start(context.Background(), Options{Model: "fun-asr-realtime", SampleRate: 24000})
			if phase == "start" {
				if err == nil {
					s.Close()
					t.Fatal("start timeout not enforced")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			defer s.Close()
			if err := s.Finish(); err != nil {
				t.Fatal(err)
			}
			if e := nextEvent(t, s); e.Err == nil || e.Finished {
				t.Fatalf("timeout=%+v", e)
			}
		})
	}
}

func TestCancelClosesUpstream(t *testing.T) {
	closed := make(chan struct{})
	server := upstreamServer(t, func(conn *websocket.Conn, id string, _ map[string]any) {
		wire(conn, id, "task-started", nil)
		conn.ReadMessage()
		close(closed)
	})
	defer server.Close()
	ctx, cancel := context.WithCancel(context.Background())
	s, err := testClient(server.URL).Start(ctx, Options{Model: "fun-asr-realtime", SampleRate: 24000})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	cancel()
	select {
	case <-closed:
	case <-time.After(time.Second):
		t.Fatal("cancel did not close upstream")
	}
}

func TestTaskFailureAndUnexpectedFinish(t *testing.T) {
	for _, typ := range []string{"task-failed", "task-finished"} {
		t.Run(typ, func(t *testing.T) {
			server := upstreamServer(t, func(conn *websocket.Conn, id string, _ map[string]any) {
				wire(conn, id, "task-started", nil)
				wire(conn, id, typ, nil)
			})
			defer server.Close()
			s, err := testClient(server.URL).Start(context.Background(), Options{Model: "fun-asr-realtime", SampleRate: 24000})
			if err != nil {
				t.Fatal(err)
			}
			defer s.Close()
			if e := nextEvent(t, s); e.Err == nil {
				t.Fatalf("expected error: %+v", e)
			}
		})
	}
}
