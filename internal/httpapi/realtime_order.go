// Copyright (C) 2026 Joey Kot <joey.kot.x@gmail.com>
// SPDX-License-Identifier: GPL-3.0-or-later

package httpapi

import (
	"errors"

	"qwen-stt-compatible/internal/realtime"
)

// Qwen VAD flushes can still create tail items. Do not link a later turn to
// previous_item_id until those earlier item boundaries are known. Audio uploads
// continue concurrently; only downstream publication waits, with bounded memory.
func (b *wsBridge) orderBlocked(turn *realtimeTurn) bool {
	for _, earlier := range b.tasks {
		if earlier.order < turn.order && earlier.qwen != nil && earlier.settings.VAD && earlier.committed && !earlier.qwen.cleared {
			return true
		}
	}
	return false
}

func queuedEventBytes(e realtime.Event) int {
	if e.Item != nil {
		return len(e.Item.Text) + len(e.Item.Delta) + len(e.Item.ID) + len(e.Item.PreviousID)
	}
	if e.Sentence != nil {
		return len(e.Sentence.Text)
	}
	return 0
}

func (b *wsBridge) upstream(e taskEvent) error {
	turn := b.tasks[e.key]
	if turn == nil {
		return nil
	}
	if b.orderBlocked(turn) {
		// Collapse adjacent prefix updates without losing their stable deltas.
		if n := len(turn.queued); n > 0 && e.event.Item != nil && e.event.Item.Kind == realtime.TextDelta {
			last := turn.queued[n-1].Item
			if last != nil && last.Kind == realtime.TextDelta && last.ID == e.event.Item.ID {
				combined := *e.event.Item
				combined.Delta = last.Delta + combined.Delta
				e.event.Item = &combined
				turn.queuedBytes -= queuedEventBytes(turn.queued[n-1])
				turn.queued = turn.queued[:n-1]
			}
		}
		turn.queuedBytes += queuedEventBytes(e.event)
		if len(turn.queued) >= 64 || turn.queuedBytes > 8<<20 {
			return errors.New("等待前一轮 VAD 结束的事件超过缓冲限制")
		}
		turn.queued = append(turn.queued, e.event)
		return nil
	}
	if err := b.processUpstream(e); err != nil {
		return err
	}
	return b.drainOrderedEvents()
}

func (b *wsBridge) drainOrderedEvents() error {
	for {
		var next *realtimeTurn
		var key string
		for k, turn := range b.tasks {
			if (turn.delayedCommit || len(turn.queued) > 0) && !b.orderBlocked(turn) && (next == nil || turn.order < next.order) {
				next, key = turn, k
			}
		}
		if next == nil {
			return nil
		}
		if next.delayedCommit {
			if err := b.commitItem(next.item); err != nil {
				return err
			}
			if next.qwen == nil {
				if err := b.delta(next.item, next.text.String()); err != nil {
					return err
				}
			}
			next.delayedCommit = false
		}
		queued := next.queued
		next.queued = nil
		next.queuedBytes = 0
		for _, event := range queued {
			if err := b.processUpstream(taskEvent{key: key, event: event}); err != nil {
				return err
			}
		}
	}
}
