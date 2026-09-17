// Copyright (C) 2026 Joey Kot <joey.kot.x@gmail.com>
// SPDX-License-Identifier: GPL-3.0-or-later

package httpapi

import (
	"bufio"
	"bytes"
	"encoding/base64"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"qwen-stt-compatible/internal/config"
	"qwen-stt-compatible/internal/realtime"
)

func setupQwenWS(t *testing.T, vad bool) (*websocket.Conn, *fakeRealtime) {
	c, f, _ := setupRealtimeWS(t)
	var turn any
	if vad {
		turn = map[string]any{"type": "server_vad", "silence_duration_ms": 400}
	}
	sendWS(t, c, map[string]any{"type": "session.update", "session": map[string]any{"audio": map[string]any{"input": map[string]any{"transcription": map[string]any{"model": "qwen3-asr-flash-realtime", "language": "yue"}, "turn_detection": turn}}}})
	readWS(t, c, "session.updated")
	return c, f
}

func qwenEvent(kind realtime.ItemKind, id, text, delta string, ms int64) realtime.Event {
	return realtime.Event{Item: &realtime.ItemEvent{Kind: kind, ID: id, Text: text, Delta: delta, TimeMS: ms}}
}

func TestQwenManualMappingAndOutOfOrderTurns(t *testing.T) {
	c, f := setupQwenWS(t, false)
	first := startAudio(t, c, f)
	if first.options.SampleRate != 16000 || first.options.VAD {
		t.Fatal(first.options)
	}
	if pcm := <-first.audio; len(pcm) != 3200 {
		t.Fatal("resampler count", len(pcm))
	}
	sendWS(t, c, map[string]any{"type": "input_audio_buffer.commit"})
	one := readWS(t, c, "input_audio_buffer.committed")["item_id"]
	readWS(t, c, "conversation.item.added")
	second := startAudio(t, c, f)
	sendWS(t, c, map[string]any{"type": "input_audio_buffer.commit"})
	e := readWS(t, c, "input_audio_buffer.committed")
	two := e["item_id"]
	if e["previous_item_id"] != one {
		t.Fatal(e)
	}
	readWS(t, c, "conversation.item.added")
	for _, task := range []*fakeSession{second, first} {
		task.events <- qwenEvent(realtime.ItemCommitted, "same-upstream-id", "", "", 0)
		task.events <- qwenEvent(realtime.TextDelta, "same-upstream-id", "你", "你", 0)
		if e := readWS(t, c, "conversation.item.input_audio_transcription.delta"); e["delta"] != "你" {
			t.Fatal(e)
		}
		task.events <- qwenEvent(realtime.ItemCompleted, "same-upstream-id", "你好", "好", 0)
		readWS(t, c, "conversation.item.input_audio_transcription.delta")
		e := readWS(t, c, "conversation.item.input_audio_transcription.completed")
		want := one
		if task == second {
			want = two
		}
		if e["item_id"] != want || e["transcript"] != "你好" {
			t.Fatal(e)
		}
		task.events <- realtime.Event{Finished: true}
	}
}

func TestQwenVADBoundariesAndExplicitTail(t *testing.T) {
	c, f := setupQwenWS(t, true)
	task := startAudio(t, c, f)
	if !task.options.VAD {
		t.Fatal("VAD not propagated")
	}
	task.events <- qwenEvent(realtime.SpeechStarted, "one", "", "", 0)
	one := readWS(t, c, "input_audio_buffer.speech_started")["item_id"]
	task.events <- qwenEvent(realtime.SpeechStopped, "one", "", "", 100)
	readWS(t, c, "input_audio_buffer.speech_stopped")
	task.events <- qwenEvent(realtime.ItemCommitted, "one", "", "", 0)
	if e := readWS(t, c, "input_audio_buffer.committed"); e["item_id"] != one {
		t.Fatal(e)
	}
	readWS(t, c, "conversation.item.added")
	task.events <- qwenEvent(realtime.TextDelta, "one", "第一", "第一", 0)
	readWS(t, c, "conversation.item.input_audio_transcription.delta")
	sendWS(t, c, map[string]any{"type": "input_audio_buffer.commit"})
	readWS(t, c, "error") // Auto-committed audio does not count towards a new tail.
	sendWS(t, c, map[string]any{"type": "input_audio_buffer.append", "audio": base64.StdEncoding.EncodeToString(make([]byte, 4752))})
	sendWS(t, c, map[string]any{"type": "input_audio_buffer.commit"})
	readWS(t, c, "error")
	sendWS(t, c, map[string]any{"type": "input_audio_buffer.append", "audio": base64.StdEncoding.EncodeToString(make([]byte, 48))})
	sendWS(t, c, map[string]any{"type": "input_audio_buffer.commit"})
	select {
	case <-task.finished:
	case <-time.After(time.Second):
		t.Fatal("tail not flushed")
	}
	task.events <- qwenEvent(realtime.SpeechStarted, "two", "", "", 100)
	two := readWS(t, c, "input_audio_buffer.speech_started")["item_id"]
	task.events <- qwenEvent(realtime.SpeechStopped, "two", "", "", 200)
	readWS(t, c, "input_audio_buffer.speech_stopped")
	task.events <- qwenEvent(realtime.ItemCommitted, "two", "", "", 0)
	if e := readWS(t, c, "input_audio_buffer.committed"); e["previous_item_id"] != one {
		t.Fatal(e)
	}
	readWS(t, c, "conversation.item.added")
	// A later item may finish before the first item's final result.
	task.events <- qwenEvent(realtime.ItemCompleted, "two", "第二句", "第二句", 0)
	readWS(t, c, "conversation.item.input_audio_transcription.delta")
	if e := readWS(t, c, "conversation.item.input_audio_transcription.completed"); e["item_id"] != two {
		t.Fatal(e)
	}
	task.events <- qwenEvent(realtime.ItemCompleted, "one", "第一句", "句", 0)
	readWS(t, c, "conversation.item.input_audio_transcription.delta")
	if e := readWS(t, c, "conversation.item.input_audio_transcription.completed"); e["item_id"] != one {
		t.Fatal(e)
	}
	task.events <- realtime.Event{Finished: true}
}

