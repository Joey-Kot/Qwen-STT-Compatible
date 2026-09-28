// Copyright (C) 2026 Joey Kot <joey.kot.x@gmail.com>
// SPDX-License-Identifier: GPL-3.0-or-later

package requestlog

import (
	"bytes"
	"context"
	"errors"
	"log"
	"strings"
	"testing"
)

func TestDiagnosticsRedactErrorsAndKeepContextIsolated(t *testing.T) {
	var output bytes.Buffer
	previous := log.Writer()
	log.SetOutput(&output)
	t.Cleanup(func() { log.SetOutput(previous) })
	parent := WithRequestID(context.Background(), "request-1")
	child := WithSecrets(WithAttempt(WithSegment(parent, 2), 3), "private-api-key")
	message := "failed private-api-key https://reader:password@files.example.com/audio.ogg?Signature=private-signature#private-fragment\nnext line data:audio/ogg;base64,private-audio"
	Printf(child, "failure error=%q", Message(child, message))
	Printf(parent, "parent")
	text := output.String()
	for _, secret := range []string{"private-api-key", "password", "private-signature", "private-fragment", "private-audio"} {
		if strings.Contains(text, secret) {
			t.Errorf("secret %q leaked: %s", secret, text)
		}
	}
	if !strings.Contains(text, "request=request-1 segment=2 attempt=3 failure") || !strings.Contains(text, "request=request-1 parent") {
		t.Fatalf("context not preserved or child fields leaked to parent: %s", text)
	}
	if strings.Count(text, "\n") != 2 {
		t.Fatalf("message injected a log line: %q", text)
	}
}

func TestOutcomePreservesWrappedCancellation(t *testing.T) {
	if got := Outcome(errors.Join(errors.New("segment failed"), context.Canceled)); got != "canceled" {
		t.Fatalf("got %q", got)
	}
	if got := Outcome(context.DeadlineExceeded); got != "timeout" {
		t.Fatalf("got %q", got)
	}
}
