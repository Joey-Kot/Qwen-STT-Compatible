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

package httpapi

import (
	"context"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	smartaudio "github.com/Joey-Kot/ASR-Audio-Preprocess"

	"qwen-stt-compatible/internal/config"
	"qwen-stt-compatible/internal/dashscope"
)

func TestSanitizeLogValueEscapesControlCharacters(t *testing.T) {
	input := "原始\n文件\r名\t\x01.wav"
	want := `原始\n文件\r名\t\x01.wav`
	if got := sanitizeLogValue(input); got != want {
		t.Fatalf("sanitizeLogValue()=%q want %q", got, want)
	}
}

func TestCleanupTempDirsRemovesOnlyRequestDirs(t *testing.T) {
	root := t.TempDir()
	for _, name := range []string{"request-a", "request-b"} {
		if err := os.MkdirAll(filepath.Join(root, name, "nested"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	keepFile := filepath.Join(root, "keep.txt")
	if err := os.WriteFile(keepFile, []byte("keep"), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := cleanupTempDirs(root); err != nil {
		t.Fatal(err)
	}

	for _, name := range []string{"request-a", "request-b"} {
		if _, err := os.Stat(filepath.Join(root, name)); !os.IsNotExist(err) {
			t.Fatalf("%s still exists or stat failed with non-not-exist error: %v", name, err)
		}
	}
	if _, err := os.Stat(keepFile); err != nil {
		t.Fatalf("keep file stat failed: %v", err)
	}
}

func TestCleanupTempDirsIgnoresMissingRoot(t *testing.T) {
	root := filepath.Join(t.TempDir(), "missing")
	if err := cleanupTempDirs(root); err != nil {
		t.Fatal(err)
	}
}

func TestCountTrimmedSliceFiles(t *testing.T) {
	root := t.TempDir()
	for _, path := range []string{
		filepath.Join(root, "run-a", "trimmed_slices", "slice-1.wav"),
		filepath.Join(root, "run-a", "trimmed_slices", "slice-2.wav"),
		filepath.Join(root, "run-b", "trimmed_slices", "slice-3.wav"),
	} {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("wav"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.MkdirAll(filepath.Join(root, "other", "trimmed_slices"), 0o755); err != nil {
		t.Fatal(err)
	}

	if got, want := countTrimmedSliceFiles(root), 3; got != want {
		t.Fatalf("countTrimmedSliceFiles()=%d want %d", got, want)
	}
}

func TestAPIConcurrencyIsGlobalAcrossRequests(t *testing.T) {
	client := &countingASRClient{delay: 10 * time.Millisecond}
	server := New(config.Config{APIConcurrency: 2}, client)
	segments := []smartaudio.Segment{
		{Index: 0, File: "0.ogg"},
		{Index: 1, File: "1.ogg"},
		{Index: 2, File: "2.ogg"},
	}

	var wg sync.WaitGroup
	errs := make(chan error, 2)
	for range 2 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := server.recognizeSegments(context.Background(), segments, "qwen3-asr-flash", dashscope.ASROptions{}, "")
			errs <- err
		}()
	}
	wg.Wait()
	close(errs)

	for err := range errs {
		if err != nil {
			t.Fatalf("recognizeSegments() error: %v", err)
		}
	}
	if got, want := client.maxConcurrent(), 2; got != want {
		t.Fatalf("max concurrent upstream calls=%d want %d", got, want)
	}
}

type countingASRClient struct {
	delay time.Duration

	mu      sync.Mutex
	current int
	max     int
}

func (c *countingASRClient) TranscribeFile(ctx context.Context, path, model string, options dashscope.ASROptions, prompt string) (string, error) {
	c.mu.Lock()
	c.current++
	if c.current > c.max {
		c.max = c.current
	}
	c.mu.Unlock()

	select {
	case <-ctx.Done():
		c.mu.Lock()
		c.current--
		c.mu.Unlock()
		return "", ctx.Err()
	case <-time.After(c.delay):
	}

	c.mu.Lock()
	c.current--
	c.mu.Unlock()
	return path, nil
}

func (c *countingASRClient) maxConcurrent() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.max
}
