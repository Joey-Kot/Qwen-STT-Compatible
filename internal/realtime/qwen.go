// Copyright (C) 2026 Joey Kot <joey.kot.x@gmail.com>
// SPDX-License-Identifier: GPL-3.0-or-later

package realtime

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/gorilla/websocket"
)

type ItemKind string

const (
	SpeechStarted   ItemKind = "speech_started"
	SpeechStopped   ItemKind = "speech_stopped"
	ItemCommitted   ItemKind = "committed"
	TextDelta       ItemKind = "delta"
	ItemCompleted   ItemKind = "completed"
	ItemFailed      ItemKind = "failed"
	MaxSessionItems          = 4096
)

// ItemEvent carries stable text only. Draft (stash) text never leaves the
// adapter. TimeMS is relative to this upstream connection, before resampling.
type ItemEvent struct {
	Kind           ItemKind
	ID, PreviousID string
	Delta, Text    string
	TimeMS         int64
	Err            error
}

type qwenWireEvent struct {
	Type         string                         `json:"type"`
	ItemID       string                         `json:"item_id"`
	PreviousID   string                         `json:"previous_item_id"`
	ContentIndex int                            `json:"content_index"`
	Text         string                         `json:"text"`
	Transcript   string                         `json:"transcript"`
	StartMS      *int64                         `json:"audio_start_ms"`
	EndMS        *int64                         `json:"audio_end_ms"`
	Error        struct{ Code, Message string } `json:"error"`
}

type qwenItem struct {
	text                              string
	started, stopped, committed, done bool
}

type qwenSession struct {
	*session           // Shared serialized writes, cancellation, deadlines and event queue.
	vad                bool
	items              map[string]*qwenItem // Bounded; completed IDs remain for deduplication.
	textBytes, pending int
}

func (c *Client) startQwen(ctx context.Context, options Options) (Session, error) {
	u, err := url.Parse(c.cfg.QwenURL)
	if err != nil {
		return nil, fmt.Errorf("Qwen-ASR WebSocket 地址无效: %w", err)
	}
	query := u.Query()
	query.Set("model", options.Model)
	u.RawQuery = query.Encode()
	headers := http.Header{"Authorization": {"Bearer " + c.cfg.APIKey}, "User-Agent": {"qwen-stt-compatible"}}
	if c.cfg.Workspace != "" {
		headers.Set("X-DashScope-WorkSpace", c.cfg.Workspace)
	}
	dialer := websocket.Dialer{Proxy: http.ProxyFromEnvironment, HandshakeTimeout: c.cfg.ConnectTimeout}
	conn, response, err := dialer.DialContext(ctx, u.String(), headers)
	if err != nil {
		if response != nil {
			response.Body.Close()
			return nil, fmt.Errorf("Qwen-ASR 上游握手失败: HTTP %d", response.StatusCode)
		}
		return nil, fmt.Errorf("Qwen-ASR 上游连接失败: %w", err)
	}
	taskCtx, cancel := context.WithCancel(ctx)
	s := &qwenSession{session: &session{conn: conn, ctx: taskCtx, cancel: cancel, cfg: c.cfg, events: make(chan Event, 32)}, vad: options.VAD, items: make(map[string]*qwenItem)}
	s.stopCancel = context.AfterFunc(taskCtx, func() { conn.Close() })
	conn.SetReadLimit(1 << 20)
	conn.SetReadDeadline(time.Now().Add(c.cfg.StartTimeout))
	if err = s.expect("session.created"); err != nil {
		s.Close()
		return nil, err
	}
	var vad any
	if options.VAD {
		silence := options.SilenceMS
		if silence == 0 {
			silence = 800
		}
		vad = map[string]any{"type": "server_vad", "threshold": 0.0, "silence_duration_ms": silence}
	}
	transcription := map[string]any{}
	if options.Language != "" {
		transcription["language"] = options.Language
	}
	err = s.control("session.update", map[string]any{"session": map[string]any{"input_audio_format": "pcm", "sample_rate": options.SampleRate, "input_audio_transcription": transcription, "turn_detection": vad}})
	if err == nil {
		err = s.expect("session.updated")
	}
	if err != nil {
		s.Close()
		return nil, err
	}
	conn.SetReadDeadline(time.Time{})
	go s.readQwen()
	return s, nil
}

func (s *qwenSession) expect(kind string) error {
	var e qwenWireEvent
	if err := s.conn.ReadJSON(&e); err != nil {
		return fmt.Errorf("Qwen-ASR 初始化失败: %w", err)
	}
	if e.Type != kind {
		return fmt.Errorf("Qwen-ASR 预期 %s，收到 %s: %s %s", kind, e.Type, e.Error.Code, e.Error.Message)
	}
	return nil
}

// Caller owns the write lock (except during initialization, before publication).
func (s *qwenSession) control(kind string, fields map[string]any) error {
	if fields == nil {
		fields = make(map[string]any)
	}
	fields["type"], fields["event_id"] = kind, "event_"+ID()
	return s.write(websocket.TextMessage, fields)
}