func TestQwenClearPreservesCommittedItems(t *testing.T) {
	c, f := setupQwenWS(t, true)
	task := startAudio(t, c, f)
	task.events <- qwenEvent(realtime.ItemCommitted, "keep", "", "", 0)
	one := readWS(t, c, "input_audio_buffer.committed")["item_id"]
	readWS(t, c, "conversation.item.added")
	sendWS(t, c, map[string]any{"type": "input_audio_buffer.clear"})
	readWS(t, c, "input_audio_buffer.cleared")
	select {
	case <-task.closed:
		t.Fatal("committed task cancelled")
	default:
	}
	// Clear must suppress the uncommitted tail even if finishing produces results.
	task.events <- qwenEvent(realtime.ItemCommitted, "discard", "", "", 0)
	task.events <- qwenEvent(realtime.ItemCompleted, "discard", "丢弃", "丢弃", 0)
	task.events <- qwenEvent(realtime.ItemCompleted, "keep", "保留", "保留", 0)
	if e := readWS(t, c, "conversation.item.input_audio_transcription.delta"); e["delta"] != "保留" {
		t.Fatal(e)
	}
	if e := readWS(t, c, "conversation.item.input_audio_transcription.completed"); e["item_id"] != one {
		t.Fatal(e)
	}
	task.events <- realtime.Event{Finished: true}
	next := startAudio(t, c, f)
	sendWS(t, c, map[string]any{"type": "input_audio_buffer.clear"})
	readWS(t, c, "input_audio_buffer.cleared")
	select {
	case <-next.closed:
	case <-time.After(time.Second):
		t.Fatal("uncommitted task not cancelled")
	}
	next.events <- qwenEvent(realtime.ItemCompleted, "late", "迟到", "迟到", 0)
	sendWS(t, c, map[string]any{"type": "session.update", "session": map[string]any{"type": "transcription"}})
	readWS(t, c, "session.updated")
}

func TestQwenItemFailureDoesNotCompleteOrCloseSession(t *testing.T) {
	c, f := setupQwenWS(t, false)
	task := startAudio(t, c, f)
	sendWS(t, c, map[string]any{"type": "input_audio_buffer.commit"})
	readWS(t, c, "input_audio_buffer.committed")
	readWS(t, c, "conversation.item.added")
	e := qwenEvent(realtime.ItemFailed, "bad", "", "", 0)
	e.Item.Err = errors.New("recognition failed")
	task.events <- e
	readWS(t, c, "conversation.item.input_audio_transcription.failed")
	task.events <- realtime.Event{Finished: true}
	sendWS(t, c, map[string]any{"type": "session.update", "session": map[string]any{"type": "transcription"}})
	readWS(t, c, "session.updated")
}

func TestQwenFileOrderedPrefixOutput(t *testing.T) {
	f := orderedFileItems{items: map[string]*fileItem{}}
	for _, e := range []*realtime.ItemEvent{
		{Kind: realtime.SpeechStarted, ID: "one"}, {Kind: realtime.SpeechStarted, ID: "two"},
		{Kind: realtime.ItemCompleted, ID: "two", Text: "第二句", Delta: "第二句"},
	} {
		if delta, err := f.accept(e); err != nil || delta != "" {
			t.Fatal(delta, err)
		}
	}
	if delta, err := f.accept(&realtime.ItemEvent{Kind: realtime.TextDelta, ID: "one", Text: "第一", Delta: "第一"}); err != nil || delta != "第一" {
		t.Fatal(delta, err)
	}
	if delta, err := f.accept(&realtime.ItemEvent{Kind: realtime.ItemCompleted, ID: "one", Text: "第一句", Delta: "句"}); err != nil || delta != "句第二句" || len(f.order) != 0 {
		t.Fatal(delta, err)
	}
}

