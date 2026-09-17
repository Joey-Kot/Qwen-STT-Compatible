// Copyright (C) 2026 Joey Kot <joey.kot.x@gmail.com>
// SPDX-License-Identifier: GPL-3.0-or-later

package httpapi

import (
	"bufio"
	"bytes"
	"context"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os/exec"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"qwen-stt-compatible/internal/config"
	"qwen-stt-compatible/internal/realtime"
)

type fakeRealtime struct{ started chan *fakeSession }
type fakeSession struct {
	options  realtime.Options
	events   chan realtime.Event
	audio    chan []byte
	finished chan struct{}
	closed   chan struct{}
	once     sync.Once
}

func newFakeSession() *fakeSession {
	return &fakeSession{events: make(chan realtime.Event, 32), audio: make(chan []byte, 32), finished: make(chan struct{}, 1), closed: make(chan struct{})}
}
func (f *fakeRealtime) Start(ctx context.Context, o realtime.Options) (realtime.Session, error) {
	s := newFakeSession()
	s.options = o
	select {
	case f.started <- s:
		return s, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}
func (s *fakeSession) Events() <-chan realtime.Event { return s.events }
func (s *fakeSession) SendAudio(p []byte) error {
	select {
	case s.audio <- bytes.Clone(p):
		return nil
	case <-s.closed:
		return errors.New("closed")
	}
}
func (s *fakeSession) UpdatePrompt(string) error { return nil }
func (s *fakeSession) Finish() error             { s.finished <- struct{}{}; return nil }
func (s *fakeSession) Close()                    { s.once.Do(func() { close(s.closed) }) }

func setupRealtimeWS(t *testing.T) (*websocket.Conn, *fakeRealtime, *httptest.Server) {
	t.Helper()
	fake := &fakeRealtime{started: make(chan *fakeSession, 8)}
	s := New(config.Config{APITokens: []string{"client-key"}, RealtimeConcurrency: 2}, nil)
	s.realtime = fake
	server := httptest.NewServer(s)
	conn, _, err := websocket.DefaultDialer.Dial(strings.Replace(server.URL, "http", "ws", 1)+"/v1/realtime?intent=transcription", http.Header{"Authorization": {"Bearer client-key"}})
	if err != nil {
		server.Close()
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.Close(); server.Close() })
	readWS(t, conn, "session.created")
	sendWS(t, conn, map[string]any{"type": "session.update", "session": map[string]any{"type": "transcription", "audio": map[string]any{"input": map[string]any{"transcription": map[string]any{"model": "fun-asr-realtime"}, "turn_detection": nil}}}})
	readWS(t, conn, "session.updated")
	return conn, fake, server
}

func sendWS(t *testing.T, c *websocket.Conn, e any) {
	t.Helper()
	c.SetWriteDeadline(time.Now().Add(2 * time.Second))
	if err := c.WriteJSON(e); err != nil {
		t.Fatal(err)
	}
}
func readWS(t *testing.T, c *websocket.Conn, typ string) map[string]any {
	t.Helper()
	c.SetReadDeadline(time.Now().Add(3 * time.Second))
	var e map[string]any
	if err := c.ReadJSON(&e); err != nil {
		t.Fatal(err)
	}
	if e["type"] != typ {
		t.Fatalf("event=%v want %s", e, typ)
	}
	return e
}
func startAudio(t *testing.T, c *websocket.Conn, f *fakeRealtime) *fakeSession {
	t.Helper()
	sendWS(t, c, map[string]any{"type": "input_audio_buffer.append", "audio": base64.StdEncoding.EncodeToString(make([]byte, 4800))})
	select {
	case s := <-f.started:
		return s
	case <-time.After(2 * time.Second):
		t.Fatal("upstream not started")
		return nil
	}
}
func sentence(text string, id int) realtime.Event {
	return realtime.Event{Sentence: &realtime.Sentence{ID: id, Final: true, Text: text}}
}

