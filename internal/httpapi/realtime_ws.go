// Copyright (C) 2026 Joey Kot <joey.kot.x@gmail.com>
// SPDX-License-Identifier: GPL-3.0-or-later

package httpapi

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/gorilla/websocket"
	"qwen-stt-compatible/internal/models"
	"qwen-stt-compatible/internal/realtime"
)

const (
	maxRealtimeMessage = 1 << 20
	maxPendingTurns    = 4
	maxTurnText        = 1 << 20
)

type clientEvent struct {
	Type    string          `json:"type"`
	EventID string          `json:"event_id,omitempty"`
	Audio   string          `json:"audio,omitempty"`
	Session json.RawMessage `json:"session,omitempty"`
}

type taskEvent struct {
	key   string
	event realtime.Event
}

type realtimeTurn struct {
	task            realtime.Session
	cancel          context.CancelFunc
	settings        realtimeSettings
	item            string
	text            strings.Builder
	committed       bool
	samples         int64
	consumedSamples int64 // Confirmed VAD boundary, in downstream 24kHz samples.
	resampler       realtime.Downsample8k
	resampler16     realtime.Downsample16k
	qwen            *qwenTurn
	baseMS          int64
	sentenceID      int
	order           int64
	delayedCommit   bool
	queued          []realtime.Event
	queuedBytes     int
}

// wsBridge state belongs exclusively to the event loop. Reader goroutines only
// deliver bounded messages, so clear/commit cannot race late upstream events.
type wsBridge struct {
	server               *Server
	conn                 *websocket.Conn
	ctx                  context.Context
	settings             realtimeSettings
	id, active, previous string
	tasks                map[string]*realtimeTurn
	events               chan taskEvent
	totalSamples         int64
	turnOrder            int64
}

func (s *Server) handleRealtime(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		methodNotAllowed(w)
		return
	}
	if intent := r.URL.Query().Get("intent"); intent != "" && intent != "transcription" {
		openAIError(w, 400, "仅支持 intent=transcription", "invalid_request_error")
		return
	}
	settings := realtimeSettings{Model: r.URL.Query().Get("model"), SilenceMS: 1300}
	if settings.Model != "" {
		if err := settings.options().Validate(); err != nil {
			openAIError(w, 400, err.Error(), "invalid_request_error")
			return
		}
	}
	release, err := s.realtimeSlot()
	if err != nil {
		openAIError(w, 429, err.Error(), "rate_limit_error")
		return
	}
	defer release()
	upgrader := websocket.Upgrader{HandshakeTimeout: 10 * time.Second, Subprotocols: []string{"realtime"}}
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	defer conn.Close()
	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()
	stop := context.AfterFunc(ctx, func() { conn.Close() })
	defer stop()
	b := &wsBridge{server: s, conn: conn, ctx: ctx, settings: settings, id: "sess_" + realtime.ID(), tasks: map[string]*realtimeTurn{}, events: make(chan taskEvent, 32)}
	log.Printf("session=%s endpoint=/v1/realtime connected", b.id)
	defer log.Printf("session=%s endpoint=/v1/realtime closed", b.id)
	defer func() {
		for _, turn := range b.tasks {
			turn.cancel()
			turn.task.Close()
		}
	}()
	conn.SetReadLimit(maxRealtimeMessage)
	input := make(chan []byte, 4)
	readerDone := make(chan struct{})
	go func() {
		defer close(readerDone)
		defer cancel()
		idle := s.cfg.RealtimeIdleTimeout
		if idle <= 0 {
			idle = 120 * time.Second
		}
		for {
			conn.SetReadDeadline(time.Now().Add(idle))
			kind, data, err := conn.ReadMessage()
			if err != nil {
				return
			}
			if kind != websocket.TextMessage {
				data = []byte(`{"type":"unsupported_binary_message"}`)
			}
			select {
			case input <- data:
			case <-ctx.Done():
				return
			}
		}
	}()
	defer func() { cancel(); conn.Close(); <-readerDone }()
	if err := b.send("session.created", map[string]any{"session": b.settings.session(b.id)}); err != nil {
		return
	}
	for {
		select {
		case <-ctx.Done():
			return
		case data := <-input:
			var event clientEvent
			if err := strictJSON(data, &event); err != nil {
				if b.protocolError("", err) != nil {
					return
				}
				continue
			}
			if err := b.handle(event); err != nil {
				var fatal *fatalRealtimeError
				if errors.As(err, &fatal) {
					_ = b.send("error", map[string]any{"error": map[string]any{"type": "server_error", "code": "upstream_error", "message": err.Error(), "event_id": event.EventID}})
					return
				}
				if b.protocolError(event.EventID, err) != nil {
					return
				}
			}
			if err := b.drainOrderedEvents(); err != nil {
				_ = b.send("error", map[string]any{"error": map[string]any{"type": "server_error", "message": err.Error()}})
				return
			}
		case upstream := <-b.events:
			if err := b.upstream(upstream); err != nil {
				_ = b.send("error", map[string]any{"error": map[string]any{"type": "server_error", "code": "upstream_error", "message": err.Error()}})
				return
			}
		}
	}
}

