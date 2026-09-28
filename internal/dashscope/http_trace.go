// Copyright (C) 2026 Joey Kot <joey.kot.x@gmail.com>
// SPDX-License-Identifier: GPL-3.0-or-later

package dashscope

import (
	"context"
	"crypto/tls"
	"errors"
	"io"
	"net/http"
	"net/http/httptrace"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"qwen-stt-compatible/internal/requestlog"
)

var nextHTTPID atomic.Uint64

func (c *Client) logContext(ctx context.Context) context.Context {
	secrets := []string{c.apiKey}
	if c.webdav != nil {
		secrets = append(secrets, c.webdav.password)
	}
	return requestlog.WithSecrets(ctx, secrets...)
}

// do logs timing and identifiers, without logging request/response bodies.
// Trace callbacks may run concurrently, including after Do has returned.
func (c *Client) do(req *http.Request) (*http.Response, error) {
	started := time.Now()
	id := nextHTTPID.Add(1)
	ctx := c.logContext(req.Context())
	target := sanitizeUpstreamURL(req.URL)
	requestlog.Printf(ctx, "upstream request method=%s url=%s http_id=%d", req.Method, target, id)

	var mu sync.Mutex
	phaseStarts := make(map[string]time.Time)
	var dnsHost string
	var gotConn, wroteRequest, firstByte time.Time
	startPhase := func(phase string) {
		mu.Lock()
		phaseStarts[phase] = time.Now()
		mu.Unlock()
	}
	endPhase := func(phase string) string {
		mu.Lock()
		defer mu.Unlock()
		start := phaseStarts[phase]
		delete(phaseStarts, phase)
		return elapsedBetween(start, time.Now())
	}
	trace := &httptrace.ClientTrace{
		GetConn: func(_ string) { startPhase("connection") },
		DNSStart: func(info httptrace.DNSStartInfo) {
			mu.Lock()
			dnsHost = info.Host
			phaseStarts["dns"] = time.Now()
			mu.Unlock()
		},
		DNSDone: func(info httptrace.DNSDoneInfo) {
			duration := endPhase("dns")
			mu.Lock()
			host := dnsHost
			mu.Unlock()
			addresses := make([]string, 0, len(info.Addrs))
			for _, addr := range info.Addrs {
				addresses = append(addresses, addr.String())
			}
			requestlog.Printf(ctx, "upstream dns http_id=%d host=%q addresses=%q coalesced=%t duration=%s error=%q", id, host, strings.Join(addresses, ","), info.Coalesced, duration, requestlog.Error(ctx, info.Err))
		},
		ConnectStart: func(network, addr string) { startPhase("connect " + network + " " + addr) },
		ConnectDone: func(network, addr string, err error) {
			duration := endPhase("connect " + network + " " + addr)
			requestlog.Printf(ctx, "upstream connect http_id=%d network=%s address=%s duration=%s error=%q", id, network, addr, duration, requestlog.Error(ctx, err))
		},
		TLSHandshakeStart: func() { startPhase("tls") },
		TLSHandshakeDone: func(state tls.ConnectionState, err error) {
			duration := endPhase("tls")
			requestlog.Printf(ctx, "upstream tls http_id=%d protocol=%q resumed=%t duration=%s error=%q", id, state.NegotiatedProtocol, state.DidResume, duration, requestlog.Error(ctx, err))
		},
		GotConn: func(info httptrace.GotConnInfo) {
			wait := endPhase("connection")
			mu.Lock()
			gotConn = time.Now()
			mu.Unlock()
			remote := ""
			if info.Conn != nil && info.Conn.RemoteAddr() != nil {
				remote = info.Conn.RemoteAddr().String()
			}
			requestlog.Printf(ctx, "upstream connection method=%s url=%s http_id=%d remote=%s reused=%t idle=%t idle_duration=%s conn_wait=%s", req.Method, target, id, remote, info.Reused, info.WasIdle, info.IdleTime, wait)
		},
		WroteRequest: func(info httptrace.WroteRequestInfo) {
			mu.Lock()
			wroteRequest = time.Now()
			duration := elapsedBetween(gotConn, wroteRequest)
			mu.Unlock()
			requestlog.Printf(ctx, "upstream sent http_id=%d write_duration=%s error=%q", id, duration, requestlog.Error(ctx, info.Err))
		},
		GotFirstResponseByte: func() {
			mu.Lock()
			firstByte = time.Now()
			mu.Unlock()
		},
	}
	resp, err := c.http.Do(req.WithContext(httptrace.WithClientTrace(ctx, trace)))
	mu.Lock()
	ttfb := elapsedBetween(started, firstByte)
	responseWait := elapsedBetween(wroteRequest, firstByte)
	pending := make([]string, 0, len(phaseStarts))
	for phase := range phaseStarts {
		pending = append(pending, phase)
	}
	mu.Unlock()
	if err != nil {
		requestlog.Printf(ctx, "upstream error method=%s url=%s http_id=%d duration=%s ttfb=%s response_wait=%s pending_phases=%q error=%q", req.Method, target, id, time.Since(started), ttfb, responseWait, strings.Join(pending, ","), requestlog.Error(ctx, err))
		return resp, err
	}
	requestlog.Printf(ctx, "upstream response method=%s url=%s status=%d protocol=%s http_id=%d duration=%s ttfb=%s response_wait=%s upstream_request_id=%q", req.Method, target, resp.StatusCode, resp.Proto, id, time.Since(started), ttfb, responseWait, resp.Header.Get("X-Request-Id"))
	resp.Body = &loggedResponseBody{ReadCloser: resp.Body, ctx: ctx, id: id, started: started, headersAt: time.Now()}
	return resp, nil
}

func elapsedBetween(start, end time.Time) string {
	if start.IsZero() || end.IsZero() || end.Before(start) {
		return "-"
	}
	return end.Sub(start).String()
}

type loggedResponseBody struct {
	io.ReadCloser
	ctx                context.Context
	id                 uint64
	started, headersAt time.Time
	mu                 sync.Mutex
	bytes              int64
	once               sync.Once
}

func (b *loggedResponseBody) Read(p []byte) (int, error) {
	n, err := b.ReadCloser.Read(p)
	b.mu.Lock()
	b.bytes += int64(n)
	b.mu.Unlock()
	if errors.Is(err, io.EOF) {
		b.finish("eof", nil)
	} else if err != nil {
		b.finish("error", err)
	}
	return n, err
}

func (b *loggedResponseBody) Close() error {
	err := b.ReadCloser.Close()
	b.finish("closed", err)
	return err
}

func (b *loggedResponseBody) finish(outcome string, err error) {
	b.once.Do(func() {
		b.mu.Lock()
		bytes := b.bytes
		b.mu.Unlock()
		requestlog.Printf(b.ctx, "upstream body http_id=%d outcome=%s bytes=%d body_duration=%s total_duration=%s error=%q", b.id, outcome, bytes, time.Since(b.headersAt), time.Since(b.started), requestlog.Error(b.ctx, err))
	})
}

// Reading small remaining response bodies permits HTTP/1.1 connection reuse.
// Bound the drain so an unexpected large response is still closed promptly.
func closeResponse(resp *http.Response) {
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 64<<10))
	_ = resp.Body.Close()
}
