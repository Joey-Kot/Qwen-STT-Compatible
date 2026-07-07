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
	cfg, err := Parse([]string{"-max-upload-mb", "750"})
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
	t.Setenv("BAILIAN_TOKEN", "legacy-dashscope")
	cfg, err := Parse([]string{"-api-token", "flag-a,flag-b", "-dashscope-api-key", "flag-dashscope"})
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

func TestParseIgnoresLegacyDashScopeToken(t *testing.T) {
	t.Setenv("DASHSCOPE_API_KEY", "")
	t.Setenv("BAILIAN_TOKEN", "legacy-dashscope")
	cfg, err := Parse(nil)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.DashScopeAPIKey != "" {
		t.Fatalf("DashScopeAPIKey=%q want empty", cfg.DashScopeAPIKey)
	}
}

func TestParseOutputBitrateIgnoresLegacyEnv(t *testing.T) {
	t.Setenv("OUTPUT_BITRATE", "")
	t.Setenv("OPUS_BITRATE", "64k")
	cfg, err := Parse(nil)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := cfg.OutputBitrate, "128k"; got != want {
		t.Fatalf("OutputBitrate=%q want %q", got, want)
	}
}

func TestParseEnableDefaultsFromEnvAndFlags(t *testing.T) {
	t.Setenv("ENABLE_LID", "false")
	t.Setenv("ENABLE_ITN", "true")
	cfg, err := Parse([]string{"-enable-lid", "1", "-enable-itn", "0"})
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
	cfg, err := Parse([]string{"-enable-lid", "false", "-enable-itn", "true"})
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
		"-asr-retry-max-attempts", "7",
		"-asr-retry-initial-delay", "750ms",
		"-asr-retry-factor", "1.5",
		"-asr-retry-max-delay", "12s",
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
