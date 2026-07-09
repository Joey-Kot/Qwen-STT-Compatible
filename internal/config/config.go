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

import (
	"flag"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

const defaultMaxUploadMB = 500

type RetryConfig struct {
	MaxAttempts  int
	InitialDelay time.Duration
	Factor       float64
	MaxDelay     time.Duration
}

type Config struct {
	Listen            string
	APITokens         []string
	DashScopeAPIKey   string
	DashScopeBaseURL  string
	APIConcurrency    int
	APISegmentLength  time.Duration
	FixedSliceLength  time.Duration
	FixedSliceWorkers int
	SilentInterval    time.Duration
	Padding           time.Duration
	OutputBitrate     string
	EnableLID         bool
	EnableITN         bool
	UpstreamTimeout   time.Duration
	Retry             RetryConfig
	ReadHeaderTimeout time.Duration
	IdleTimeout       time.Duration
	MaxUploadBytes    int64
}

func Parse(args []string) (Config, error) {
	maxUploadMB := int64(envPositiveInt("MAX_UPLOAD_MB", defaultMaxUploadMB))
	cfg := Config{
		Listen:            envString("LISTEN", ":8080"),
		APITokens:         splitTokens(os.Getenv("API_TOKEN")),
		DashScopeAPIKey:   strings.TrimSpace(os.Getenv("DASHSCOPE_API_KEY")),
		DashScopeBaseURL:  strings.TrimRight(envString("DASHSCOPE_HTTP_BASE_URL", "https://dashscope.aliyuncs.com/api/v1"), "/"),
		APIConcurrency:    envPositiveInt("API_CONCURRENCY", 10),
		APISegmentLength:  time.Duration(envPositiveInt("API_SEGMENT_LENGTH", 175)) * time.Second,
		FixedSliceLength:  time.Duration(envPositiveInt("FFMPEG_SEGMENT_LENGTH", 5)) * time.Second,
		FixedSliceWorkers: envPositiveInt("FFMPEG_WORKS", 16),
		SilentInterval:    time.Duration(envPositiveInt("SILENT_INTERVAL", 700)) * time.Millisecond,
		Padding:           time.Duration(envPositiveInt("PADDING_LENGTH", 100)) * time.Millisecond,
		OutputBitrate:     envString("OUTPUT_BITRATE", "128k"),
		EnableLID:         envBool("ENABLE_LID", true),
		EnableITN:         envBool("ENABLE_ITN", false),
		UpstreamTimeout:   time.Duration(envPositiveInt("UPSTREAM_TIMEOUT_SECONDS", 30)) * time.Second,
		Retry: RetryConfig{
			MaxAttempts:  envPositiveInt("ASR_RETRY_MAX_ATTEMPTS", 4),
			InitialDelay: time.Duration(envPositiveFloat("ASR_RETRY_INITIAL_DELAY", 0.5) * float64(time.Second)),
			Factor:       envPositiveFloat("ASR_RETRY_FACTOR", 2.0),
			MaxDelay:     time.Duration(envPositiveFloat("ASR_RETRY_MAX_DELAY", 8.0) * float64(time.Second)),
		},
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       120 * time.Second,
		MaxUploadBytes:    maxUploadMB << 20,
	}

	apiToken := strings.Join(cfg.APITokens, ",")
	fs := flag.NewFlagSet("qwen-stt-compatible", flag.ContinueOnError)
	fs.Usage = func() {
		fmt.Fprintf(fs.Output(), "Usage of %s:\n", fs.Name())
		fs.VisitAll(func(f *flag.Flag) {
			fmt.Fprintf(fs.Output(), "  --%s\n    \t%s\n", f.Name, f.Usage)
		})
	}
	fs.StringVar(&cfg.Listen, "listen", cfg.Listen, "listen address")
	fs.StringVar(&apiToken, "api-token", apiToken, "comma-separated compatible API tokens")
	fs.StringVar(&cfg.DashScopeAPIKey, "dashscope-api-key", cfg.DashScopeAPIKey, "DashScope API key")
	fs.StringVar(&cfg.DashScopeBaseURL, "dashscope-base-url", cfg.DashScopeBaseURL, "DashScope HTTP API base URL")
	fs.IntVar(&cfg.APIConcurrency, "api-concurrency", cfg.APIConcurrency, "DashScope request concurrency")
	fs.DurationVar(&cfg.APISegmentLength, "api-segment-length", cfg.APISegmentLength, "maximum ASR segment length")
	fs.DurationVar(&cfg.FixedSliceLength, "fixed-slice-length", cfg.FixedSliceLength, "fixed trim slice length")
	fs.IntVar(&cfg.FixedSliceWorkers, "fixed-slice-workers", cfg.FixedSliceWorkers, "fixed trim workers")
	fs.DurationVar(&cfg.SilentInterval, "silent-interval", cfg.SilentInterval, "minimum silence interval")
	fs.DurationVar(&cfg.Padding, "padding", cfg.Padding, "speech interval padding")
	fs.StringVar(&cfg.OutputBitrate, "output-bitrate", cfg.OutputBitrate, "audio output bitrate")
	fs.Var(boolValue{target: &cfg.EnableLID}, "enable-lid", "default enable_lid value for transcription requests, accepts 0/1 or true/false")
	fs.Var(boolValue{target: &cfg.EnableITN}, "enable-itn", "default enable_itn value for transcription requests, accepts 0/1 or true/false")
	fs.DurationVar(&cfg.UpstreamTimeout, "upstream-timeout", cfg.UpstreamTimeout, "DashScope request timeout")
	fs.IntVar(&cfg.Retry.MaxAttempts, "asr-retry-max-attempts", cfg.Retry.MaxAttempts, "maximum DashScope ASR retry attempts")
	fs.DurationVar(&cfg.Retry.InitialDelay, "asr-retry-initial-delay", cfg.Retry.InitialDelay, "initial DashScope ASR retry delay")
	fs.Float64Var(&cfg.Retry.Factor, "asr-retry-factor", cfg.Retry.Factor, "DashScope ASR retry backoff factor")
	fs.DurationVar(&cfg.Retry.MaxDelay, "asr-retry-max-delay", cfg.Retry.MaxDelay, "maximum DashScope ASR retry delay")
	fs.Int64Var(&maxUploadMB, "max-upload-mb", maxUploadMB, "maximum upload size in MiB")
	if err := validateDoubleDashArgs(args); err != nil {
		return cfg, err
	}
	if err := fs.Parse(args); err != nil {
		return cfg, err
	}
	cfg.APITokens = splitTokens(apiToken)
	if maxUploadMB <= 0 {
		return cfg, fmt.Errorf("--max-upload-mb must be positive")
	}
	if cfg.Retry.MaxAttempts <= 0 {
		return cfg, fmt.Errorf("--asr-retry-max-attempts must be positive")
	}
	if cfg.Retry.InitialDelay <= 0 {
		return cfg, fmt.Errorf("--asr-retry-initial-delay must be positive")
	}
	if cfg.Retry.Factor <= 0 {
		return cfg, fmt.Errorf("--asr-retry-factor must be positive")
	}
	if cfg.Retry.MaxDelay <= 0 {
		return cfg, fmt.Errorf("--asr-retry-max-delay must be positive")
	}
	cfg.DashScopeBaseURL = strings.TrimRight(cfg.DashScopeBaseURL, "/")
	cfg.MaxUploadBytes = maxUploadMB << 20
	return cfg, nil
}

func validateDoubleDashArgs(args []string) error {
	for _, arg := range args {
		if arg == "--" {
			return nil
		}
		if strings.HasPrefix(arg, "--") || !strings.HasPrefix(arg, "-") {
			continue
		}
		return fmt.Errorf("command-line argument %q must use --name form", arg)
	}
	return nil
}

func envString(name, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(name)); value != "" {
		return value
	}
	return fallback
}

