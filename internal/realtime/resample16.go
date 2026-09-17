// Copyright (C) 2026 Joey Kot <joey.kot.x@gmail.com>
// SPDX-License-Identifier: GPL-3.0-or-later

package realtime

import "math"

// Downsample16k implements a 2/3 rational resampler: insert zeros at 48kHz,
// low-pass filter, then decimate by three. Both phases survive append boundaries.
// A causal filter has a fixed ~1ms delay; no padding is inserted between chunks.
type Downsample16k struct {
	history         [127]float64
	position, phase int
}

var resample16Coefficients = func() [127]float64 {
	var c [127]float64
	const cutoff = 7200.0 / 48000
	var sum float64
	for i := range c {
		x := float64(i - 63)
		v := 2 * cutoff
		if x != 0 {
			v = math.Sin(2*math.Pi*cutoff*x) / (math.Pi * x)
		}
		v *= 0.54 - 0.46*math.Cos(2*math.Pi*float64(i)/126)
		c[i] = v
		sum += v
	}
	for i := range c {
		c[i] *= 2 / sum
	}
	return c
}()

func (d *Downsample16k) Process(p []byte) []byte {
	out := make([]byte, 0, len(p)*2/3+2)
	for i := 0; i+1 < len(p); i += 2 {
		value := float64(int16(uint16(p[i]) | uint16(p[i+1])<<8))
		for up := 0; up < 2; up++ {
			d.history[d.position] = value
			value = 0
			d.position = (d.position + 1) % len(d.history)
			d.phase++
			if d.phase != 3 {
				continue
			}
			d.phase = 0
			var filtered float64
			for j, c := range resample16Coefficients {
				filtered += c * d.history[(d.position-1-j+len(d.history))%len(d.history)]
			}
			sample := int16(math.Round(min(32767, max(-32768, filtered))))
			out = append(out, byte(sample), byte(uint16(sample)>>8))
		}
	}
	return out
}
