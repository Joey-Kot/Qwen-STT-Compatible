// Copyright (C) 2026 Joey Kot <joey.kot.x@gmail.com>
// SPDX-License-Identifier: GPL-3.0-or-later

package realtime

import (
	"bytes"
	"context"
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

func qwenTestServer(t *testing.T, serve func(*websocket.Conn, map[string]any)) *httptest.Server {
	t.Helper()
	u := websocket.Upgrader{}
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("model") != "qwen3-asr-flash-realtime" || r.Header.Get("Authorization") != "Bearer test-key" || r.Header.Get("X-DashScope-WorkSpace") != "workspace" {
			t.Error("model/credentials not propagated")
		}
		c, err := u.Upgrade(w, r, nil)
		if err != nil {
			t.Error(err)
			return
		}
		defer c.Close()
		c.SetReadDeadline(time.Now().Add(3 * time.Second))
		c.WriteJSON(map[string]any{"type": "session.created"})
		var update map[string]any
		if err := c.ReadJSON(&update); err != nil {
			t.Error(err)
			return
		}
		if update["type"] != "session.update" || update["event_id"] == "" {
			t.Error(update)
			return
		}
		serve(c, update["session"].(map[string]any))
	}))
}

func qwenTestClient(address string) *Client {
	return New(Config{QwenURL: strings.Replace(address, "http", "ws", 1) + "?model=replace-me", APIKey: "test-key", Workspace: "workspace", StartTimeout: 200 * time.Millisecond, FinishTimeout: 200 * time.Millisecond})
}

func qwenOptions(vad bool) Options {
	return Options{Model: "qwen3-asr-flash-realtime", SampleRate: 16000, Language: "yue", VAD: vad, SilenceMS: 400}
}

func TestQwenLifecycle(t *testing.T) {
	for _, vad := range []bool{false, true} {
		t.Run(map[bool]string{false: "manual", true: "vad"}[vad], func(t *testing.T) {
			pcm := bytes.Repeat([]byte{0x12, 0x34}, 3201)
			server := qwenTestServer(t, func(c *websocket.Conn, settings map[string]any) {
				if settings["sample_rate"] != float64(16000) || settings["input_audio_format"] != "pcm" {
					t.Error(settings)
				}
				if settings["input_audio_transcription"].(map[string]any)["language"] != "yue" {
					t.Error(settings)
				}
				if vad {
					if settings["turn_detection"].(map[string]any)["silence_duration_ms"] != float64(400) {
						t.Error(settings)
					}
				} else if settings["turn_detection"] != nil {
					t.Error("manual VAD enabled")
				}
				c.WriteJSON(map[string]any{"type": "session.updated"})
				var received []byte
				for len(received) < len(pcm) {
					var e map[string]any
					if err := c.ReadJSON(&e); err != nil {
						t.Error(err)
						return
					}
					if e["type"] != "input_audio_buffer.append" || e["event_id"] == "" {
						t.Error(e)
						return
					}
					data, err := base64.StdEncoding.DecodeString(e["audio"].(string))
					if err != nil || len(data) > 3200 {
						t.Error("invalid Base64 audio")
					}
					received = append(received, data...)
				}
				if !bytes.Equal(received, pcm) {
					t.Error("audio changed")
				}
				finishTypes := []string{"session.finish"}
				if !vad {
					finishTypes = append([]string{"input_audio_buffer.commit"}, finishTypes...)
				}
				for _, typ := range finishTypes {
					var e map[string]any
					if err := c.ReadJSON(&e); err != nil {
						t.Error(err)
						return
					}
					if e["type"] != typ {
						t.Errorf("%v want %s", e, typ)
						return
					}
				}
				c.WriteJSON(map[string]any{"type": "input_audio_buffer.committed", "item_id": "one"})
				for _, text := range []string{"你", "你", "你好"} {
					c.WriteJSON(map[string]any{"type": "conversation.item.input_audio_transcription.text", "item_id": "one", "text": text, "stash": "不会输出"})
				}
				for range 2 {
					c.WriteJSON(map[string]any{"type": "conversation.item.input_audio_transcription.completed", "item_id": "one", "transcript": "你好。"})
				}
				c.WriteJSON(map[string]any{"type": "session.finished"})
			})
			defer server.Close()
			s, err := qwenTestClient(server.URL).Start(context.Background(), qwenOptions(vad))
			if err != nil {
				t.Fatal(err)
			}
			defer s.Close()
			if err = s.SendAudio([]byte{1}); err == nil {
				t.Fatal("unaligned PCM accepted")
			}
			if err = s.UpdatePrompt("test"); err == nil {
				t.Fatal("prompt accepted")
			}
			if err = s.SendAudio(pcm); err != nil {
				t.Fatal(err)
			}
			if err = s.Finish(); err != nil {
				t.Fatal(err)
			}
			if err = s.SendAudio(pcm); err == nil {
				t.Fatal("send after finish")
			}
			var text string
			completed := 0
			for {
				e := nextEvent(t, s)
				if e.Err != nil {
					t.Fatal(e.Err)
				}
				if e.Finished {
					break
				}
				if e.Item != nil {
					text += e.Item.Delta
					if e.Item.Kind == ItemCompleted {
						completed++
					}
				}
			}
			if text != "你好。" || completed != 1 {
				t.Fatalf("text=%s completed=%d", text, completed)
			}
		})
	}
}

