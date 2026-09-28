// Copyright (C) 2026 Joey Kot <joey.kot.x@gmail.com>
// SPDX-License-Identifier: GPL-3.0-or-later

package dashscope

import (
	"context"
	"time"

	"qwen-stt-compatible/internal/requestlog"
)

func logAsyncTask(ctx context.Context, event, taskID string, response asyncTaskResponse, poll int, elapsed, nextPoll time.Duration) {
	output := response.Output
	scheduled := output.ScheduledTime
	if scheduled == "" {
		scheduled = output.ScheduleTime
	}
	requestlog.Printf(ctx, "async task %s task_id=%q upstream_request_id=%q task_status=%q poll=%d elapsed=%s next_poll=%s code=%q message=%q task_code=%q task_message=%q submit_time=%q scheduled_time=%q end_time=%q queue_duration=%s execution_duration=%s results=%d task_metrics=%v", event, taskID, response.RequestID, output.TaskStatus, poll, elapsed, nextPoll, response.Code, requestlog.Message(ctx, response.Message), output.Code, requestlog.Message(ctx, output.Message), output.SubmitTime, scheduled, output.EndTime, taskTimeDifference(output.SubmitTime, scheduled), taskTimeDifference(scheduled, output.EndTime), len(output.Results), output.TaskMetrics)
	for index, item := range output.Results {
		requestlog.Printf(ctx, "async subtask task_id=%q index=%d subtask_status=%q code=%q message=%q file_url=%q transcription_url=%q", taskID, index, item.SubtaskStatus, item.Code, requestlog.Message(ctx, item.Message), requestlog.Message(ctx, item.FileURL), requestlog.Message(ctx, item.TranscriptionURL))
	}
}

// Compare upstream timestamps only with each other, avoiding local clock skew.
// Missing or unrecognized times are left unknown rather than reported as zero.
func taskTimeDifference(from, to string) string {
	parse := func(value string) time.Time {
		for _, layout := range []string{"2006-01-02 15:04:05.999999999", time.RFC3339Nano} {
			if parsed, err := time.Parse(layout, value); err == nil {
				return parsed
			}
		}
		return time.Time{}
	}
	return elapsedBetween(parse(from), parse(to))
}
