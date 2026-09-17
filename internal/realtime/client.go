// Copyright (C) 2026 Joey Kot <joey.kot.x@gmail.com>
// SPDX-License-Identifier: GPL-3.0-or-later

// Package realtime implements the DashScope duplex ASR protocol, independently
// of the downstream HTTP/SSE or OpenAI WebSocket transport.
package realtime

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
	"qwen-stt-compatible/internal/models"
)

type Config struct {
	URL, QwenURL, APIKey, Workspace                           string
	ConnectTimeout, StartTimeout, FinishTimeout, WriteTimeout time.Duration
}

type Options struct {
	Model            string
	SampleRate       int
	Language, Prompt string
	SilenceMS        int
	VAD              bool
}

func (o Options) Validate() error {
	route, err := models.Match(o.Model)
	if err != nil {
		return err
	}
	if route.Mode != models.Realtime {
		return fmt.Errorf("模型 %q 不是实时模型", o.Model)
	}
	if o.SampleRate <= 0 || (route.SampleRate == 8000 && o.SampleRate != 8000) {
		return fmt.Errorf("模型 %q 不支持采样率 %d", o.Model, o.SampleRate)
	}
	if route.Protocol == models.QwenRealtime && o.SampleRate != 16000 && o.SampleRate != 8000 {
		return fmt.Errorf("Qwen-ASR 实时模型仅支持 16000 或 8000 Hz")
	}
	if models.IsParaformerV1(o.Model) {
		if o.SampleRate != route.SampleRate {
			return fmt.Errorf("模型 %q 仅支持 %d Hz", o.Model, route.SampleRate)
		}
		if o.SilenceMS != 0 {
			return fmt.Errorf("模型 %q 不支持自定义 silence_duration_ms", o.Model)
		}
	}
	if !models.SupportsLanguage(o.Model, o.Language) {
		return fmt.Errorf("模型 %q 不支持语言 %q", o.Model, o.Language)
	}
	if o.Prompt != "" && !models.SupportsContext(o.Model) {
		return fmt.Errorf("模型 %q 不支持 prompt 上下文", o.Model)
	}
	if len([]rune(o.Prompt)) > 400 {
		return errors.New("实时 prompt 不能超过 400 个字符")
	}
	if o.SilenceMS != 0 && (o.SilenceMS < 200 || o.SilenceMS > 6000) {
		return errors.New("silence_duration_ms 必须在 200 到 6000 之间")
	}
	return nil
}

type Sentence struct {
	ID        int    `json:"sentence_id"`
	Text      string `json:"text"`
	Begin     bool   `json:"sentence_begin"`
	Final     bool   `json:"sentence_end"`
	Heartbeat bool   `json:"heartbeat"`
	BeginTime int64  `json:"begin_time"`
	EndTime   *int64 `json:"end_time"`
	Words     []struct {
		EndTime *int64 `json:"end_time"`
	} `json:"words,omitempty"`
}

type Event struct {
	Sentence *Sentence
	Item     *ItemEvent
	Finished bool
	Err      error
}

type Session interface {
	SendAudio([]byte) error
	UpdatePrompt(string) error
	Finish() error
	Events() <-chan Event
	Close()
}

type Starter interface {
	Start(context.Context, Options) (Session, error)
}

type Client struct{ cfg Config }

func New(cfg Config) *Client {
	if cfg.URL == "" {
		cfg.URL = "wss://dashscope.aliyuncs.com/api-ws/v1/inference"
	}
	if cfg.QwenURL == "" {
		cfg.QwenURL = "wss://dashscope.aliyuncs.com/api-ws/v1/realtime"
	}
	if cfg.ConnectTimeout <= 0 {
		cfg.ConnectTimeout = 10 * time.Second
	}
	if cfg.StartTimeout <= 0 {
		cfg.StartTimeout = 10 * time.Second
	}
	if cfg.FinishTimeout <= 0 {
		cfg.FinishTimeout = 30 * time.Second
	}
	if cfg.WriteTimeout <= 0 {
		cfg.WriteTimeout = 10 * time.Second
	}
	return &Client{cfg: cfg}
}

