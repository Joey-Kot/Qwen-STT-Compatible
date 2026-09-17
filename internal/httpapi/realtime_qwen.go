// Copyright (C) 2026 Joey Kot <joey.kot.x@gmail.com>
// SPDX-License-Identifier: GPL-3.0-or-later

package httpapi

import (
	"errors"
	"qwen-stt-compatible/internal/realtime"
)

type bridgeItem struct {
	id, text        string
	committed, done bool
}

type qwenTurn struct {
	items       map[string]*bridgeItem
	cleared     bool
	manualBound bool
}

func (q *qwenTurn) hasPendingCommitted() bool {
	for _, item := range q.items {
		if item.committed && !item.done {
			return true
		}
	}
	return false
}

func (b *wsBridge) commitQwen(turn *realtimeTurn) error {
	if !turn.settings.VAD {
		turn.item = "item_" + realtime.ID()
		turn.delayedCommit = b.orderBlocked(turn)
		if !turn.delayedCommit {
			if err := b.commitItem(turn.item); err != nil {
				return &fatalRealtimeError{err}
			}
		}
	}
	// VAD must flush via session.finish, never input_audio_buffer.commit.
	// Let upstream speech_stopped/committed identify the actual tail item.
	turn.committed = true
	if err := turn.task.Finish(); err != nil {
		return &fatalRealtimeError{err}
	}
	b.active = ""
	return nil
}

func (b *wsBridge) failQwenItem(item *bridgeItem, err error) error {
	item.done, item.text = true, ""
	return b.send("conversation.item.input_audio_transcription.failed", map[string]any{"item_id": item.id, "content_index": 0, "error": map[string]any{"type": "server_error", "code": "upstream_error", "message": err.Error()}})
}

func (b *wsBridge) upstreamQwen(e taskEvent, turn *realtimeTurn) error {
	q := turn.qwen
	if e.event.Err != nil {
		for _, item := range q.items {
			if item.committed && !item.done {
				_ = b.failQwenItem(item, e.event.Err)
			}
		}
		if !turn.settings.VAD && turn.item != "" && !q.manualBound {
			_ = b.failQwenItem(&bridgeItem{id: turn.item}, e.event.Err)
		}
		return e.event.Err
	}
	if event := e.event.Item; event != nil {
		item := q.items[event.ID]
		if q.cleared && (item == nil || !item.committed) {
			return nil
		}
		if item == nil {
			if len(q.items) >= realtime.MaxSessionItems {
				return errors.New("单个上游会话项目数超过限制")
			}
			item = &bridgeItem{id: "item_" + realtime.ID()}
			if !turn.settings.VAD {
				if !turn.committed || q.manualBound {
					return errors.New("Qwen-ASR 手动提交项目与本地轮次不匹配")
				}
				item.id, item.committed = turn.item, true
				q.manualBound = true
			}
			q.items[event.ID] = item
		}
		if item.done {
			return nil
		}
		switch event.Kind {
		case realtime.SpeechStarted:
			return b.send("input_audio_buffer.speech_started", map[string]any{"item_id": item.id, "audio_start_ms": turn.baseMS + event.TimeMS})
		case realtime.SpeechStopped:
			consumed := turn.samples
			if event.TimeMS <= turn.samples/24 {
				consumed = event.TimeMS * 24
			}
			turn.consumedSamples = max(turn.consumedSamples, consumed)
			return b.send("input_audio_buffer.speech_stopped", map[string]any{"item_id": item.id, "audio_end_ms": turn.baseMS + event.TimeMS})
		case realtime.ItemCommitted:
			if !item.committed {
				if err := b.commitItem(item.id); err != nil {
					return err
				}
				item.committed = true
				return b.delta(item.id, item.text)
			}
		case realtime.TextDelta, realtime.ItemCompleted:
			if len(event.Text) > maxTurnText {
				return errors.New("单项转写文本超过 1 MiB 限制")
			}
			item.text = event.Text
			if item.committed {
				if err := b.delta(item.id, event.Delta); err != nil {
					return err
				}
			}
			if event.Kind == realtime.ItemCompleted {
				if !item.committed {
					return errors.New("Qwen-ASR 项目在提交确认前结束")
				}
				item.done, item.text = true, ""
				return b.completed(item.id, event.Text)
			}
		case realtime.ItemFailed:
			if !item.committed {
				return errors.New("Qwen-ASR 失败项目缺少提交确认")
			}
			return b.failQwenItem(item, event.Err)
		}
	}
	if e.event.Finished {
		if !turn.committed {
			return errors.New("实时任务提前结束")
		}
		if q.hasPendingCommitted() {
			return errors.New("Qwen-ASR 会话结束时仍有未完成的已提交项目")
		}
		if !turn.settings.VAD && !q.manualBound {
			return errors.New("Qwen-ASR 手动提交未返回识别项目")
		}
		turn.cancel()
		turn.task.Close()
		delete(b.tasks, e.key)
	}
	return nil
}
