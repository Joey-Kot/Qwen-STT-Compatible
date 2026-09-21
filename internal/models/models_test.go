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

import "testing"

func TestParaformerRealtimeCapabilities(t *testing.T) {
	for model, rate := range map[string]int{"paraformer-realtime-v2": 24000, "paraformer-realtime-v1": 16000, "paraformer-realtime-8k-v2": 8000, "paraformer-realtime-8k-v1": 8000} {
		route, err := Match(model)
		if err != nil || route.Mode != Realtime || route.Protocol != ParaformerRealtime || route.SampleRate != rate || SupportsContext(model) {
			t.Fatal(model, route, err)
		}
		found := false
		for _, listed := range List() {
			if listed == model {
				found = true
			}
		}
		if !found {
			t.Fatal("not listed", model)
		}
		for _, language := range []string{"", "zh", "en", "ja", "yue", "ko", "de", "fr", "ru"} {
			if !SupportsLanguage(model, language) {
				t.Error(model, language)
			}
		}
		if SupportsLanguage(model, "es") {
			t.Error(model, "es")
		}
	}
	for _, model := range []string{"paraformer-v2", "paraformer-v1", "paraformer-8k-v1", "paraformer-mtl-v1"} {
		route, err := Match(model)
		if err != nil || route.Mode != Async {
			t.Fatal(model, route, err)
		}
	}
}

func TestRealtimeRoutesPrecedeOfflinePrefixes(t *testing.T) {
	for _, model := range []string{"qwen3-asr-flash-realtime", "qwen3-asr-flash-realtime-2026-02-10", "qwen-audio-3.0-asr-flash-streaming", "qwen-audio-3.1-asr-flash-streaming", "fun-asr-realtime-2026-02-28", "fun-asr-flash-8k-realtime-2026-01-28"} {
		route, err := Match(model)
		if err != nil || route.Mode != Realtime {
			t.Fatalf("%s: %+v %v", model, route, err)
		}
	}
	for _, model := range []string{"qwen-audio-3.0-asr-flash", "qwen-audio-3.0-asr-flash-filetrans", "qwen-audio-3.1-asr-flash", "qwen-audio-3.1-asr-flash-message", "qwen-audio-3.1-asr-flash-filetrans", "fun-asr-flash-2026-06-15", "fun-asr"} {
		route, err := Match(model)
		if err != nil || route.Mode == Realtime {
			t.Fatalf("offline %s: %+v %v", model, route, err)
		}
	}
}

func TestQwenAudio3RoutesMatchAny3xMinorVersion(t *testing.T) {
	tests := []struct {
		model           string
		mode            Mode
		sampleRate      int
		supportsContext bool
	}{
		{model: "qwen-audio-3.1-asr-flash-streaming", mode: Realtime, sampleRate: 24000, supportsContext: true},
		{model: "qwen-audio-3.1-asr-flash-filetrans", mode: Async, sampleRate: 16000},
		{model: "qwen-audio-3.1-asr-flash", mode: HTTP, sampleRate: 16000},
		{model: "qwen-audio-3.1-asr-flash-message", mode: HTTP, sampleRate: 16000},
		{model: "qwen-audio-3.12-asr-flash", mode: HTTP, sampleRate: 16000},
	}
	for _, tt := range tests {
		route, err := Match(tt.model)
		if err != nil || route.Mode != tt.mode || route.SampleRate != tt.sampleRate {
			t.Fatalf("Match(%q) = %+v, %v", tt.model, route, err)
		}
		if got := SupportsContext(tt.model); got != tt.supportsContext {
			t.Errorf("SupportsContext(%q) = %v, want %v", tt.model, got, tt.supportsContext)
		}
	}
	if _, err := Match("qwen-audio-4.1-asr-flash"); err == nil {
		t.Fatal("qwen-audio-4.1-asr-flash unexpectedly matched")
	}
}

func TestQwenRealtimeCapabilities(t *testing.T) {
	for _, name := range []string{"qwen3-asr-flash-realtime", "qwen3-asr-flash-realtime-2025-10-27", "qwen3-asr-flash-realtime-2026-02-10"} {
		route, err := Match(name)
		if err != nil || route.Protocol != QwenRealtime || route.SampleRate != 16000 || SupportsContext(name) {
			t.Fatalf("route=%+v err=%v", route, err)
		}
		for _, language := range []string{"", "zh", "yue", "fil", "uk", "is"} {
			if !SupportsLanguage(name, language) {
				t.Error(name, language)
			}
		}
		for _, language := range []string{"tl", "nl", "el"} {
			if SupportsLanguage(name, language) {
				t.Error(name, language)
			}
		}
		found := false
		for _, listed := range List() {
			if listed == name {
				found = true
			}
		}
		if !found {
			t.Error("missing model", name)
		}
	}
	route, _ := Match("qwen3-asr-flash-2025-09-08")
	if route.Mode != HTTP {
		t.Fatal(route)
	}
}

func TestSampleRateMatchesModelPrefixWithoutAlias(t *testing.T) {
	tests := []struct {
		model string
		want  int
	}{
		{model: "qwen-audio-3.0-asr-flash-filetrans", want: 16000},
		{model: "qwen-audio-3.0-asr-flash", want: 16000},
		{model: "qwen-audio-3.1-asr-flash-filetrans", want: 16000},
		{model: "qwen-audio-3.1-asr-flash", want: 16000},
		{model: "qwen-audio-3.1-asr-flash-message", want: 16000},
		{model: "qwen3-asr-flash-2025-09-08", want: 16000},
		{model: "fun-asr-flash-2026-06-15", want: 16000},
		{model: "fun-asr", want: 16000},
		{model: "paraformer-v2", want: 16000},
		{model: "paraformer-8k-v1", want: 8000},
		{model: "paraformer-mtl-v1", want: 16000},
	}
	for _, tt := range tests {
		got, err := SampleRate(tt.model)
		if err != nil {
			t.Fatalf("SampleRate(%q) error: %v", tt.model, err)
		}
		if got != tt.want {
			t.Fatalf("SampleRate(%q)=%d want %d", tt.model, got, tt.want)
		}
	}
}

func TestListIncludesAudio3AndFunASRFlash(t *testing.T) {
	listed := make(map[string]bool)
	for _, model := range List() {
		listed[model] = true
	}
	for _, model := range []string{
		"qwen-audio-3.0-asr-flash-filetrans",
		"qwen-audio-3.0-asr-flash",
		"qwen-audio-3.1-asr-flash-streaming",
		"qwen-audio-3.1-asr-flash-filetrans",
		"qwen-audio-3.1-asr-flash",
		"qwen-audio-3.1-asr-flash-message",
		"fun-asr-2025-11-07",
		"fun-asr-mtl",
		"fun-asr-flash-2026-06-15",
		"paraformer-v2",
	} {
		if !listed[model] {
			t.Errorf("List() does not include %q", model)
		}
	}
}

func TestSampleRateRejectsRemovedAlias(t *testing.T) {
	if _, err := SampleRate("asr"); err == nil {
		t.Fatal("SampleRate(\"asr\") succeeded, want removed alias to be rejected")
	}
}