func (s *qwenSession) SendAudio(data []byte) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed || s.finishing {
		return errors.New("实时任务已结束输入")
	}
	if len(data)%2 != 0 {
		return errors.New("PCM 音频必须按 16 位样本对齐")
	}
	for len(data) > 0 {
		n := min(len(data), 3200)
		encoded := base64.StdEncoding.EncodeToString(data[:n])
		if len(encoded) > 15<<20 {
			return errors.New("单次音频 Base64 数据超过 15 MiB 限制")
		}
		if err := s.control("input_audio_buffer.append", map[string]any{"audio": encoded}); err != nil {
			return err
		}
		data = data[n:]
	}
	return nil
}

func (s *qwenSession) UpdatePrompt(string) error {
	return errors.New("Qwen-ASR 实时模型不支持 prompt 上下文")
}

func (s *qwenSession) Finish() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed || s.finishing {
		return errors.New("实时任务已结束输入")
	}
	s.finishing = true
	s.finishTimer = time.AfterFunc(s.cfg.FinishTimeout, func() { s.conn.Close() })
	if !s.vad {
		if err := s.control("input_audio_buffer.commit", nil); err != nil {
			return err
		}
	}
	return s.control("session.finish", nil)
}

func (s *qwenSession) readQwen() {
	defer close(s.events)
	defer s.Close()
	for {
		var e qwenWireEvent
		if err := s.conn.ReadJSON(&e); err != nil {
			s.emit(Event{Err: fmt.Errorf("Qwen-ASR 上游读取失败或结束等待超时: %w", err)})
			return
		}
		if e.Type == "error" {
			s.emit(Event{Err: fmt.Errorf("%s: %s", e.Error.Code, e.Error.Message)})
			return
		}
		if e.Type == "session.finished" {
			s.mu.Lock()
			finishing := s.finishing
			s.mu.Unlock()
			if !finishing {
				s.emit(Event{Err: errors.New("Qwen-ASR 上游提前结束会话")})
				return
			}
			for _, item := range s.items {
				if !item.done {
					s.emit(Event{Err: errors.New("Qwen-ASR 会话结束时仍有未完成项目")})
					return
				}
			}
			s.emit(Event{Finished: true})
			return
		}
		item, err := s.convert(e)
		if err != nil {
			s.emit(Event{Err: err})
			return
		}
		if item != nil && !s.emit(Event{Item: item}) {
			return
		}
	}
}

func (s *qwenSession) convert(e qwenWireEvent) (*ItemEvent, error) {
	var kind ItemKind
	switch e.Type {
	case "input_audio_buffer.speech_started":
		kind = SpeechStarted
	case "input_audio_buffer.speech_stopped":
		kind = SpeechStopped
	case "input_audio_buffer.committed":
		kind = ItemCommitted
	case "conversation.item.input_audio_transcription.text":
		kind = TextDelta
	case "conversation.item.input_audio_transcription.completed":
		kind = ItemCompleted
	case "conversation.item.input_audio_transcription.failed":
		kind = ItemFailed
	default:
		return nil, nil // created/updated and item.created carry no new data.
	}
	if e.ItemID == "" || len(e.ItemID) > 256 || len(e.PreviousID) > 256 || e.ContentIndex != 0 {
		return nil, errors.New("Qwen-ASR 项目标识或 content_index 无效")
	}
	state := s.items[e.ItemID]
	if state == nil {
		if s.pending >= 64 {
			return nil, errors.New("等待完成的转写项目超过 64 个")
		}
		if len(s.items) >= MaxSessionItems {
			return nil, errors.New("单个上游会话项目数超过 4096 限制，请提交后开始新一轮")
		}
		state = &qwenItem{}
		s.items[e.ItemID] = state
		s.pending++
	}
	if state.done {
		return nil, nil
	}
	item := &ItemEvent{Kind: kind, ID: e.ItemID, PreviousID: e.PreviousID}
	switch kind {
	case SpeechStarted:
		if e.StartMS == nil || *e.StartMS < 0 {
			return nil, errors.New("Qwen-ASR 缺少有效 audio_start_ms")
		}
		if state.started {
			return nil, nil
		}
		state.started = true
		item.TimeMS = *e.StartMS
	case SpeechStopped:
		if e.EndMS == nil || *e.EndMS < 0 {
			return nil, errors.New("Qwen-ASR 缺少有效 audio_end_ms")
		}
		if state.stopped {
			return nil, nil
		}
		state.stopped = true
		item.TimeMS = *e.EndMS
	case ItemCommitted:
		if state.committed {
			return nil, nil
		}
		state.committed = true
	case TextDelta, ItemCompleted:
		text := e.Text
		if kind == ItemCompleted {
			text = e.Transcript
		}
		if len(text) > 1<<20 {
			return nil, errors.New("单项转写文本超过 1 MiB 限制")
		}
		if !strings.HasPrefix(text, state.text) {
			return nil, errors.New("Qwen-ASR 修改了已确认文本前缀")
		}
		item.Delta, item.Text = text[len(state.text):], text
		s.textBytes += len(text) - len(state.text)
		if s.textBytes > 8<<20 {
			return nil, errors.New("等待完成的转写文本超过 8 MiB 限制")
		}
		state.text = text
		if kind == ItemCompleted {
			state.done = true
			s.pending--
			s.textBytes -= len(state.text)
			state.text = ""
		}
	case ItemFailed:
		s.pending--
		s.textBytes -= len(state.text)
		state.done, state.text = true, ""
		item.Err = fmt.Errorf("%s: %s", e.Error.Code, e.Error.Message)
	}
	return item, nil
}