func TestRealtimeManualTurnsCanCompleteOutOfOrder(t *testing.T) {
	c, f, _ := setupRealtimeWS(t)
	first := startAudio(t, c, f)
	first.events <- sentence("第一句", 1)
	sendWS(t, c, map[string]any{"type": "input_audio_buffer.commit"})
	committed1 := readWS(t, c, "input_audio_buffer.committed")
	readWS(t, c, "conversation.item.added")
	if delta := readWS(t, c, "conversation.item.input_audio_transcription.delta"); delta["delta"] != "第一句" {
		t.Fatal(delta)
	}
	second := startAudio(t, c, f)
	sendWS(t, c, map[string]any{"type": "input_audio_buffer.commit"})
	committed2 := readWS(t, c, "input_audio_buffer.committed")
	if committed2["previous_item_id"] != committed1["item_id"] {
		t.Fatal("turn ordering lost")
	}
	readWS(t, c, "conversation.item.added")
	second.events <- sentence("第二轮", 1)
	second.events <- realtime.Event{Finished: true}
	readWS(t, c, "conversation.item.input_audio_transcription.delta")
	if e := readWS(t, c, "conversation.item.input_audio_transcription.completed"); e["item_id"] != committed2["item_id"] || e["transcript"] != "第二轮" {
		t.Fatal(e)
	}
	first.events <- sentence("尾句", 2)
	first.events <- realtime.Event{Finished: true}
	readWS(t, c, "conversation.item.input_audio_transcription.delta")
	if e := readWS(t, c, "conversation.item.input_audio_transcription.completed"); e["item_id"] != committed1["item_id"] || e["transcript"] != "第一句尾句" {
		t.Fatal(e)
	}
}

func TestRealtimeClearDropsOldResults(t *testing.T) {
	c, f, _ := setupRealtimeWS(t)
	old := startAudio(t, c, f)
	old.events <- sentence("应丢弃", 1)
	sendWS(t, c, map[string]any{"type": "input_audio_buffer.clear"})
	readWS(t, c, "input_audio_buffer.cleared")
	select {
	case <-old.closed:
	default:
		t.Fatal("clear did not close upstream")
	}
	old.events <- sentence("迟到结果", 2)
	next := startAudio(t, c, f)
	sendWS(t, c, map[string]any{"type": "input_audio_buffer.commit"})
	readWS(t, c, "input_audio_buffer.committed")
	readWS(t, c, "conversation.item.added")
	next.events <- sentence("保留", 1)
	next.events <- realtime.Event{Finished: true}
	if e := readWS(t, c, "conversation.item.input_audio_transcription.delta"); e["delta"] != "保留" {
		t.Fatal(e)
	}
	if e := readWS(t, c, "conversation.item.input_audio_transcription.completed"); e["transcript"] != "保留" {
		t.Fatal(e)
	}
}

func TestRealtimeVADAndExplicitTailCommit(t *testing.T) {
	c, f, _ := setupRealtimeWS(t)
	sendWS(t, c, map[string]any{"type": "session.update", "session": map[string]any{"audio": map[string]any{"input": map[string]any{"turn_detection": map[string]any{"type": "server_vad", "silence_duration_ms": 500}}}}})
	readWS(t, c, "session.updated")
	s := startAudio(t, c, f)
	if s.options.SilenceMS != 500 {
		t.Fatal(s.options)
	}
	s.events <- realtime.Event{Sentence: &realtime.Sentence{ID: 1, Begin: true, BeginTime: 10, Text: "中间草稿"}}
	begin := readWS(t, c, "input_audio_buffer.speech_started")
	final := sentence("确认文本", 1)
	end := int64(100)
	final.Sentence.EndTime = &end
	s.events <- final
	readWS(t, c, "input_audio_buffer.speech_stopped")
	if e := readWS(t, c, "input_audio_buffer.committed"); e["item_id"] != begin["item_id"] {
		t.Fatal(e)
	}
	readWS(t, c, "conversation.item.added")
	if e := readWS(t, c, "conversation.item.input_audio_transcription.delta"); e["delta"] != "确认文本" {
		t.Fatal(e)
	}
	readWS(t, c, "conversation.item.input_audio_transcription.completed")
	// The first 100ms have been automatically committed. Empty and short
	// buffers must not finish the upstream task or create conversation items.
	sendWS(t, c, map[string]any{"type": "input_audio_buffer.commit"})
	readWS(t, c, "error")
	sendWS(t, c, map[string]any{"type": "input_audio_buffer.append", "audio": base64.StdEncoding.EncodeToString(make([]byte, 4752))}) // 99ms
	sendWS(t, c, map[string]any{"type": "input_audio_buffer.commit"})
	readWS(t, c, "error")
	select {
	case <-s.finished:
		t.Fatal("invalid commit finished upstream")
	default:
	}
	sendWS(t, c, map[string]any{"type": "input_audio_buffer.append", "audio": base64.StdEncoding.EncodeToString(make([]byte, 48))}) // Exactly 100ms pending.
	sendWS(t, c, map[string]any{"type": "input_audio_buffer.commit"})
	readWS(t, c, "input_audio_buffer.committed")
	readWS(t, c, "conversation.item.added")
	s.events <- sentence("最后一段", 2)
	s.events <- realtime.Event{Finished: true}
	readWS(t, c, "conversation.item.input_audio_transcription.delta")
	if e := readWS(t, c, "conversation.item.input_audio_transcription.completed"); e["transcript"] != "最后一段" {
		t.Fatal(e)
	}
}

