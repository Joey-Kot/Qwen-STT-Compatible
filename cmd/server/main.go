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

package main

import (
	"errors"
	"flag"
	"log"
	"net/http"
	"os"

	"qwen-stt-compatible/internal/config"
	"qwen-stt-compatible/internal/dashscope"
	"qwen-stt-compatible/internal/httpapi"
)

func main() {
	cfg, err := config.Parse(os.Args[1:])
	if err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return
		}
		log.Fatal(err)
	}

	client := dashscope.New(dashscope.Config{
		APIKey:  cfg.DashScopeAPIKey,
		BaseURL: cfg.DashScopeBaseURL,
		Timeout: cfg.UpstreamTimeout,
		Retry:   cfg.Retry,
	})

	handler := httpapi.New(cfg, client)
	server := &http.Server{
		Addr:              cfg.Listen,
		Handler:           handler,
		ReadHeaderTimeout: cfg.ReadHeaderTimeout,
		IdleTimeout:       cfg.IdleTimeout,
	}
	log.Printf("listening on %s", cfg.Listen)
	if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Fatal(err)
	}
}
