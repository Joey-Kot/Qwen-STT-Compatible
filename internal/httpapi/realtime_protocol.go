// Copyright (C) 2026 Joey Kot <joey.kot.x@gmail.com>
// SPDX-License-Identifier: GPL-3.0-or-later

package httpapi

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"

	"qwen-stt-compatible/internal/models"
	"qwen-stt-compatible/internal/realtime"
)

type realtimeSettings struct {
	Model, Language, Prompt string
	VAD                     bool
	SilenceMS               int
	SilenceExplicit         bool
}

func (c realtimeSettings) options() realtime.Options {
	rate, _ := models.SampleRate(c.Model)
	silence := c.SilenceMS
	if models.IsParaformerV1(c.Model) && !c.SilenceExplicit {
		silence = 0
	}
	return realtime.Options{Model: c.Model, Language: c.Language, Prompt: c.Prompt, SampleRate: rate, SilenceMS: silence, VAD: c.VAD}
}

func (c realtimeSettings) session(id string) map[string]any {
	var turn any
	if c.VAD {
		turn = map[string]any{"type": "server_vad", "silence_duration_ms": c.SilenceMS}
		if models.IsParaformerV1(c.Model) {
			turn = map[string]any{"type": "server_vad"}
		}
	}
	return map[string]any{"id": id, "object": "realtime.transcription_session", "type": "transcription", "audio": map[string]any{"input": map[string]any{
		"format":         map[string]any{"type": "audio/pcm", "rate": 24000},
		"transcription":  map[string]any{"model": c.Model, "language": c.Language, "prompt": c.Prompt},
		"turn_detection": turn, "noise_reduction": nil,
	}}, "include": []string{}}
}

type sessionPatch struct {
	Type  *string `json:"type"`
	Audio *struct {
		Input *struct {
			Format *struct {
				Type *string `json:"type"`
				Rate *int    `json:"rate"`
			} `json:"format"`
			Transcription *struct {
				Model    *string `json:"model"`
				Language *string `json:"language"`
				Prompt   *string `json:"prompt"`
			} `json:"transcription"`
			Turn  json.RawMessage `json:"turn_detection"`
			Noise json.RawMessage `json:"noise_reduction"`
		} `json:"input"`
	} `json:"audio"`
	Include []string `json:"include"`
}

func strictJSON(data []byte, target any) error {
	d := json.NewDecoder(bytes.NewReader(data))
	d.DisallowUnknownFields()
	if err := d.Decode(target); err != nil {
		return err
	}
	var extra any
	if err := d.Decode(&extra); err != io.EOF {
		return errors.New("事件必须是单个 JSON 对象")
	}
	return nil
}

func (c realtimeSettings) update(raw json.RawMessage) (realtimeSettings, error) {
	if len(raw) == 0 || bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return c, errors.New("session 必须是对象")
	}
	var patch sessionPatch
	if err := rejectUnsupportedNulls(raw); err != nil {
		return c, err
	}
	if err := strictJSON(raw, &patch); err != nil {
		return c, err
	}
	if patch.Type != nil && *patch.Type != "transcription" {
		return c, errors.New("仅支持 type=transcription 会话")
	}
	if len(patch.Include) > 0 {
		return c, errors.New("不支持 include/logprobs")
	}
	if patch.Audio != nil && patch.Audio.Input != nil {
		input := patch.Audio.Input
		if input.Format != nil {
			if input.Format.Type != nil && *input.Format.Type != "audio/pcm" {
				return c, errors.New("仅支持单声道 PCM16 little-endian")
			}
			if input.Format.Rate != nil && *input.Format.Rate != 24000 {
				return c, errors.New("OpenAI 兼容 PCM 输入采样率必须为 24000 Hz")
			}
		}
		if input.Transcription != nil {
			t := input.Transcription
			if t.Model != nil {
				c.Model = *t.Model
			}
			if t.Language != nil {
				c.Language = *t.Language
			}
			if t.Prompt != nil {
				c.Prompt = *t.Prompt
			}
		}
		if len(input.Noise) > 0 && string(input.Noise) != "null" {
			return c, errors.New("不支持 noise_reduction，必须为 null")
		}
		if len(input.Turn) > 0 {
			if string(input.Turn) == "null" {
				c.VAD = false
				c.SilenceExplicit = false
			} else {
				var vad struct {
					Type    string `json:"type"`
					Silence *int   `json:"silence_duration_ms"`
				}
				if err := strictJSON(input.Turn, &vad); err != nil {
					return c, err
				}
				if vad.Type != "server_vad" {
					return c, errors.New("仅支持 server_vad 或 null")
				}
				c.VAD = true
				if models.IsParaformerV1(c.Model) {
					c.SilenceExplicit = false
				}
				if vad.Silence != nil {
					c.SilenceExplicit = true
					if *vad.Silence < 200 || *vad.Silence > 6000 {
						return c, errors.New("silence_duration_ms 必须在 200 到 6000 之间")
					}
					c.SilenceMS = *vad.Silence
				}
			}
		}
	}
	if c.Model == "" {
		return c, errors.New("transcription.model is required")
	}
	if err := c.options().Validate(); err != nil {
		return c, fmt.Errorf("无效会话配置: %w", err)
	}
	return c, nil
}

func rejectUnsupportedNulls(raw json.RawMessage) error {
	var object map[string]json.RawMessage
	if err := json.Unmarshal(raw, &object); err != nil {
		return err
	}
	for key, value := range object {
		value = bytes.TrimSpace(value)
		if bytes.Equal(value, []byte("null")) && key != "turn_detection" && key != "noise_reduction" {
			return fmt.Errorf("%s 不支持 null", key)
		}
		if len(value) > 0 && value[0] == '{' {
			if err := rejectUnsupportedNulls(value); err != nil {
				return err
			}
		}
	}
	return nil
}