func TestRealtimeErrorsAndDisconnectCancelTasks(t *testing.T) {
	c, f, _ := setupRealtimeWS(t)
	sendWS(t, c, map[string]any{"type": "response.create", "event_id": "bad-event"})
	if e := readWS(t, c, "error"); e["error"].(map[string]any)["event_id"] != "bad-event" {
		t.Fatal(e)
	}
	s := startAudio(t, c, f)
	c.Close()
	select {
	case <-s.closed:
	case <-time.After(time.Second):
		t.Fatal("disconnect leaked upstream")
	}
}

func TestRealtimeVADPreservesAudioBeyondDelayedFinal(t *testing.T) {
	for _, model := range []string{"fun-asr-realtime", "fun-asr-flash-8k-realtime"} {
		t.Run(model, func(t *testing.T) {
			c, f, _ := setupRealtimeWS(t)
			sendWS(t, c, map[string]any{"type": "session.update", "session": map[string]any{"audio": map[string]any{"input": map[string]any{
				"transcription": map[string]any{"model": model}, "turn_detection": map[string]any{"type": "server_vad"},
			}}}})
			readWS(t, c, "session.updated")
			s := startAudio(t, c, f)
			// Send 300ms in total before either final result arrives.
			sendWS(t, c, map[string]any{"type": "input_audio_buffer.append", "audio": base64.StdEncoding.EncodeToString(make([]byte, 9600))})
			<-s.audio
			<-s.audio
			for id := 1; id <= 2; id++ {
				final := sentence("自动提交", id)
				end := int64(id * 100)
				final.Sentence.EndTime = &end
				s.events <- final
				readWS(t, c, "input_audio_buffer.speech_started")
				readWS(t, c, "input_audio_buffer.speech_stopped")
				readWS(t, c, "input_audio_buffer.committed")
				readWS(t, c, "conversation.item.added")
				readWS(t, c, "conversation.item.input_audio_transcription.delta")
				readWS(t, c, "conversation.item.input_audio_transcription.completed")
			}
			// 200ms are consumed, but the final 100ms must still be committable.
			sendWS(t, c, map[string]any{"type": "input_audio_buffer.commit"})
			readWS(t, c, "input_audio_buffer.committed")
			readWS(t, c, "conversation.item.added")
			s.events <- sentence("保留尾部", 3)
			s.events <- realtime.Event{Finished: true}
			readWS(t, c, "conversation.item.input_audio_transcription.delta")
			if e := readWS(t, c, "conversation.item.input_audio_transcription.completed"); e["transcript"] != "保留尾部" {
				t.Fatal(e)
			}
		})
	}
}

func TestRealtimeUpstreamFailureIsNotCompleted(t *testing.T) {
	c, f, _ := setupRealtimeWS(t)
	s := startAudio(t, c, f)
	sendWS(t, c, map[string]any{"type": "input_audio_buffer.commit"})
	readWS(t, c, "input_audio_buffer.committed")
	readWS(t, c, "conversation.item.added")
	s.events <- realtime.Event{Err: errors.New("upstream failed")}
	readWS(t, c, "conversation.item.input_audio_transcription.failed")
	readWS(t, c, "error")
}

