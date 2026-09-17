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

package config

import "testing"

func TestRealtimeConfigFlagsAndValidation(t *testing.T) {
	t.Setenv("REALTIME_CONCURRENCY", "3")
	cfg, err := Parse([]string{"--realtime-concurrency", "7", "--dashscope-ws-url", "wss://workspace.cn-beijing.maas.aliyuncs.com/api-ws/v1/inference", "--realtime-finish-timeout", "45s"})
	if err != nil || cfg.RealtimeConcurrency != 7 || cfg.Realtime.FinishTimeout.String() != "45s" {
		t.Fatalf("config=%+v err=%v", cfg, err)
	}
	for _, args := range [][]string{{"--dashscope-ws-url", "http://example.com"}, {"--realtime-concurrency", "0"}, {"--realtime-start-timeout", "0s"}} {
		if _, err := Parse(args); err == nil {
			t.Errorf("accepted %v", args)
		}
	}
}

func TestParseDefaultsMaxUploadTo500MiB(t *testing.T) {
	t.Setenv("MAX_UPLOAD_MB", "")
	cfg, err := Parse(nil)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := cfg.MaxUploadBytes, int64(500<<20); got != want {
		t.Fatalf("MaxUploadBytes=%d want %d", got, want)
	}
}

func TestParseMaxUploadFlagOverridesEnv(t *testing.T) {
	t.Setenv("MAX_UPLOAD_MB", "600")
	cfg, err := Parse([]string{"--max-upload-mb", "750"})
	if err != nil {
		t.Fatal(err)
	}
	if got, want := cfg.MaxUploadBytes, int64(750<<20); got != want {
		t.Fatalf("MaxUploadBytes=%d want %d", got, want)
	}
}

func TestParseTokenFlagsOverrideEnv(t *testing.T) {
	t.Setenv("API_TOKEN", "env-a,env-b")
	t.Setenv("DASHSCOPE_API_KEY", "env-dashscope")
	cfg, err := Parse([]string{"--api-token", "flag-a,flag-b", "--dashscope-api-key", "flag-dashscope"})
	if err != nil {
		t.Fatal(err)
	}
	if got, want := len(cfg.APITokens), 2; got != want {
		t.Fatalf("len(APITokens)=%d want %d", got, want)
	}
	if cfg.APITokens[0] != "flag-a" || cfg.APITokens[1] != "flag-b" {
		t.Fatalf("APITokens=%v", cfg.APITokens)
	}
	if got, want := cfg.DashScopeAPIKey, "flag-dashscope"; got != want {
		t.Fatalf("DashScopeAPIKey=%q want %q", got, want)
	}
}

func TestParseWebDAVFlagsOverrideEnv(t *testing.T) {
	t.Setenv("WEBDAV_URL", "https://env.example.com/dav/")
	t.Setenv("WEBDAV_CREDENTIALS", "env-user@env-password")
	cfg, err := Parse([]string{
		"--webdav-url", "https://files.example.com/asr/",
		"--webdav-credentials", "flag-user@flag-password",
	})
	if err != nil {
		t.Fatal(err)
	}
	if got, want := cfg.WebDAVURL, "https://files.example.com/asr"; got != want {
		t.Fatalf("WebDAVURL=%q want %q", got, want)
	}
	if got, want := cfg.WebDAVCredentials, "flag-user@flag-password"; got != want {
		t.Fatalf("WebDAVCredentials=%q want %q", got, want)
	}
}

func TestParseRejectsInvalidActiveWebDAVConfig(t *testing.T) {
	tests := [][]string{
		{"--webdav-url", "http://files.example.com/dav", "--webdav-credentials", "user@password"},
		{"--webdav-url", "https://files.example.com/dav", "--webdav-credentials", "not-a-pair"},
	}
	for _, args := range tests {
		if _, err := Parse(args); err == nil {
			t.Fatalf("Parse(%v) accepted invalid WebDAV configuration", args)
		}
	}
}

func TestParseOutputBitrateFlagOverridesEnv(t *testing.T) {
	t.Setenv("OUTPUT_BITRATE", "96k")
	cfg, err := Parse([]string{"--output-bitrate", "64k"})
	if err != nil {
		t.Fatal(err)
	}
	if got, want := cfg.OutputBitrate, "64k"; got != want {
		t.Fatalf("OutputBitrate=%q want %q", got, want)
	}
}

func TestParsePreprocessConcurrencyFlagsOverrideEnv(t *testing.T) {
	t.Setenv("SEGMENT_WORKERS", "0")
	t.Setenv("LIBAV_CODEC_THREADS", "0")
	cfg, err := Parse([]string{"--segment-workers", "6", "--libav-codec-threads", "2"})
	if err != nil {
		t.Fatal(err)
	}
	if got, want := cfg.SegmentWorkers, 6; got != want {
		t.Fatalf("SegmentWorkers=%d want %d", got, want)
	}
	if got, want := cfg.LibavCodecThreads, 2; got != want {
		t.Fatalf("LibavCodecThreads=%d want %d", got, want)
	}
}

