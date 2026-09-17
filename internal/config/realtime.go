// Copyright (C) 2026 Joey Kot <joey.kot.x@gmail.com>
// SPDX-License-Identifier: GPL-3.0-or-later

package config

import (
	"flag"
	"fmt"
	"net/url"
	"time"
)

func registerRealtimeFlags(fs *flag.FlagSet, cfg *Config) {
	rt := &cfg.Realtime
	fs.StringVar(&rt.URL, "dashscope-ws-url", envString("DASHSCOPE_WS_URL", "wss://dashscope.aliyuncs.com/api-ws/v1/inference"), "DashScope realtime WebSocket URL")
	fs.StringVar(&rt.QwenURL, "dashscope-qwen-ws-url", envString("DASHSCOPE_QWEN_WS_URL", "wss://dashscope.aliyuncs.com/api-ws/v1/realtime"), "Qwen-ASR realtime WebSocket URL (model is added automatically)")
	fs.StringVar(&rt.Workspace, "dashscope-workspace", envString("DASHSCOPE_WORKSPACE", ""), "DashScope realtime workspace ID")
	fs.DurationVar(&rt.ConnectTimeout, "realtime-connect-timeout", time.Duration(envPositiveInt("REALTIME_CONNECT_TIMEOUT_SECONDS", 10))*time.Second, "realtime handshake timeout")
	fs.DurationVar(&rt.StartTimeout, "realtime-start-timeout", time.Duration(envPositiveInt("REALTIME_START_TIMEOUT_SECONDS", 10))*time.Second, "realtime task/session initialization timeout")
	fs.DurationVar(&rt.FinishTimeout, "realtime-finish-timeout", time.Duration(envPositiveInt("REALTIME_FINISH_TIMEOUT_SECONDS", 30))*time.Second, "realtime task/session finish timeout")
	fs.DurationVar(&rt.WriteTimeout, "realtime-write-timeout", time.Duration(envPositiveInt("REALTIME_WRITE_TIMEOUT_SECONDS", 10))*time.Second, "realtime upstream/downstream write timeout")
	fs.IntVar(&cfg.RealtimeConcurrency, "realtime-concurrency", envPositiveInt("REALTIME_CONCURRENCY", 10), "maximum concurrent realtime requests/sessions")
	fs.DurationVar(&cfg.RealtimeIdleTimeout, "realtime-idle-timeout", time.Duration(envPositiveInt("REALTIME_IDLE_TIMEOUT_SECONDS", 120))*time.Second, "downstream realtime WebSocket inactivity timeout")
}

func validateRealtimeConfig(cfg Config) error {
	for name, address := range map[string]string{"dashscope-ws-url": cfg.Realtime.URL, "dashscope-qwen-ws-url": cfg.Realtime.QwenURL} {
		u, err := url.Parse(address)
		if err != nil || u.Scheme != "wss" || u.Host == "" || u.User != nil || u.Fragment != "" {
			return fmt.Errorf("--%s must be a wss URL without credentials or fragment", name)
		}
	}
	if cfg.RealtimeConcurrency <= 0 || cfg.RealtimeIdleTimeout <= 0 || cfg.Realtime.ConnectTimeout <= 0 || cfg.Realtime.StartTimeout <= 0 || cfg.Realtime.FinishTimeout <= 0 || cfg.Realtime.WriteTimeout <= 0 {
		return fmt.Errorf("realtime concurrency and timeouts must be positive")
	}
	return nil
}