func ID() string { return rand.Text() }

func taskID() string {
	var b [16]byte
	_, _ = rand.Read(b[:])
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}

type wireEvent struct {
	Header struct {
		Event   string `json:"event"`
		TaskID  string `json:"task_id"`
		Code    string `json:"error_code"`
		Message string `json:"error_message"`
	} `json:"header"`
	Payload struct {
		Output struct {
			Sentence *Sentence `json:"sentence"`
		} `json:"output"`
	} `json:"payload"`
}

func (c *Client) Start(ctx context.Context, options Options) (Session, error) {
	if err := options.Validate(); err != nil {
		return nil, err
	}
	if strings.TrimSpace(c.cfg.APIKey) == "" {
		return nil, errors.New("DASHSCOPE_API_KEY 未配置")
	}
	route, _ := models.Match(options.Model)
	if route.Protocol == models.QwenRealtime {
		return c.startQwen(ctx, options)
	}
	headers := http.Header{"Authorization": {"Bearer " + c.cfg.APIKey}, "User-Agent": {"qwen-stt-compatible"}}
	if c.cfg.Workspace != "" {
		headers.Set("X-DashScope-WorkSpace", c.cfg.Workspace)
	}
	dialer := websocket.Dialer{Proxy: http.ProxyFromEnvironment, HandshakeTimeout: c.cfg.ConnectTimeout}
	conn, response, err := dialer.DialContext(ctx, c.cfg.URL, headers)
	if err != nil {
		if response != nil {
			response.Body.Close()
			return nil, fmt.Errorf("实时上游握手失败: HTTP %d", response.StatusCode)
		}
		return nil, fmt.Errorf("实时上游连接失败: %w", err)
	}
	taskCtx, cancel := context.WithCancel(ctx)
	s := &session{conn: conn, ctx: taskCtx, cancel: cancel, cfg: c.cfg, id: taskID(), events: make(chan Event, 32)}
	if route.Protocol == models.ParaformerRealtime {
		s.paraformer = &paraformerSentences{}
	}
	s.stopCancel = context.AfterFunc(taskCtx, func() { conn.Close() })
	conn.SetReadLimit(1 << 20)
	parameters := map[string]any{"format": "pcm", "sample_rate": options.SampleRate, "heartbeat": true}
	if models.IsParaformerV1(options.Model) {
		delete(parameters, "heartbeat")
	}
	if options.Language != "" {
		parameters["language_hints"] = []string{options.Language}
	}
	if options.SilenceMS > 0 {
		parameters["max_sentence_silence"] = options.SilenceMS
	}
	payload := map[string]any{"task_group": "audio", "task": "asr", "function": "recognition", "model": options.Model, "parameters": parameters, "input": promptInput(options.Prompt)}
	if err = s.writeControl("run-task", payload); err != nil {
		s.Close()
		return nil, err
	}
	conn.SetReadDeadline(time.Now().Add(c.cfg.StartTimeout))
	var event wireEvent
	err = conn.ReadJSON(&event)
	if err == nil && (event.Header.TaskID != s.id || event.Header.Event != "task-started") {
		err = fmt.Errorf("实时任务启动失败: %s %s %s", event.Header.Event, event.Header.Code, event.Header.Message)
	}
	if err != nil {
		s.Close()
		return nil, err
	}
	conn.SetReadDeadline(time.Time{})
	go s.readLoop()
	return s, nil
}

type session struct {
	conn              *websocket.Conn
	ctx               context.Context
	cancel            context.CancelFunc
	stopCancel        func() bool
	cfg               Config
	id                string
	events            chan Event
	mu                sync.Mutex // Serializes writes and finish/close state.
	finishing, closed bool
	finishTimer       *time.Timer
	paraformer        *paraformerSentences // Owned by readLoop after startup.
}

func (s *session) Events() <-chan Event { return s.events }

func (s *session) writeControl(action string, payload any) error {
	return s.write(websocket.TextMessage, map[string]any{"header": map[string]any{"action": action, "task_id": s.id, "streaming": "duplex"}, "payload": payload})
}