func TestRealtimeSessionPatchValidation(t *testing.T) {
	initial := realtimeSettings{Model: "fun-asr-realtime", SilenceMS: 1300}
	for _, raw := range []string{
		`{"audio":{"input":{"format":{"rate":16000}}}}`,
		`{"audio":{"input":{"turn_detection":{"type":"server_vad","threshold":0.5}}}}`,
		`{"audio":{"input":{"transcription":null}}}`,
		`{"audio":{"input":{"noise_reduction":{"type":"near_field"}}}}`,
		`{"audio":{"input":{"transcription":{"model":"fun-asr-realtime-2025-09-15","prompt":"test"}}}}`,
		`{"audio":{"input":{"transcription":{"model":"fun-asr-flash-8k-realtime","language":"en"}}}}`,
		`{"include":["item.input_audio_transcription.logprobs"]}`,
	} {
		if _, err := initial.update(json.RawMessage(raw)); err == nil {
			t.Errorf("accepted %s", raw)
		}
	}
	next, err := initial.update(json.RawMessage(`{"audio":{"input":{"transcription":{"language":"zh"},"turn_detection":null}}}`))
	if err != nil || next.Model != initial.Model || next.Language != "zh" || next.VAD {
		t.Fatalf("partial update: %+v %v", next, err)
	}
}

func TestRealtimeFileSSEArrivesBeforeTaskFinished(t *testing.T) {
	for _, fail := range []bool{false, true} {
		t.Run(map[bool]string{false: "success", true: "late_error"}[fail], func(t *testing.T) {
			task := newFakeSession()
			s := New(config.Config{}, nil)
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				err := s.realtimeFileResponse(w, r, task, io.NopCloser(bytes.NewReader(make([]byte, 9600))), 24000, true)
				if err != nil && !errors.Is(err, errResponseWritten) {
					s.handleProcessError(w, err)
				}
			}))
			defer server.Close()
			go func() { <-task.audio; task.events <- sentence("提前返回", 1) }()
			client := &http.Client{Timeout: 3 * time.Second}
			response, err := client.Get(server.URL)
			if err != nil {
				t.Fatal(err)
			}
			defer response.Body.Close()
			reader := bufio.NewReader(response.Body)
			line, err := reader.ReadString('\n')
			if err != nil || !strings.Contains(line, "提前返回") {
				t.Fatalf("first event=%s err=%v", line, err)
			}
			select {
			case <-task.finished:
			case <-time.After(time.Second):
				t.Fatal("finish not sent")
			}
			if fail {
				task.events <- realtime.Event{Err: errors.New("late failure")}
			} else {
				task.events <- realtime.Event{Finished: true}
			}
			rest, err := io.ReadAll(reader)
			if err != nil {
				t.Fatal(err)
			}
			if fail && (strings.Contains(string(rest), "transcript.text.done") || !strings.Contains(string(rest), "late failure")) {
				t.Fatalf("bad error stream=%s", rest)
			}
			if !fail && !strings.Contains(string(rest), "transcript.text.done") {
				t.Fatalf("missing done=%s", rest)
			}
		})
	}
}

func TestRealtimeFileNonStreamAndDecodeFailure(t *testing.T) {
	for _, decodeFailure := range []bool{false, true} {
		task := newFakeSession()
		s := New(config.Config{}, nil)
		r := httptest.NewRequest("POST", "/", nil)
		w := httptest.NewRecorder()
		var reader io.ReadCloser = io.NopCloser(bytes.NewReader(make([]byte, 4800)))
		if decodeFailure {
			reader = io.NopCloser(errorReader{})
		} else {
			go func() {
				<-task.finished
				task.events <- sentence("完整文本", 1)
				task.events <- realtime.Event{Finished: true}
			}()
		}
		err := s.realtimeFileResponse(w, r, task, reader, 24000, false)
		if decodeFailure {
			if err == nil {
				t.Fatal("decode error ignored")
			}
		} else if err != nil || !strings.Contains(w.Body.String(), "完整文本") {
			t.Fatalf("response=%s err=%v", w.Body, err)
		}
	}
}