func TestQwenTimeoutCancellationAndFailures(t *testing.T) {
	for _, mode := range []string{"start", "finish", "cancel", "error", "early", "incomplete", "empty", "failed"} {
		t.Run(mode, func(t *testing.T) {
			server := qwenTestServer(t, func(c *websocket.Conn, _ map[string]any) {
				if mode == "start" {
					var v any
					c.ReadJSON(&v)
					return
				}
				c.WriteJSON(map[string]any{"type": "session.updated"})
				if mode == "cancel" {
					var v any
					c.ReadJSON(&v)
					return
				}
				if mode == "early" {
					c.WriteJSON(map[string]any{"type": "session.finished"})
					return
				}
				var finish any
				if err := c.ReadJSON(&finish); err != nil {
					return
				}
				switch mode {
				case "finish":
					c.ReadJSON(&finish)
				case "error":
					c.WriteJSON(map[string]any{"type": "error", "error": map[string]any{"code": "test", "message": "failed"}})
				case "incomplete":
					c.WriteJSON(map[string]any{"type": "input_audio_buffer.committed", "item_id": "one"})
					c.WriteJSON(map[string]any{"type": "session.finished"})
				case "empty":
					c.WriteJSON(map[string]any{"type": "session.finished"})
				case "failed":
					c.WriteJSON(map[string]any{"type": "conversation.item.input_audio_transcription.failed", "item_id": "one", "error": map[string]any{"code": "test", "message": "failed"}})
					c.WriteJSON(map[string]any{"type": "session.finished"})
				}
			})
			defer server.Close()
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			s, err := qwenTestClient(server.URL).Start(ctx, qwenOptions(true))
			if mode == "start" {
				if err == nil {
					s.Close()
					t.Fatal("start did not timeout")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			defer s.Close()
			if mode == "cancel" {
				cancel()
				select {
				case <-s.Events():
				case <-time.After(time.Second):
					t.Fatal("cancel did not stop reader")
				}
				return
			}
			if mode != "early" {
				if err = s.Finish(); err != nil {
					t.Fatal(err)
				}
			}
			for {
				e := nextEvent(t, s)
				if mode == "failed" && e.Item != nil {
					if e.Item.Kind != ItemFailed || e.Item.Err == nil {
						t.Fatal(e)
					}
					continue
				}
				if e.Item != nil {
					continue
				}
				if mode == "empty" || mode == "failed" {
					if !e.Finished || e.Err != nil {
						t.Fatal(e)
					}
				} else if e.Err == nil {
					t.Fatal("missing error", e)
				}
				break
			}
		})
	}
}

func TestQwenPrefixValidationAndBounds(t *testing.T) {
	s := &qwenSession{items: map[string]*qwenItem{}}
	for _, id := range []string{"one", "two"} {
		e, err := s.convert(qwenWireEvent{Type: "conversation.item.input_audio_transcription.text", ItemID: id, Text: "你好"})
		if err != nil || e.Delta != "你好" {
			t.Fatal(e, err)
		}
	}
	if _, err := s.convert(qwenWireEvent{Type: "conversation.item.input_audio_transcription.text", ItemID: "one", Text: "您好"}); err == nil {
		t.Fatal("confirmed prefix rewrite accepted")
	}
	if _, err := s.convert(qwenWireEvent{Type: "input_audio_buffer.speech_stopped", ItemID: "one"}); err == nil {
		t.Fatal("missing boundary accepted")
	}
	if _, err := s.convert(qwenWireEvent{Type: "conversation.item.input_audio_transcription.text", ItemID: "two", Text: strings.Repeat("x", (1<<20)+1)}); err == nil {
		t.Fatal("oversize text accepted")
	}
	for _, rate := range []int{8000, 16000, 24000} {
		o := qwenOptions(false)
		o.SampleRate = rate
		if err := o.Validate(); (err == nil) != (rate != 24000) {
			t.Fatal(rate, err)
		}
	}
	o := qwenOptions(false)
	o.Prompt = "context"
	if o.Validate() == nil {
		t.Fatal("prompt supported")
	}
}
