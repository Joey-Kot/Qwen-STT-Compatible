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

package dashscope

import (
	"encoding/json"
	"testing"
)

func TestModelFamilyPrefersFunASRFlashBeforeFunASR(t *testing.T) {
	tests := []struct {
		model string
		want  string
	}{
		{model: "fun-asr-flash-2026-06-15", want: "fun-asr-flash"},
		{model: "fun-asr", want: "fun-asr"},
		{model: "paraformer-v1", want: "paraformer"},
		{model: "qwen3-asr-flash-2025-09-08", want: "qwen3-asr-flash"},
	}
	for _, tt := range tests {
		if got := modelFamily(tt.model); got != tt.want {
			t.Fatalf("modelFamily(%q)=%q want %q", tt.model, got, tt.want)
		}
	}
}

func TestExtractTranscriptionTextPrefersTranscriptText(t *testing.T) {
	payload := map[string]any{
		"transcripts": []any{
			map[string]any{
				"text": "第一段",
				"sentences": []any{
					map[string]any{"text": "不应重复"},
				},
			},
			map[string]any{"text": "第二段"},
		},
	}
	if got := extractTranscriptionText(payload); got != "第一段第二段" {
		t.Fatalf("extractTranscriptionText()=%q", got)
	}
}

func TestExtractTranscriptionTextFallsBackToSentences(t *testing.T) {
	payload := map[string]any{
		"transcripts": []any{
			map[string]any{
				"sentences": []any{
					map[string]any{"text": "第一句"},
					map[string]any{"text": "第二句"},
				},
			},
		},
	}
	if got := extractTranscriptionText(payload); got != "第一句第二句" {
		t.Fatalf("extractTranscriptionText()=%q", got)
	}
}

func TestAsyncTaskResponseFallsBackToTopLevelTaskID(t *testing.T) {
	var response asyncTaskResponse
	if err := json.Unmarshal([]byte(`{"request_id":"req","task_id":"task-top"}`), &response); err != nil {
		t.Fatal(err)
	}
	if got := response.taskID(); got != "task-top" {
		t.Fatalf("taskID()=%q want %q", got, "task-top")
	}
}

func TestAsyncTaskResponsePrefersOutputTaskID(t *testing.T) {
	var response asyncTaskResponse
	if err := json.Unmarshal([]byte(`{"task_id":"task-top","output":{"task_id":"task-output"}}`), &response); err != nil {
		t.Fatal(err)
	}
	if got := response.taskID(); got != "task-output" {
		t.Fatalf("taskID()=%q want %q", got, "task-output")
	}
}
