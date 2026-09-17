// Copyright (C) 2026 Joey Kot <joey.kot.x@gmail.com>
// SPDX-License-Identifier: GPL-3.0-or-later

package httpapi

import (
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"qwen-stt-compatible/internal/config"
	"qwen-stt-compatible/internal/models"
	"qwen-stt-compatible/internal/realtime"
)

func TestParaformerSessionSilenceCapabilities(t *testing.T) {
	c := realtimeSettings{Model: "paraformer-realtime-v1", SilenceMS: 1300}
	if err := c.options().Validate(); err != nil {
		t.Fatal(err)
	}
	for _, raw := range []string{
		`{"audio":{"input":{"turn_detection":{"type":"server_vad","silence_duration_ms":1300}}}}`,
		`{"audio":{"input":{"transcription":{"prompt":"热词"}}}}`,
		`{"audio":{"input":{"transcription":{"language":"es"}}}}`,
	} {
		if _, err := c.update(json.RawMessage(raw)); err == nil {
			t.Fatal("accepted", raw)
		}
	}
	next, err := c.update(json.RawMessage(`{"audio":{"input":{"turn_detection":{"type":"server_vad"}}}}`))
	if err != nil || next.options().SilenceMS != 0 {
		t.Fatal(next, err)
	}
	audio := next.session("test")["audio"].(map[string]any)["input"].(map[string]any)
	if _, ok := audio["turn_detection"].(map[string]any)["silence_duration_ms"]; ok {
		t.Fatal(audio)
	}
	// A model switch must not silently discard an explicitly requested setting.
	c.Model, c.SilenceExplicit, c.SilenceMS = "paraformer-realtime-v2", true, 500
	if _, err := c.update(json.RawMessage(`{"audio":{"input":{"transcription":{"model":"paraformer-realtime-v1"}}}}`)); err == nil {
		t.Fatal("ignored explicit silence")
	}
	if _, err := c.update(json.RawMessage(`{"audio":{"input":{"transcription":{"model":"paraformer-realtime-v1"},"turn_detection":null}}}`)); err != nil {
		t.Fatal(err)
	}
}

func TestParaformerVADMissingEndFailsWithoutCompletion(t *testing.T) {
	c, f, _ := setupRealtimeWS(t)
	sendWS(t, c, map[string]any{"type": "session.update", "session": map[string]any{"audio": map[string]any{"input": map[string]any{"transcription": map[string]any{"model": "paraformer-realtime-v2"}, "turn_detection": map[string]any{"type": "server_vad"}}}}})
	readWS(t, c, "session.updated")
	task := startAudio(t, c, f)
	task.events <- sentence("缺少结束时间", 1)
	readWS(t, c, "input_audio_buffer.speech_started")
	readWS(t, c, "error")
	select {
	case <-task.closed:
	case <-time.After(time.Second):
		t.Fatal("failed task not closed")
	}
}

