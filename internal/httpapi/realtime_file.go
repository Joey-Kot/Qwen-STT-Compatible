// Copyright (C) 2026 Joey Kot <joey.kot.x@gmail.com>
// SPDX-License-Identifier: GPL-3.0-or-later

package httpapi

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"

	"qwen-stt-compatible/internal/models"
	"qwen-stt-compatible/internal/realtime"
	"qwen-stt-compatible/internal/sse"
)

var errResponseWritten = errors.New("response already written")

func (s *Server) realtimeSlot() (func(), error) {
	select {
	case s.realtimeSem <- struct{}{}:
		return func() { <-s.realtimeSem }, nil
	default:
		return nil, errors.New("实时识别并发已达上限")
	}
}

func (s *Server) realtimeWriteTimeout() time.Duration {
	if s.cfg.Realtime.WriteTimeout > 0 {
		return s.cfg.Realtime.WriteTimeout
	}
	return 10 * time.Second
}

func (s *Server) transcribeRealtimeFile(w http.ResponseWriter, r *http.Request, path, model, language, prompt string, rate int, stream bool) error {
	options := realtime.Options{Model: model, SampleRate: rate, Language: language, Prompt: prompt}
	route, _ := models.Match(model)
	if route.Protocol == models.QwenRealtime {
		options.VAD = true
	}
	if err := options.Validate(); err != nil {
		return clientError(err.Error())
	}
	if format := r.FormValue("response_format"); format != "" && format != "json" {
		return clientError("实时文件识别仅支持 response_format=json")
	}
	release, err := s.realtimeSlot()
	if err != nil {
		openAIError(w, http.StatusTooManyRequests, err.Error(), "rate_limit_error")
		return errResponseWritten
	}
	defer release()
	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()
	audio, err := realtime.DecodeFile(ctx, path, rate)
	if err != nil {
		return err
	}
	defer audio.Close()
	task, err := s.realtime.Start(ctx, options)
	if err != nil {
		return err
	}
	defer task.Close()
	return s.realtimeFileResponse(w, r.WithContext(ctx), task, audio, rate, stream)
}

func (s *Server) realtimeFileResponse(w http.ResponseWriter, r *http.Request, task realtime.Session, audio io.ReadCloser, rate int, stream bool) error {
	ctx, cancel := context.WithCancel(r.Context())
	sent := make(chan error, 1)
	senderDone := make(chan struct{})
	go func() {
		defer close(senderDone)
		err := sendPCM(ctx, task, audio, rate)
		if err == nil {
			err = task.Finish()
		}
		sent <- err
	}()
	defer func() { cancel(); task.Close(); audio.Close(); <-senderDone }()
	started := false
	controller := http.NewResponseController(w)
	defer controller.SetWriteDeadline(time.Time{})
	startStream := func() {
		if !started {
			w.Header().Set("Content-Type", "text/event-stream")
			w.Header().Set("Cache-Control", "no-cache")
			w.Header().Set("X-Accel-Buffering", "no")
			w.WriteHeader(http.StatusOK)
			started = true
		}
	}
	emit := func(value any) error {
		controller.SetWriteDeadline(time.Now().Add(s.realtimeWriteTimeout()))
		defer controller.SetWriteDeadline(time.Time{})
		startStream()
		if err := sse.Data(w, value); err != nil {
			return err
		}
		return controller.Flush()
	}
	var heartbeat <-chan time.Time
	if stream {
		ticker := time.NewTicker(15 * time.Second)
		defer ticker.Stop()
		heartbeat = ticker.C
	}
	fail := func(err error) error {
		if started {
			_ = emit(map[string]any{"type": "error", "error": map[string]any{"type": "server_error", "message": err.Error()}})
			return errResponseWritten
		}
		return err
	}
	var text strings.Builder
	items := orderedFileItems{items: make(map[string]*fileItem)}
	for {
		select {
		case <-heartbeat:
			controller.SetWriteDeadline(time.Now().Add(s.realtimeWriteTimeout()))
			startStream()
			_, err := io.WriteString(w, ": keepalive\n\n")
			if err == nil {
				err = controller.Flush()
			}
			controller.SetWriteDeadline(time.Time{})
			if err != nil {
				return errResponseWritten
			}
		case <-ctx.Done():
			return fail(ctx.Err())
		case err := <-sent:
			if err != nil {
				return fail(err)
			}
			sent = nil
		case event, ok := <-task.Events():
			if !ok {
				return fail(errors.New("实时任务未正常结束"))
			}
			if event.Err != nil {
				return fail(event.Err)
			}
			var delta string
			if sentence := event.Sentence; sentence != nil && sentence.Final {
				delta = sentence.Text
			}
			if event.Item != nil {
				var err error
				delta, err = items.accept(event.Item)
				if err != nil {
					return fail(err)
				}
			}
			if delta != "" {
				if text.Len()+len(delta) > 8<<20 {
					return fail(errors.New("实时转写文本超过 8 MiB 限制"))
				}
				text.WriteString(delta)
				if stream {
					if err := emit(map[string]any{"type": "transcript.text.delta", "delta": delta}); err != nil {
						return errResponseWritten
					}
				}
			}
			if event.Finished {
				if len(items.order) != 0 {
					return fail(errors.New("实时会话结束时仍有未完成项目"))
				}
				if sent != nil {
					if err := <-sent; err != nil {
						return fail(err)
					}
				}
				if stream {
					if err := emit(map[string]any{"type": "transcript.text.done", "text": text.String()}); err != nil {
						return errResponseWritten
					}
				} else {
					controller.SetWriteDeadline(time.Now().Add(s.realtimeWriteTimeout()))
					writeJSON(w, http.StatusOK, transcriptionResult{Status: "success", Text: text.String()})
				}
				return nil
			}
		}
	}
}

func sendPCM(ctx context.Context, task realtime.Session, audio io.Reader, rate int) error {
	buffer := make([]byte, rate/10*2) // 100ms; conservative real-time pacing for files.
	start := time.Now()
	var samples int64
	for {
		n, err := io.ReadFull(audio, buffer)
		if n > 0 {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			if writeErr := task.SendAudio(buffer[:n]); writeErr != nil {
				return writeErr
			}
			samples += int64(n / 2)
			delay := time.Until(start.Add(time.Duration(samples) * time.Second / time.Duration(rate)))
			if delay > 0 {
				timer := time.NewTimer(delay)
				select {
				case <-ctx.Done():
					timer.Stop()
					return ctx.Err()
				case <-timer.C:
				}
			}
		}
		if err == io.EOF || err == io.ErrUnexpectedEOF {
			if samples == 0 {
				return errors.New("音频为空")
			}
			return nil
		}
		if err != nil {
			return err
		}
	}
}
