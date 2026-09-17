// Copyright (C) 2026 Joey Kot <joey.kot.x@gmail.com>
// SPDX-License-Identifier: GPL-3.0-or-later

package httpapi

import (
	"errors"
	"qwen-stt-compatible/internal/realtime"
	"strings"
)

type fileItem struct {
	text    string
	emitted int
	done    bool
}

// The upstream adapter deduplicates items. Speech/commit events establish audio
// order; results may complete out of order, but a file has one linear transcript.
type orderedFileItems struct {
	items    map[string]*fileItem
	order    []string
	buffered int
}

func (f *orderedFileItems) accept(e *realtime.ItemEvent) (string, error) {
	if e.Kind == realtime.ItemFailed {
		return "", e.Err
	}
	item := f.items[e.ID]
	if item == nil {
		if len(f.order) >= 64 {
			return "", errors.New("等待完成的转写项目超过 64 个")
		}
		item = &fileItem{}
		f.items[e.ID] = item
		f.order = append(f.order, e.ID)
	}
	if e.Kind == realtime.TextDelta || e.Kind == realtime.ItemCompleted {
		f.buffered += len(e.Text) - len(item.text)
		if f.buffered > 8<<20 {
			return "", errors.New("等待输出的转写文本超过 8 MiB 限制")
		}
		item.text = e.Text
		item.done = e.Kind == realtime.ItemCompleted
	}
	var delta strings.Builder
	for len(f.order) > 0 {
		id := f.order[0]
		front := f.items[id]
		delta.WriteString(front.text[front.emitted:])
		front.emitted = len(front.text)
		if !front.done {
			break
		}
		f.buffered -= len(front.text)
		delete(f.items, id)
		f.order = f.order[1:]
	}
	return delta.String(), nil
}
