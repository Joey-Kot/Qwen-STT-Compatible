// Copyright (C) 2026 Joey Kot <joey.kot.x@gmail.com>
// SPDX-License-Identifier: GPL-3.0-or-later

package realtime

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

func TestParaformerLifecycle(t *testing.T) {
	for _, tc := range []struct {
		model string
		rate  int
		v1    bool
	}{
		{"paraformer-realtime-v2", 24000, false},
		{"paraformer-realtime-v1", 16000, true},
		{"paraformer-realtime-8k-v2", 8000, false},
		{"paraformer-realtime-8k-v1", 8000, true},
	} {
		t.Run(tc.model, func(t *testing.T) {
			server := upstreamServer(t, func(conn *websocket.Conn, id string, run map[string]any) {
				params := run["parameters"].(map[string]any)
				if params["format"] != "pcm" || params["sample_rate"] != float64(tc.rate) || len(run["input"].(map[string]any)) != 0 {
					t.Error(run)
				}
				_, heartbeat := params["heartbeat"]
				_, silence := params["max_sentence_silence"]
				if heartbeat == tc.v1 || silence == tc.v1 {
					t.Error(params)
				}
				wire(conn, id, "task-started", nil)
				kind, audio, err := conn.ReadMessage()
				if err != nil || kind != websocket.BinaryMessage || len(audio) != 1600 {
					t.Errorf("audio %d %d %v", kind, len(audio), err)
					return
				}
				wire(conn, id, "result-generated", map[string]any{"heartbeat": true})
				wire(conn, id, "result-generated", map[string]any{"begin_time": 0, "text": "草稿"})
				first := map[string]any{"begin_time": 0, "sentence_end": true, "text": "确认", "end_time": nil, "words": []any{map[string]any{"end_time": 100}}}
				wire(conn, id, "result-generated", first)
				wire(conn, id, "result-generated", first)
				wire(conn, id, "result-generated", map[string]any{"begin_time": 200, "sentence_end": true, "text": "第二句", "end_time": 300})
				wire(conn, id, "result-generated", first) // Older duplicate after a later sentence.
				var finish map[string]any
				if err := conn.ReadJSON(&finish); err != nil {
					t.Error(err)
					return
				}
				if finish["header"].(map[string]any)["action"] != "finish-task" {
					t.Error(finish)
				}
				wire(conn, id, "result-generated", map[string]any{"begin_time": 400, "sentence_end": true, "text": "尾句"})
				wire(conn, id, "task-finished", nil)
			})
			defer server.Close()
			silence := 500
			if tc.v1 {
				silence = 0
			}
			s, err := testClient(server.URL).Start(context.Background(), Options{Model: tc.model, SampleRate: tc.rate, SilenceMS: silence, Language: "yue"})
			if err != nil {
				t.Fatal(err)
			}
			defer s.Close()
			if err := s.UpdatePrompt(""); err == nil {
				t.Fatal("accepted continue-task")
			}
			if err := s.SendAudio(make([]byte, 1600)); err != nil {
				t.Fatal(err)
			}
			if e := nextEvent(t, s); e.Sentence == nil || e.Sentence.ID != 1 || !e.Sentence.Begin || e.Sentence.Final {
				t.Fatal(e)
			}
			if e := nextEvent(t, s); e.Sentence == nil || e.Sentence.Text != "确认" || e.Sentence.EndTime == nil || *e.Sentence.EndTime != 100 {
				t.Fatal(e)
			}
			if e := nextEvent(t, s); e.Sentence == nil || e.Sentence.ID != 2 || e.Sentence.Text != "第二句" {
				t.Fatal(e)
			}
			if err := s.Finish(); err != nil {
				t.Fatal(err)
			}
			if e := nextEvent(t, s); e.Sentence == nil || e.Sentence.ID != 3 || e.Sentence.Text != "尾句" {
				t.Fatal(e)
			}
			if e := nextEvent(t, s); !e.Finished {
				t.Fatal(e)
			}
		})
	}
}