func TestParaformerWebSocketEndToEnd(t *testing.T) {
	for _, model := range []string{"paraformer-realtime-v2", "paraformer-realtime-v1", "paraformer-realtime-8k-v2", "paraformer-realtime-8k-v1"} {
		for _, vad := range []bool{false, true} {
			name := model + "/manual"
			if vad {
				name = model + "/vad"
			}
			t.Run(name, func(t *testing.T) {
				rate, _ := models.SampleRate(model)
				upgrader := websocket.Upgrader{}
				up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					conn, err := upgrader.Upgrade(w, r, nil)
					if err != nil {
						t.Error(err)
						return
					}
					defer conn.Close()
					conn.SetReadDeadline(time.Now().Add(3 * time.Second))
					var run struct {
						Header struct {
							ID string `json:"task_id"`
						} `json:"header"`
					}
					if err := conn.ReadJSON(&run); err != nil {
						t.Error(err)
						return
					}
					emit := func(event string, sentence any) {
						if err := conn.WriteJSON(map[string]any{"header": map[string]any{"task_id": run.Header.ID, "event": event}, "payload": map[string]any{"output": map[string]any{"sentence": sentence}}}); err != nil {
							t.Error(err)
						}
					}
					emit("task-started", nil)
					for received := 0; received < rate*3/5; {
						kind, data, err := conn.ReadMessage()
						if err != nil || kind != websocket.BinaryMessage {
							t.Errorf("audio %d %v", kind, err)
							return
						}
						received += len(data)
						if received > rate*3/5 {
							t.Error("wrong resampled length", received)
						}
					}
					first := map[string]any{"begin_time": 0, "sentence_end": true, "text": "第一句", "end_time": nil, "words": []any{map[string]any{"end_time": 100}}}
					emit("result-generated", map[string]any{"begin_time": 0, "text": "可改写草稿"})
					emit("result-generated", first)
					emit("result-generated", first)
					emit("result-generated", map[string]any{"begin_time": 100, "sentence_end": true, "text": "第二句", "end_time": 200})
					var finish map[string]any
					if err := conn.ReadJSON(&finish); err != nil {
						t.Error(err)
						return
					}
					if finish["header"].(map[string]any)["action"] != "finish-task" {
						t.Error(finish)
					}
					emit("result-generated", map[string]any{"begin_time": 200, "sentence_end": true, "text": "尾句", "end_time": 300})
					emit("task-finished", nil)
				}))
				defer up.Close()
				s := New(config.Config{APITokens: []string{"key"}, DashScopeAPIKey: "upstream-key", Realtime: realtime.Config{URL: strings.Replace(up.URL, "http", "ws", 1)}}, nil)
				down := httptest.NewServer(s)
				defer down.Close()
				c, _, err := websocket.DefaultDialer.Dial(strings.Replace(down.URL, "http", "ws", 1)+"/v1/realtime?model="+model, http.Header{"Authorization": {"Bearer key"}})
				if err != nil {
					t.Fatal(err)
				}
				defer c.Close()
				readWS(t, c, "session.created")
				if vad {
					sendWS(t, c, map[string]any{"type": "session.update", "session": map[string]any{"audio": map[string]any{"input": map[string]any{"turn_detection": map[string]any{"type": "server_vad"}}}}})
					readWS(t, c, "session.updated")
				}
				sendWS(t, c, map[string]any{"type": "input_audio_buffer.append", "audio": base64.StdEncoding.EncodeToString(make([]byte, 14400))})
				var previous any
				if vad {
					for _, want := range []string{"第一句", "第二句"} {
						start := readWS(t, c, "input_audio_buffer.speech_started")
						readWS(t, c, "input_audio_buffer.speech_stopped")
						commit := readWS(t, c, "input_audio_buffer.committed")
						if commit["item_id"] != start["item_id"] || commit["previous_item_id"] != previous {
							t.Fatal(commit)
						}
						previous = commit["item_id"]
						readWS(t, c, "conversation.item.added")
						if e := readWS(t, c, "conversation.item.input_audio_transcription.delta"); e["delta"] != want {
							t.Fatal(e)
						}
						if e := readWS(t, c, "conversation.item.input_audio_transcription.completed"); e["transcript"] != want {
							t.Fatal(e)
						}
					}
				}
				// 100ms remain after VAD's two finalized sentences.
				sendWS(t, c, map[string]any{"type": "input_audio_buffer.commit"})
				commit := readWS(t, c, "input_audio_buffer.committed")
				if commit["previous_item_id"] != previous {
					t.Fatal(commit)
				}
				readWS(t, c, "conversation.item.added")
				var text strings.Builder
				for {
					c.SetReadDeadline(time.Now().Add(3 * time.Second))
					var event map[string]any
					if err := c.ReadJSON(&event); err != nil {
						t.Fatal(err)
					}
					if event["type"] == "conversation.item.input_audio_transcription.completed" {
						want := "第一句第二句尾句"
						if vad {
							want = "尾句"
						}
						if event["transcript"] != want || text.String() != want {
							t.Fatal(event, text.String())
						}
						break
					}
					if event["type"] != "conversation.item.input_audio_transcription.delta" {
						t.Fatal(event)
					}
					text.WriteString(event["delta"].(string))
				}
			})
		}
	}
}