type errorReader struct{}

func (errorReader) Read([]byte) (int, error) { return 0, errors.New("decode failure") }

func TestRealtimeMultipartBypassesOfflinePreprocessing(t *testing.T) {
	for _, model := range []string{"fun-asr-realtime", "qwen3-asr-flash-realtime", "paraformer-realtime-v2", "paraformer-realtime-v1", "paraformer-realtime-8k-v2", "paraformer-realtime-8k-v1"} {
		t.Run(model, func(t *testing.T) {
			if _, err := exec.LookPath("ffmpeg"); err != nil {
				t.Skip("ffmpeg unavailable")
			}
			var wav bytes.Buffer
			wav.WriteString("RIFF")
			binary.Write(&wav, binary.LittleEndian, uint32(36+4800))
			wav.WriteString("WAVEfmt ")
			for _, v := range []any{uint32(16), uint16(1), uint16(1), uint32(24000), uint32(48000), uint16(2), uint16(16)} {
				binary.Write(&wav, binary.LittleEndian, v)
			}
			wav.WriteString("data")
			binary.Write(&wav, binary.LittleEndian, uint32(4800))
			wav.Write(make([]byte, 4800))
			var body bytes.Buffer
			form := multipart.NewWriter(&body)
			part, _ := form.CreateFormFile("file", "test.wav")
			part.Write(wav.Bytes())
			form.WriteField("model", model)
			form.WriteField("language", "zh")
			form.WriteField("stream", "true")
			form.Close()
			r := httptest.NewRequest("POST", "/v1/audio/transcriptions", &body)
			r.Header.Set("Authorization", "Bearer client-key")
			r.Header.Set("Content-Type", form.FormDataContentType())
			fake := &fakeRealtime{started: make(chan *fakeSession, 1)}
			// This config would fail offline validation and the test binary has no libav.
			s := New(config.Config{APITokens: []string{"client-key"}, MaxUploadBytes: 1 << 20, SegmentWorkers: -1, OutputBitrate: "invalid"}, nil)
			s.realtime = fake
			go func() {
				task := <-fake.started
				if strings.HasPrefix(model, "paraformer-realtime") {
					want := 24000
					if strings.Contains(model, "8k") {
						want = 8000
					} else if model == "paraformer-realtime-v1" {
						want = 16000
					}
					if task.options.SampleRate != want || task.options.SilenceMS != 0 {
						t.Error(task.options)
					}
					if data := <-task.audio; len(data) != want/5 {
						t.Error("invalid Paraformer decode rate", len(data))
					}
				}
				if model == "qwen3-asr-flash-realtime" {
					if task.options.SampleRate != 16000 || !task.options.VAD {
						t.Error(task.options)
					}
					if data := <-task.audio; len(data) != 3200 {
						t.Error("invalid Qwen decode rate", len(data))
					}
				}
				<-task.finished
				if model == "qwen3-asr-flash-realtime" {
					task.events <- qwenEvent(realtime.ItemCompleted, "one", "实时链路", "实时链路", 0)
				} else {
					task.events <- sentence("实时链路", 1)
				}
				task.events <- realtime.Event{Finished: true}
			}()
			w := httptest.NewRecorder()
			s.ServeHTTP(w, r)
			if w.Code != 200 || strings.Count(w.Body.String(), "transcript.text.done") != 1 || strings.Contains(w.Body.String(), "[DONE]") {
				t.Fatalf("response %d: %s", w.Code, w.Body)
			}
		})
	}
}

func TestRealtimeAuthenticationAndConcurrency(t *testing.T) {
	s := New(config.Config{APITokens: []string{"key"}, RealtimeConcurrency: 1}, nil)
	release, err := s.realtimeSlot()
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	for _, token := range []string{"", "key"} {
		r := httptest.NewRequest("GET", "/v1/realtime", nil)
		if token != "" {
			r.Header.Set("Authorization", "Bearer "+token)
		}
		w := httptest.NewRecorder()
		s.ServeHTTP(w, r)
		want := 401
		if token != "" {
			want = 429
		}
		if w.Code != want {
			t.Fatalf("status=%d want %d", w.Code, want)
		}
	}
}