type fatalRealtimeError struct{ error }

func (b *wsBridge) send(typ string, fields map[string]any) error {
	fields["type"] = typ
	fields["event_id"] = "event_" + realtime.ID()
	b.conn.SetWriteDeadline(time.Now().Add(b.server.realtimeWriteTimeout()))
	return b.conn.WriteJSON(fields)
}
func (b *wsBridge) protocolError(id string, err error) error {
	return b.send("error", map[string]any{"error": map[string]any{"type": "invalid_request_error", "code": "invalid_event", "message": err.Error(), "event_id": id}})
}

func (b *wsBridge) startTurn() (*realtimeTurn, error) {
	if b.settings.Model == "" {
		return nil, errors.New("请先使用 session.update 设置 transcription.model")
	}
	if len(b.tasks) >= maxPendingTurns {
		return nil, errors.New("等待完成的音频轮次已达上限，请等待转写完成后再发送")
	}
	ctx, cancel := context.WithCancel(b.ctx)
	task, err := b.server.realtime.Start(ctx, b.settings.options())
	if err != nil {
		cancel()
		return nil, &fatalRealtimeError{err}
	}
	key := realtime.ID()
	turn := &realtimeTurn{task: task, cancel: cancel, settings: b.settings, baseMS: b.totalSamples / 24}
	b.turnOrder++
	turn.order = b.turnOrder
	route, _ := models.Match(b.settings.Model)
	if route.Protocol == models.QwenRealtime {
		turn.qwen = &qwenTurn{items: make(map[string]*bridgeItem)}
	}
	b.tasks[key] = turn
	b.active = key
	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			case event, ok := <-task.Events():
				if !ok {
					event = realtime.Event{Err: errors.New("实时上游未正常结束")}
				}
				select {
				case b.events <- taskEvent{key: key, event: event}:
				case <-ctx.Done():
					return
				}
				if !ok || event.Finished || event.Err != nil {
					return
				}
			}
		}
	}()
	return turn, nil
}

func (b *wsBridge) handle(e clientEvent) error {
	switch e.Type {
	case "session.update":
		next, err := b.settings.update(e.Session)
		if err != nil {
			return err
		}
		if b.active != "" {
			previous := b.settings
			previous.Prompt = next.Prompt
			if previous != next {
				return errors.New("音频输入期间只能更新 prompt；其他配置请在 commit 或 clear 后更新")
			}
			if b.settings.Prompt != next.Prompt {
				if err := b.tasks[b.active].task.UpdatePrompt(next.Prompt); err != nil {
					return &fatalRealtimeError{err}
				}
				b.tasks[b.active].settings = next
			}
		}
		b.settings = next
		return b.send("session.updated", map[string]any{"session": next.session(b.id)})
	case "input_audio_buffer.append":
		if e.Audio == "" {
			return errors.New("audio 不能为空")
		}
		data, err := base64.StdEncoding.Strict().DecodeString(e.Audio)
		if err != nil || len(data) == 0 || len(data)%2 != 0 {
			return errors.New("audio 必须为 Base64 编码的完整 PCM16 样本")
		}
		turn := b.tasks[b.active]
		if turn == nil {
			turn, err = b.startTurn()
			if err != nil {
				return err
			}
		}
		turn.samples += int64(len(data) / 2)
		b.totalSamples += int64(len(data) / 2)
		if turn.settings.options().SampleRate == 8000 {
			data = turn.resampler.Process(data)
		} else if turn.settings.options().SampleRate == 16000 {
			data = turn.resampler16.Process(data)
		}
		if err := turn.task.SendAudio(data); err != nil {
			return &fatalRealtimeError{err}
		}
		return nil
	case "input_audio_buffer.commit":
		turn := b.tasks[b.active]
		if turn == nil || turn.samples-turn.consumedSamples < 2400 {
			return errors.New("提交的音频至少需要 100 ms")
		}
		if turn.qwen != nil {
			return b.commitQwen(turn)
		}
		{
			if turn.settings.VAD && turn.item != "" {
				if err := b.send("input_audio_buffer.speech_stopped", map[string]any{"item_id": turn.item, "audio_end_ms": b.totalSamples / 24}); err != nil {
					return &fatalRealtimeError{err}
				}
			}
			if turn.item == "" {
				turn.item = "item_" + realtime.ID()
			}
			turn.delayedCommit = b.orderBlocked(turn)
			if !turn.delayedCommit {
				if err := b.commitItem(turn.item); err != nil {
					return &fatalRealtimeError{err}
				}
				if turn.text.Len() > 0 {
					if err := b.delta(turn.item, turn.text.String()); err != nil {
						return &fatalRealtimeError{err}
					}
				}
			}
		}
		turn.committed = true
		if err := turn.task.Finish(); err != nil {
			return &fatalRealtimeError{err}
		}
		b.active = ""
		return nil
	case "input_audio_buffer.clear":
		if turn := b.tasks[b.active]; turn != nil {
			if turn.qwen != nil && turn.qwen.hasPendingCommitted() {
				turn.qwen.cleared = true
				turn.committed = true
				if err := turn.task.Finish(); err != nil {
					return &fatalRealtimeError{err}
				}
				b.active = ""
				return b.send("input_audio_buffer.cleared", map[string]any{})
			}
			turn.cancel()
			turn.task.Close()
			delete(b.tasks, b.active)
			b.active = ""
		}
		return b.send("input_audio_buffer.cleared", map[string]any{})
	default:
		return fmt.Errorf("不支持的客户端事件: %q", e.Type)
	}
}

