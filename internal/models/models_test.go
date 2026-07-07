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

func TestSampleRateMatchesModelPrefixWithoutAlias(t *testing.T) {
	tests := []struct {
		model string
		want  int
	}{
		{model: "qwen3-asr-flash-2025-09-08", want: 16000},
		{model: "fun-asr-flash-2026-06-15", want: 16000},
		{model: "fun-asr", want: 16000},
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

func TestSampleRateRejectsRemovedAlias(t *testing.T) {
	if _, err := SampleRate("asr"); err == nil {
		t.Fatal("SampleRate(\"asr\") succeeded, want removed alias to be rejected")
	}
}
