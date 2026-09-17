// Copyright (C) 2026 Joey Kot <joey.kot.x@gmail.com>
// SPDX-License-Identifier: GPL-3.0-or-later

package realtime

import "errors"

// Paraformer has no sentence_id. Its sequential sentences are identified by
// begin_time. Keep only the active sentence and a finalized timestamp watermark,
// rather than retaining a growing map for the lifetime of a live connection.
type paraformerSentences struct {
	id     int
	begin  int64
	active bool
}

func (p *paraformerSentences) normalize(s *Sentence) (*Sentence, error) {
	if s == nil || s.Heartbeat {
		return nil, nil
	}
	if s.BeginTime < 0 {
		return nil, errors.New("Paraformer 句子 begin_time 无效")
	}
	if p.id > 0 && (s.BeginTime < p.begin || (!p.active && s.BeginTime == p.begin)) {
		return nil, nil // Duplicate or delayed result of an already finalized sentence.
	}
	if p.active && s.BeginTime != p.begin {
		return nil, errors.New("Paraformer 在句子确认前切换了 begin_time")
	}
	first := !p.active
	if first {
		p.id++
		p.begin = s.BeginTime
		p.active = true
	}
	s.ID, s.Begin = p.id, first
	if s.Final {
		p.active = false
		if s.EndTime == nil || *s.EndTime < s.BeginTime {
			s.EndTime = nil
			for _, word := range s.Words {
				if word.EndTime != nil && *word.EndTime >= s.BeginTime && (s.EndTime == nil || *word.EndTime > *s.EndTime) {
					s.EndTime = word.EndTime
				}
			}
		}
	}
	// Word timestamps are only used for boundary recovery, never forwarded.
	s.Words = nil
	return s, nil
}
