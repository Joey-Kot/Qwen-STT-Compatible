// Copyright (C) 2026 Joey Kot <joey.kot.x@gmail.com>
// SPDX-License-Identifier: GPL-3.0-or-later

package requestlog

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/url"
	"regexp"
	"strings"
)

type contextKey struct{}

type fields struct {
	request string
	segment *int
	attempt int
	secrets []string
}

func fromContext(ctx context.Context) fields {
	f, _ := ctx.Value(contextKey{}).(fields)
	return f
}

func WithRequestID(ctx context.Context, id string) context.Context {
	f := fromContext(ctx)
	f.request = id
	return context.WithValue(ctx, contextKey{}, f)
}

func RequestID(ctx context.Context) string { return fromContext(ctx).request }

func WithSegment(ctx context.Context, index int) context.Context {
	f := fromContext(ctx)
	f.segment = &index
	return context.WithValue(ctx, contextKey{}, f)
}

func WithAttempt(ctx context.Context, attempt int) context.Context {
	f := fromContext(ctx)
	f.attempt = attempt
	return context.WithValue(ctx, contextKey{}, f)
}

// WithSecrets also protects error messages that echo configured credentials.
func WithSecrets(ctx context.Context, secrets ...string) context.Context {
	f := fromContext(ctx)
	f.secrets = append(append([]string(nil), f.secrets...), secrets...)
	return context.WithValue(ctx, contextKey{}, f)
}

func Printf(ctx context.Context, format string, args ...any) {
	f := fromContext(ctx)
	var prefix strings.Builder
	if f.request != "" {
		fmt.Fprintf(&prefix, "request=%s ", f.request)
	}
	if f.segment != nil {
		fmt.Fprintf(&prefix, "segment=%d ", *f.segment)
	}
	if f.attempt > 0 {
		fmt.Fprintf(&prefix, "attempt=%d ", f.attempt)
	}
	log.Print(prefix.String() + redact(ctx, fmt.Sprintf(format, args...)))
}

var sensitiveURL = regexp.MustCompile(`(?i)(?:https?://|oss://|data:)[^\s"'<>\\]+`)

func redact(ctx context.Context, message string) string {
	for _, secret := range fromContext(ctx).secrets {
		if secret != "" {
			message = strings.ReplaceAll(message, secret, "[redacted]")
		}
	}
	return sensitiveURL.ReplaceAllStringFunc(message, func(raw string) string {
		if strings.HasPrefix(strings.ToLower(raw), "data:") {
			return "data:[redacted]"
		}
		u, err := url.Parse(raw)
		if err != nil {
			return "[redacted-url]"
		}
		u.User, u.RawQuery, u.Fragment = nil, "", ""
		u.ForceQuery = false
		return u.String()
	})
}

// Message bounds diagnostic text; callers quote it to keep each event on one line.
func Message(ctx context.Context, message string) string {
	runes := []rune(redact(ctx, message))
	if len(runes) > 2048 {
		return string(runes[:2048]) + "...(truncated)"
	}
	return string(runes)
}

func Error(ctx context.Context, err error) string {
	if err == nil {
		return ""
	}
	var diagnostic interface{ LogMessage() string }
	if errors.As(err, &diagnostic) {
		return Message(ctx, diagnostic.LogMessage())
	}
	return Message(ctx, err.Error())
}

func Outcome(err error) string {
	switch {
	case err == nil:
		return "success"
	case errors.Is(err, context.Canceled):
		return "canceled"
	case errors.Is(err, context.DeadlineExceeded):
		return "timeout"
	default:
		return "error"
	}
}