func (b *wsBridge) commitItem(item string) error {
	var previous any
	if b.previous != "" {
		previous = b.previous
	}
	if err := b.send("input_audio_buffer.committed", map[string]any{"item_id": item, "previous_item_id": previous}); err != nil {
		return err
	}
	b.previous = item
	return b.send("conversation.item.added", map[string]any{"previous_item_id": previous, "item": map[string]any{"id": item, "object": "realtime.item", "type": "message", "status": "completed", "role": "user", "content": []any{map[string]any{"type": "input_audio", "transcript": nil}}}})
}
func (b *wsBridge) delta(item, text string) error {
	if text == "" {
		return nil
	}
	return b.send("conversation.item.input_audio_transcription.delta", map[string]any{"item_id": item, "content_index": 0, "delta": text})
}
func (b *wsBridge) completed(item, text string) error {
	return b.send("conversation.item.input_audio_transcription.completed", map[string]any{"item_id": item, "content_index": 0, "transcript": text})
}

func (b *wsBridge) processUpstream(e taskEvent) error {
	turn := b.tasks[e.key]
	if turn == nil {
		return nil
	} // Cleared task: ignore even already queued results.
	if turn.qwen != nil {
		return b.upstreamQwen(e, turn)
	}
	if e.event.Err != nil {
		if turn.item != "" {
			_ = b.send("conversation.item.input_audio_transcription.failed", map[string]any{"item_id": turn.item, "content_index": 0, "error": map[string]any{"type": "server_error", "code": "upstream_error", "message": e.event.Err.Error()}})
		}
		return e.event.Err
	}
	if sentence := e.event.Sentence; sentence != nil {
		if turn.settings.VAD && !turn.committed {
			if turn.item == "" {
				turn.item = "item_" + realtime.ID()
				turn.sentenceID = sentence.ID
				if err := b.send("input_audio_buffer.speech_started", map[string]any{"item_id": turn.item, "audio_start_ms": turn.baseMS + sentence.BeginTime}); err != nil {
					return err
				}
			}
			if sentence.ID != turn.sentenceID {
				return errors.New("上游句子在最终结果前发生切换")
			}
			if sentence.Final {
				if sentence.EndTime == nil || *sentence.EndTime < 0 {
					return errors.New("上游 VAD 最终结果缺少有效 end_time，无法确定音频提交边界")
				}
				end := *sentence.EndTime
				// The timestamp is relative to this upstream task, not the whole
				// client session. Keep audio already sent beyond this boundary:
				// a final result can arrive after the next sentence's audio.
				consumed := turn.samples
				if end <= turn.samples/24 {
					consumed = end * 24
				}
				turn.consumedSamples = max(turn.consumedSamples, consumed)
				if err := b.send("input_audio_buffer.speech_stopped", map[string]any{"item_id": turn.item, "audio_end_ms": turn.baseMS + end}); err != nil {
					return err
				}
				if err := b.commitItem(turn.item); err != nil {
					return err
				}
				if err := b.delta(turn.item, sentence.Text); err != nil {
					return err
				}
				if err := b.completed(turn.item, sentence.Text); err != nil {
					return err
				}
				turn.item = ""
			}
		} else if sentence.Final {
			if turn.text.Len()+len(sentence.Text) > maxTurnText {
				return errors.New("单轮转写文本超过 1 MiB 限制")
			}
			turn.text.WriteString(sentence.Text)
			if turn.committed {
				if err := b.delta(turn.item, sentence.Text); err != nil {
					return err
				}
			}
		}
	}
	if e.event.Finished {
		if !turn.committed {
			return errors.New("实时任务提前结束")
		}
		if turn.item != "" {
			if err := b.completed(turn.item, turn.text.String()); err != nil {
				return err
			}
		}
		turn.cancel()
		turn.task.Close()
		delete(b.tasks, e.key)
	}
	return nil
}
