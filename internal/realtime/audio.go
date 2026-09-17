// Copyright (C) 2026 Joey Kot <joey.kot.x@gmail.com>
// SPDX-License-Identifier: GPL-3.0-or-later

package realtime

import (
	"context"
	"fmt"
	"io"
	"os/exec"
	"strconv"
	"sync"
)

// DecodeFile streams mono PCM16 without trimming, segmenting or buffering a
// whole decoded file. FFmpeg is only needed by the file entry point.
func DecodeFile(ctx context.Context, path string, rate int) (io.ReadCloser, error) {
	ctx, cancel := context.WithCancel(ctx)
	cmd := exec.CommandContext(ctx, "ffmpeg", "-nostdin", "-hide_banner", "-loglevel", "error", "-threads", "1", "-i", path, "-map", "0:a:0", "-vn", "-ac", "1", "-ar", strconv.Itoa(rate), "-c:a", "pcm_s16le", "-threads", "1", "-f", "s16le", "pipe:1")
	d := &decoder{cmd: cmd, cancel: cancel}
	cmd.Stderr = &d.stderr
	pipe, err := cmd.StdoutPipe()
	if err != nil {
		cancel()
		return nil, err
	}
	d.pipe = pipe
	if err := cmd.Start(); err != nil {
		cancel()
		pipe.Close()
		return nil, fmt.Errorf("启动实时音频解码失败（需要 ffmpeg）: %w", err)
	}
	return d, nil
}

type decoder struct {
	cmd    *exec.Cmd
	cancel context.CancelFunc
	pipe   io.ReadCloser
	stderr limitedBuffer
	once   sync.Once
	err    error
}

type limitedBuffer struct{ data []byte }

func (b *limitedBuffer) Write(p []byte) (int, error) {
	n := len(p)
	if len(b.data) < 4096 {
		b.data = append(b.data, p[:min(n, 4096-len(b.data))]...)
	}
	return n, nil
}

func (d *decoder) wait() error {
	d.once.Do(func() {
		if err := d.cmd.Wait(); err != nil {
			d.err = fmt.Errorf("实时音频解码失败: %w: %s", err, d.stderr.data)
		}
	})
	return d.err
}
func (d *decoder) Read(p []byte) (int, error) {
	n, err := d.pipe.Read(p)
	if err == io.EOF {
		if waitErr := d.wait(); waitErr != nil {
			return n, waitErr
		}
	}
	return n, err
}
func (d *decoder) Close() error {
	d.cancel()
	d.pipe.Close()
	return d.wait()
}

// Downsample8k is a stateful 24kHz-to-8kHz PCM16 low-pass FIR decimator.
// State spans append boundaries; processing chunks independently loses samples.
type Downsample8k struct {
	history         [63]float64
	position, phase int
}

func (d *Downsample8k) Process(p []byte) []byte {
	out := make([]byte, 0, len(p)/3+2)
	for i := 0; i+1 < len(p); i += 2 {
		d.history[d.position] = float64(int16(uint16(p[i]) | uint16(p[i+1])<<8))
		d.position = (d.position + 1) % len(d.history)
		d.phase++
		if d.phase != 3 {
			continue
		}
		d.phase = 0
		var value float64
		for j, c := range decimatorCoefficients {
			value += c * d.history[(d.position-1-j+len(d.history))%len(d.history)]
		}
		value = min(32767, max(-32768, value))
		sample := int16(value)
		out = append(out, byte(sample), byte(uint16(sample)>>8))
	}
	return out
}