func TestQwenFileStreamsBeforeUploadFinishes(t *testing.T) {
	task := newFakeSession()
	s := New(config.Config{}, nil)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		err := s.realtimeFileResponse(w, r, task, io.NopCloser(bytes.NewReader(make([]byte, 32000))), 16000, true)
		if err != nil && !errors.Is(err, errResponseWritten) {
			s.handleProcessError(w, err)
		}
	}))
	defer server.Close()
	go func() { <-task.audio; task.events <- qwenEvent(realtime.TextDelta, "one", "提前", "提前", 0) }()
	client := http.Client{Timeout: 3 * time.Second}
	response, err := client.Get(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	reader := bufio.NewReader(response.Body)
	line, err := reader.ReadString('\n')
	if err != nil || !strings.Contains(line, "提前") {
		t.Fatal(line, err)
	}
	select {
	case <-task.finished:
		t.Fatal("first delta waited for upload")
	default:
	}
	task.events <- qwenEvent(realtime.ItemCompleted, "one", "提前输出", "输出", 0)
	<-task.finished
	task.events <- realtime.Event{Finished: true}
	rest, err := io.ReadAll(reader)
	if err != nil || !strings.Contains(string(rest), "transcript.text.done") || !strings.Contains(string(rest), "提前输出") || strings.Contains(string(rest), "[DONE]") {
		t.Fatal(string(rest), err)
	}
}

func TestQwenVADFlushOrdersLaterManualCommits(t *testing.T) {
	for _, model := range []string{"qwen3-asr-flash-realtime", "fun-asr-realtime"} {
		t.Run(model, func(t *testing.T) {
			c, f := setupQwenWS(t, true)
			first := startAudio(t, c, f)
			sendWS(t, c, map[string]any{"type": "input_audio_buffer.commit"})
			select {
			case <-first.finished:
			case <-time.After(time.Second):
				t.Fatal("finish not sent")
			}
			sendWS(t, c, map[string]any{"type": "session.update", "session": map[string]any{"audio": map[string]any{"input": map[string]any{"transcription": map[string]any{"model": model, "language": "zh"}, "turn_detection": nil}}}})
			readWS(t, c, "session.updated")
			second := startAudio(t, c, f)
			sendWS(t, c, map[string]any{"type": "input_audio_buffer.commit"})
			select {
			case <-second.finished:
			case <-time.After(time.Second):
				t.Fatal("second finish not sent")
			}
			if model == "fun-asr-realtime" {
				second.events <- sentence("第二轮", 1)
			} else {
				second.events <- qwenEvent(realtime.ItemCommitted, "second", "", "", 0)
				second.events <- qwenEvent(realtime.TextDelta, "second", "第", "第", 0)
				second.events <- qwenEvent(realtime.TextDelta, "second", "第二", "二", 0)
				second.events <- qwenEvent(realtime.ItemCompleted, "second", "第二轮", "轮", 0)
			}
			second.events <- realtime.Event{Finished: true}
			// Control responses are not held, and no second commit can overtake.
			sendWS(t, c, map[string]any{"type": "session.update", "session": map[string]any{"type": "transcription"}})
			readWS(t, c, "session.updated")
			first.events <- qwenEvent(realtime.ItemCommitted, "first", "", "", 0)
			first.events <- qwenEvent(realtime.ItemCompleted, "first", "第一轮", "第一轮", 0)
			one := readWS(t, c, "input_audio_buffer.committed")["item_id"]
			readWS(t, c, "conversation.item.added")
			readWS(t, c, "conversation.item.input_audio_transcription.delta")
			readWS(t, c, "conversation.item.input_audio_transcription.completed")
			first.events <- realtime.Event{Finished: true}
			if e := readWS(t, c, "input_audio_buffer.committed"); e["previous_item_id"] != one {
				t.Fatal(e)
			}
			readWS(t, c, "conversation.item.added")
			var text string
			for {
				var e map[string]any
				c.SetReadDeadline(time.Now().Add(time.Second))
				if err := c.ReadJSON(&e); err != nil {
					t.Fatal(err)
				}
				if e["type"] == "conversation.item.input_audio_transcription.completed" {
					if e["transcript"] != "第二轮" || text != "第二轮" {
						t.Fatal(e, text)
					}
					break
				}
				if e["type"] != "conversation.item.input_audio_transcription.delta" {
					t.Fatal(e)
				}
				text += e["delta"].(string)
			}
		})
	}
}
