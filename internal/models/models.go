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

package models

import (
	"fmt"
	"strings"
)

type Route struct {
	Prefix     string
	SampleRate int
}

var routes = []Route{
	{Prefix: "qwen3-asr-flash", SampleRate: 16000},
	{Prefix: "fun-asr-flash", SampleRate: 16000},
	{Prefix: "fun-asr", SampleRate: 16000},
	{Prefix: "paraformer-8k", SampleRate: 8000},
	{Prefix: "paraformer", SampleRate: 16000},
}

func Match(name string) (Route, error) {
	key := strings.ToLower(strings.TrimSpace(name))
	if key == "" {
		return Route{}, fmt.Errorf("model is required")
	}
	for _, route := range routes {
		if strings.HasPrefix(key, route.Prefix) {
			return route, nil
		}
	}
	return Route{}, fmt.Errorf("不支持的模型前缀: %q", name)
}

func SampleRate(model string) (int, error) {
	route, err := Match(model)
	if err != nil {
		return 0, err
	}
	return route.SampleRate, nil
}

func List() []string {
	return []string{
		"qwen3-asr-flash",
		"qwen3-asr-flash-2025-09-08",
		"fun-asr",
		"fun-asr-flash-2026-06-15",
		"paraformer-v1",
		"paraformer-8k-v1",
		"paraformer-mtl-v1",
	}
}
