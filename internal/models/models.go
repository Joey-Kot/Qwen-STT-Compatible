// Copyright (C) 2026 Joey Kot <joey.kot.x@gmail.com>
//
// This program is free software: you can redistribute it and/or modify
// it under the terms of the GNU General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.
//
// This program is distributed WITHOUT ANY WARRANTY; without even the
// implied warranty of MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE.
// See <https://www.gnu.org/licenses/> for more details.

package models

import (
	"fmt"
	"strings"
)

type Route struct {
	Prefix     string
	SampleRate int
	Mode       Mode
	Protocol   string
}

const QwenRealtime = "qwen-realtime"
const ParaformerRealtime = "paraformer-realtime"

const qwenAudio3Prefix = "qwen-audio-3."

type Mode string

const (
	HTTP     Mode = "http"
	Async    Mode = "async"
	Realtime Mode = "realtime"
)

var routes = []Route{
	{Prefix: "paraformer-realtime-8k-v2", SampleRate: 8000, Mode: Realtime, Protocol: ParaformerRealtime},
	{Prefix: "paraformer-realtime-8k-v1", SampleRate: 8000, Mode: Realtime, Protocol: ParaformerRealtime},
	{Prefix: "paraformer-realtime-v2", SampleRate: 24000, Mode: Realtime, Protocol: ParaformerRealtime},
	{Prefix: "paraformer-realtime-v1", SampleRate: 16000, Mode: Realtime, Protocol: ParaformerRealtime},
	{Prefix: "qwen3-asr-flash-realtime", SampleRate: 16000, Mode: Realtime, Protocol: QwenRealtime},
	{Prefix: "fun-asr-flash-8k-realtime", SampleRate: 8000, Mode: Realtime},
	{Prefix: "fun-asr-realtime", SampleRate: 24000, Mode: Realtime},
	{Prefix: "qwen3-asr-flash", SampleRate: 16000, Mode: HTTP},
	{Prefix: "fun-asr-flash", SampleRate: 16000, Mode: HTTP},
	{Prefix: "fun-asr", SampleRate: 16000, Mode: Async},
	{Prefix: "paraformer-8k", SampleRate: 8000, Mode: Async},
	{Prefix: "paraformer", SampleRate: 16000, Mode: Async},
}

type qwenAudio3Route struct {
	variant string
	route   Route
}

var qwenAudio3Routes = []qwenAudio3Route{
	{variant: "asr-flash-streaming", route: Route{Prefix: qwenAudio3Prefix, SampleRate: 24000, Mode: Realtime}},
	{variant: "asr-flash-filetrans", route: Route{Prefix: qwenAudio3Prefix, SampleRate: 16000, Mode: Async}},
	{variant: "asr-flash", route: Route{Prefix: qwenAudio3Prefix, SampleRate: 16000, Mode: HTTP}},
}

func IsQwenAudio3ASRFlashStreaming(model string) bool {
	return isQwenAudio3Variant(model, "asr-flash-streaming")
}

func IsQwenAudio3ASRFlashFiletrans(model string) bool {
	return isQwenAudio3Variant(model, "asr-flash-filetrans")
}

func IsQwenAudio3ASRFlash(model string) bool {
	return isQwenAudio3Variant(model, "asr-flash")
}

func isQwenAudio3Variant(model, variant string) bool {
	key := strings.ToLower(strings.TrimSpace(model))
	remainder, ok := strings.CutPrefix(key, qwenAudio3Prefix)
	if !ok {
		return false
	}
	version, name, ok := strings.Cut(remainder, "-")
	return ok && version != "" && strings.HasPrefix(name, variant)
}

func SupportsContext(model string) bool {
	if IsQwenAudio3ASRFlashStreaming(model) {
		return true
	}
	switch strings.ToLower(strings.TrimSpace(model)) {
	case "fun-asr-realtime", "fun-asr-realtime-2025-11-07":
		return true
	default:
		return false
	}
}

func IsParaformerV1(model string) bool {
	key := strings.ToLower(strings.TrimSpace(model))
	return strings.HasPrefix(key, "paraformer-realtime-v1") || strings.HasPrefix(key, "paraformer-realtime-8k-v1")
}

func SupportsLanguage(model, language string) bool {
	if language == "" {
		return true
	}
	key := strings.ToLower(strings.TrimSpace(model))
	languages := "zh en ja ko vi th id ms tl hi ar fr de es pt ru it nl sv da fi no el pl cs hu ro bg hr sk"
	switch {
	case strings.HasPrefix(key, "paraformer-realtime"):
		languages = "zh en ja yue ko de fr ru"
	case strings.HasPrefix(key, "qwen3-asr-flash-realtime"):
		languages = "zh yue en ja de ko ru fr pt ar it es hi id th tr uk vi cs da fil fi is ms no pl sv"
	case strings.HasPrefix(key, "fun-asr-flash-8k-realtime"):
		languages = "zh"
	case key == "fun-asr-realtime-2026-02-28":
		languages = "zh en ja"
	case key == "fun-asr-realtime-2025-09-15":
		languages = "zh en"
	}
	return strings.Contains(" "+languages+" ", " "+language+" ")
}

func Match(name string) (Route, error) {
	key := strings.ToLower(strings.TrimSpace(name))
	if key == "" {
		return Route{}, fmt.Errorf("model is required")
	}
	for _, route := range qwenAudio3Routes {
		if isQwenAudio3Variant(key, route.variant) {
			return route.route, nil
		}
	}
	for _, route := range routes {
		if strings.HasPrefix(key, route.Prefix) {
			return route, nil
		}
	}
	return Route{}, fmt.Errorf("不支持的模型前缀: %q", name)
}

func SampleRate(model string) (int, error) {
	route, err := Match(model)
	if err != nil {
		return 0, err
	}
	return route.SampleRate, nil
}

func List() []string {
	return []string{
		"paraformer-realtime-v2",
		"paraformer-realtime-v1",
		"paraformer-realtime-8k-v2",
		"paraformer-realtime-8k-v1",
		"qwen3-asr-flash-realtime",
		"qwen3-asr-flash-realtime-2026-02-10",
		"qwen3-asr-flash-realtime-2025-10-27",
		"qwen-audio-3.0-asr-flash-streaming",
		"qwen-audio-3.1-asr-flash-streaming",
		"fun-asr-realtime",
		"fun-asr-realtime-2025-11-07",
		"fun-asr-realtime-2026-02-28",
		"fun-asr-realtime-2025-09-15",
		"fun-asr-flash-8k-realtime",
		"fun-asr-flash-8k-realtime-2026-01-28",
		"qwen-audio-3.0-asr-flash-filetrans",
		"qwen-audio-3.1-asr-flash-filetrans",
		"qwen-audio-3.0-asr-flash",
		"qwen-audio-3.1-asr-flash",
		"qwen-audio-3.1-asr-flash-message",
		"qwen3-asr-flash",
		"qwen3-asr-flash-2025-09-08",
		"fun-asr",
		"fun-asr-2025-11-07",
		"fun-asr-2025-08-25",
		"fun-asr-mtl",
		"fun-asr-mtl-2025-08-25",
		"fun-asr-flash-2026-06-15",
		"paraformer-v2",
		"paraformer-v1",
		"paraformer-8k-v1",
		"paraformer-mtl-v1",
	}
}