func (s *session) write(kind int, value any) error {
	s.conn.SetWriteDeadline(time.Now().Add(s.cfg.WriteTimeout))
	if kind == websocket.BinaryMessage {
		return s.conn.WriteMessage(kind, value.([]byte))
	}
	data, err := json.Marshal(value)
	if err != nil {
		return err
	}
	return s.conn.WriteMessage(kind, data)
}

func (s *session) SendAudio(data []byte) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed || s.finishing {
		return errors.New("实时任务已结束输入")
	}
	if len(data)%2 != 0 {
		return errors.New("PCM 音频必须按 16 位样本对齐")
	}
	// Bound upstream frame size even when a downstream append contains more data.
	for len(data) > 0 {
		n := min(len(data), 3200)
		if err := s.write(websocket.BinaryMessage, data[:n]); err != nil {
			return err
		}
		data = data[n:]
	}
	return nil
}

func promptInput(prompt string) map[string]any {
	input := map[string]any{}
	if prompt != "" {
		input["context"] = []any{map[string]any{"role": "user", "content": []any{map[string]any{"type": "input_text", "text": prompt}}}}
	}
	return input
}

func (s *session) UpdatePrompt(prompt string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.paraformer != nil {
		return errors.New("Paraformer 实时模型不支持更新 prompt")
	}
	if s.closed || s.finishing {
		return errors.New("实时任务已结束输入")
	}
	input := promptInput(prompt)
	if prompt == "" {
		input["context"] = []any{}
	}
	return s.writeControl("continue-task", map[string]any{"input": input})
}

func (s *session) Finish() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed || s.finishing {
		return errors.New("实时任务已结束输入")
	}
	s.finishing = true
	// Closing the connection also interrupts a blocked ReadJSON without racing
	// SetReadDeadline against the reader goroutine.
	s.finishTimer = time.AfterFunc(s.cfg.FinishTimeout, func() { s.conn.Close() })
	return s.writeControl("finish-task", map[string]any{"input": map[string]any{}})
}

func (s *session) Close() {
	s.cancel()
	s.conn.Close() // Interrupt writes before waiting for their lock.
	s.mu.Lock()
	defer s.mu.Unlock()
	s.closed = true
	if s.finishTimer != nil {
		s.finishTimer.Stop()
	}
	if s.stopCancel != nil {
		s.stopCancel()
	}
}

func (s *session) emit(e Event) bool {
	select {
	case s.events <- e:
		return true
	case <-s.ctx.Done():
		return false
	}
}

func (s *session) readLoop() {
	defer close(s.events)
	defer s.Close()
	lastFinal := 0
	for {
		var e wireEvent
		if err := s.conn.ReadJSON(&e); err != nil {
			s.emit(Event{Err: fmt.Errorf("实时上游读取失败或结束等待超时: %w", err)})
			return
		}
		if e.Header.TaskID != s.id {
			s.emit(Event{Err: errors.New("实时上游 task_id 不匹配")})
			return
		}
		switch e.Header.Event {
		case "task-failed":
			s.emit(Event{Err: fmt.Errorf("%s: %s", e.Header.Code, e.Header.Message)})
			return
		case "task-finished":
			if s.paraformer != nil && s.paraformer.active {
				s.emit(Event{Err: errors.New("Paraformer 任务结束时仍有未确认句子")})
				return
			}
			s.mu.Lock()
			finishing := s.finishing
			s.mu.Unlock()
			if !finishing {
				s.emit(Event{Err: errors.New("实时上游提前结束任务")})
				return
			}
			s.emit(Event{Finished: true})
			return
		case "result-generated":
			sentence := e.Payload.Output.Sentence
			if s.paraformer != nil {
				var err error
				sentence, err = s.paraformer.normalize(sentence)
				if err != nil {
					s.emit(Event{Err: err})
					return
				}
			}
			if sentence == nil || sentence.Heartbeat || sentence.ID <= lastFinal {
				continue
			}
			if sentence.Final {
				lastFinal = sentence.ID
			}
			if !s.emit(Event{Sentence: sentence}) {
				return
			}
		}
	}
}