func TestParaformerValidation(t *testing.T) {
	for _, o := range []Options{
		{Model: "paraformer-realtime-v1", SampleRate: 24000},
		{Model: "paraformer-realtime-v1", SampleRate: 16000, SilenceMS: 1300},
		{Model: "paraformer-realtime-8k-v2", SampleRate: 16000},
		{Model: "paraformer-realtime-v2", SampleRate: 24000, Language: "es"},
		{Model: "paraformer-realtime-v2", SampleRate: 24000, Prompt: "词表"},
	} {
		if err := o.Validate(); err == nil {
			t.Errorf("accepted %+v", o)
		}
	}
	if err := (Options{Model: "paraformer-realtime-v2", SampleRate: 44100}).Validate(); err != nil {
		t.Fatal(err)
	}
}

func TestParaformerSentenceBoundaries(t *testing.T) {
	for _, tc := range []struct {
		raw    string
		end    int64
		absent bool
	}{
		{`{"begin_time":10,"sentence_end":true,"end_time":50,"words":[{"end_time":40}]}`, 50, false},
		{`{"begin_time":10,"sentence_end":true,"end_time":null,"words":[{"end_time":null},{"end_time":40},{"end_time":30}]}`, 40, false},
		{`{"begin_time":10,"sentence_end":true,"end_time":-1,"words":[{"end_time":5}]}`, 0, true},
	} {
		var s Sentence
		if err := json.Unmarshal([]byte(tc.raw), &s); err != nil {
			t.Fatal(err)
		}
		p := &paraformerSentences{}
		got, err := p.normalize(&s)
		if err != nil || got == nil {
			t.Fatal(got, err)
		}
		if tc.absent {
			if got.EndTime != nil {
				t.Fatal(got)
			}
		} else if got.EndTime == nil || *got.EndTime != tc.end {
			t.Fatal(got)
		}
	}
	p := &paraformerSentences{}
	p.normalize(&Sentence{BeginTime: 10})
	if _, err := p.normalize(&Sentence{BeginTime: 20}); err == nil {
		t.Fatal("accepted missing final")
	}
}

func TestParaformerUnfinishedSentenceFails(t *testing.T) {
	server := upstreamServer(t, func(conn *websocket.Conn, id string, _ map[string]any) {
		wire(conn, id, "task-started", nil)
		wire(conn, id, "result-generated", map[string]any{"begin_time": 0, "text": "草稿"})
		conn.ReadMessage()
		wire(conn, id, "task-finished", nil)
	})
	defer server.Close()
	s, err := testClient(server.URL).Start(context.Background(), Options{Model: "paraformer-realtime-v2", SampleRate: 24000})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	nextEvent(t, s)
	if err := s.Finish(); err != nil {
		t.Fatal(err)
	}
	if e := nextEvent(t, s); e.Err == nil || e.Finished {
		t.Fatal(e)
	}
}

func TestParaformerFailureAndCancellation(t *testing.T) {
	for _, phase := range []string{"start_timeout", "finish_timeout", "task-failed", "disconnect", "cancel"} {
		t.Run(phase, func(t *testing.T) {
			closed := make(chan struct{})
			server := upstreamServer(t, func(conn *websocket.Conn, id string, _ map[string]any) {
				defer close(closed)
				if phase == "start_timeout" {
					conn.ReadMessage()
					return
				}
				wire(conn, id, "task-started", nil)
				switch phase {
				case "task-failed":
					wire(conn, id, "task-failed", nil)
				case "finish_timeout":
					conn.ReadMessage()
					conn.ReadMessage()
				case "cancel":
					conn.ReadMessage()
				}
			})
			defer server.Close()
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			s, err := testClient(server.URL).Start(ctx, Options{Model: "paraformer-realtime-v2", SampleRate: 24000})
			if phase == "start_timeout" {
				if err == nil {
					s.Close()
					t.Fatal("start did not time out")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			defer s.Close()
			if phase == "cancel" {
				cancel()
				select {
				case <-closed:
				case <-time.After(time.Second):
					t.Fatal("cancel did not close connection")
				}
				return
			}
			if phase == "finish_timeout" {
				if err := s.Finish(); err != nil {
					t.Fatal(err)
				}
			}
			if e := nextEvent(t, s); e.Err == nil || e.Finished {
				t.Fatal(e)
			}
		})
	}
}