func TestParsePreprocessConcurrencyAllowsZeroAuto(t *testing.T) {
	cfg, err := Parse([]string{"--segment-workers", "0", "--libav-codec-threads", "0"})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.SegmentWorkers != 0 {
		t.Fatalf("SegmentWorkers=%d want 0", cfg.SegmentWorkers)
	}
	if cfg.LibavCodecThreads != 0 {
		t.Fatalf("LibavCodecThreads=%d want 0", cfg.LibavCodecThreads)
	}
}

func TestParseSkipTrimDefaultsFromEnvAndFlag(t *testing.T) {
	t.Setenv("SKIP_TRIM", "")
	cfg, err := Parse(nil)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.SkipTrim {
		t.Fatal("SkipTrim=true want false")
	}

	t.Setenv("SKIP_TRIM", "true")
	cfg, err = Parse(nil)
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.SkipTrim {
		t.Fatal("SkipTrim=false want true")
	}

	cfg, err = Parse([]string{"--skip-trim", "false"})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.SkipTrim {
		t.Fatal("SkipTrim=true want false")
	}
}

func TestParseEnableDefaultsFromEnvAndFlags(t *testing.T) {
	t.Setenv("ENABLE_LID", "false")
	t.Setenv("ENABLE_ITN", "true")
	cfg, err := Parse([]string{"--enable-lid", "1", "--enable-itn", "0"})
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.EnableLID {
		t.Fatal("EnableLID=false want true")
	}
	if cfg.EnableITN {
		t.Fatal("EnableITN=true want false")
	}
}

func TestParseEnableDefaultsAcceptTrueFalseFlags(t *testing.T) {
	cfg, err := Parse([]string{"--enable-lid", "false", "--enable-itn", "true"})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.EnableLID {
		t.Fatal("EnableLID=true want false")
	}
	if !cfg.EnableITN {
		t.Fatal("EnableITN=false want true")
	}
}

func TestParseRetryFlagsOverrideEnv(t *testing.T) {
	t.Setenv("ASR_RETRY_MAX_ATTEMPTS", "4")
	t.Setenv("ASR_RETRY_INITIAL_DELAY", "0.5")
	t.Setenv("ASR_RETRY_FACTOR", "2")
	t.Setenv("ASR_RETRY_MAX_DELAY", "8")
	cfg, err := Parse([]string{
		"--asr-retry-max-attempts", "7",
		"--asr-retry-initial-delay", "750ms",
		"--asr-retry-factor", "1.5",
		"--asr-retry-max-delay", "12s",
	})
	if err != nil {
		t.Fatal(err)
	}
	if got, want := cfg.Retry.MaxAttempts, 7; got != want {
		t.Fatalf("Retry.MaxAttempts=%d want %d", got, want)
	}
	if got, want := cfg.Retry.InitialDelay.String(), "750ms"; got != want {
		t.Fatalf("Retry.InitialDelay=%s want %s", got, want)
	}
	if got, want := cfg.Retry.Factor, 1.5; got != want {
		t.Fatalf("Retry.Factor=%v want %v", got, want)
	}
	if got, want := cfg.Retry.MaxDelay.String(), "12s"; got != want {
		t.Fatalf("Retry.MaxDelay=%s want %s", got, want)
	}
}

func TestParseRejectsSingleDashFlag(t *testing.T) {
	if _, err := Parse([]string{"-listen", ":9090"}); err == nil {
		t.Fatal("Parse accepted single-dash flag")
	}
}

func TestOfflineValidationDoesNotBlockRealtimeStartup(t *testing.T) {
	for _, args := range [][]string{{"--segment-workers=-1"}, {"--libav-codec-threads=-1"}, {"--asr-retry-max-attempts", "0"}} {
		cfg, err := Parse(args)
		if err != nil {
			t.Fatalf("unused offline config blocked startup: %v", err)
		}
		if err := cfg.ValidateOffline(); err == nil {
			t.Errorf("offline request accepted %v", args)
		}
	}
}

func TestQwenRealtimeURLConfiguration(t *testing.T) {
	t.Setenv("DASHSCOPE_QWEN_WS_URL", "wss://workspace.ap-southeast-1.maas.aliyuncs.com/api-ws/v1/realtime")
	cfg, err := Parse(nil)
	if err != nil || cfg.Realtime.QwenURL != "wss://workspace.ap-southeast-1.maas.aliyuncs.com/api-ws/v1/realtime" {
		t.Fatal(cfg.Realtime, err)
	}
	cfg, err = Parse([]string{"--dashscope-qwen-ws-url", "wss://custom.example/realtime"})
	if err != nil || cfg.Realtime.QwenURL != "wss://custom.example/realtime" {
		t.Fatal(cfg.Realtime, err)
	}
	for _, address := range []string{"http://example.com", "ws://example.com", "wss://user:pass@example.com", "wss://example.com/#fragment"} {
		if _, err := Parse([]string{"--dashscope-qwen-ws-url", address}); err == nil {
			t.Error("accepted", address)
		}
	}
}
