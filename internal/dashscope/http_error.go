// Copyright (C) 2026 Joey Kot <joey.kot.x@gmail.com>
// SPDX-License-Identifier: GPL-3.0-or-later

package dashscope

import (
	"encoding/json"
	"encoding/xml"
	"fmt"
	"io"
	"net/http"
	"strings"
)

type httpResponseError struct {
	summary     string
	body        []byte
	requestID   string
	contentType string
}

func (e *httpResponseError) Error() string {
	return e.summary + ": " + strings.TrimSpace(string(e.body))
}

// LogMessage keeps diagnostic fields from an error response, without copying
// arbitrary response content into logs. Error retains the existing API detail.
func (e *httpResponseError) LogMessage() string {
	type detail struct {
		Code      string `json:"code" xml:"Code"`
		Message   string `json:"message" xml:"Message"`
		RequestID string `json:"request_id" xml:"RequestId"`
	}
	var response struct {
		detail
		Output detail `json:"output"`
	}
	if err := json.Unmarshal(e.body, &response); err != nil {
		_ = xml.Unmarshal(e.body, &response.detail)
	}
	if response.Code == "" {
		response.Code = response.Output.Code
	}
	if response.Message == "" {
		response.Message = response.Output.Message
	}
	if response.RequestID == "" {
		response.RequestID = e.requestID
	}
	return fmt.Sprintf("%s upstream_request_id=%s code=%s message=%s content_type=%s body_bytes=%d", e.summary, response.RequestID, response.Code, response.Message, e.contentType, len(e.body))
}

func responseError(resp *http.Response, summary string, limit int64) error {
	data, err := io.ReadAll(io.LimitReader(resp.Body, limit))
	if err != nil {
		return fmt.Errorf("%s: reading error response: %w", summary, err)
	}
	return &httpResponseError{summary: summary, body: data, requestID: resp.Header.Get("X-Request-Id"), contentType: resp.Header.Get("Content-Type")}
}