func envPositiveInt(name string, fallback int) int {
	value := strings.TrimSpace(os.Getenv(name))
	if value == "" {
		return fallback
	}
	parsed, err := strconv.Atoi(value)
	if err != nil || parsed <= 0 {
		return fallback
	}
	return parsed
}

func envPositiveFloat(name string, fallback float64) float64 {
	value := strings.TrimSpace(os.Getenv(name))
	if value == "" {
		return fallback
	}
	parsed, err := strconv.ParseFloat(value, 64)
	if err != nil || parsed <= 0 {
		return fallback
	}
	return parsed
}

func envBool(name string, fallback bool) bool {
	value := strings.TrimSpace(os.Getenv(name))
	if value == "" {
		return fallback
	}
	parsed, err := strconv.ParseBool(value)
	if err != nil {
		return fallback
	}
	return parsed
}

type boolValue struct {
	target *bool
}

func (v boolValue) String() string {
	if v.target == nil {
		return ""
	}
	return strconv.FormatBool(*v.target)
}

func (v boolValue) Set(value string) error {
	parsed, err := strconv.ParseBool(value)
	if err != nil {
		return fmt.Errorf("must be 0/1 or true/false")
	}
	*v.target = parsed
	return nil
}

func splitTokens(value string) []string {
	parts := strings.Split(value, ",")
	tokens := make([]string, 0, len(parts))
	for _, part := range parts {
		if token := strings.TrimSpace(part); token != "" {
			tokens = append(tokens, token)
		}
	}
	return tokens
}
