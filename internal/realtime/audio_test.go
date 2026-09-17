// Copyright (C) 2026 Joey Kot <joey.kot.x@gmail.com>
// SPDX-License-Identifier: GPL-3.0-or-later

package realtime

import (
	"bytes"
	"context"
	"encoding/binary"
	"io"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestResamplerPreservesChunkBoundariesAndRejectsAliasing(t *testing.T) {
	makeTone := func(hz float64) []byte {
		data := make([]byte, 48000)
		for i := 0; i < len(data)/2; i++ {
			binary.LittleEndian.PutUint16(data[i*2:], uint16(int16(10000*math.Sin(2*math.Pi*hz*float64(i)/24000))))
		}
		return data
	}
	input := makeTone(1000)
	var whole, chunked Downsample8k
	want := whole.Process(input)
	var got []byte
	for start := 0; start < len(input); start += 14 {
		got = append(got, chunked.Process(input[start:min(start+14, len(input))])...)
	}
	if !bytes.Equal(got, want) || len(got) != 16000 {
		t.Fatal("resampling depends on append boundaries")
	}
	rms := func(data []byte) float64 {
		var total float64
		for i := 200; i+1 < len(data); i += 2 {
			v := float64(int16(binary.LittleEndian.Uint16(data[i:])))
			total += v * v
		}
		return math.Sqrt(total / float64((len(data)-200)/2))
	}
	var high Downsample8k
	if ratio := rms(high.Process(makeTone(7000))) / rms(want); ratio > 0.05 {
		t.Fatalf("aliasing rejection too weak: %f", ratio)
	}
}

func TestDecodeFileStreamsPCMAndReportsCorruption(t *testing.T) {
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("ffmpeg unavailable")
	}
	path := filepath.Join(t.TempDir(), "test.wav")
	var wav bytes.Buffer
	wav.WriteString("RIFF")
	binary.Write(&wav, binary.LittleEndian, uint32(36+4800))
	wav.WriteString("WAVEfmt ")
	for _, v := range []any{uint32(16), uint16(1), uint16(1), uint32(24000), uint32(48000), uint16(2), uint16(16)} {
		binary.Write(&wav, binary.LittleEndian, v)
	}
	wav.WriteString("data")
	binary.Write(&wav, binary.LittleEndian, uint32(4800))
	wav.Write(make([]byte, 4800))
	if err := os.WriteFile(path, wav.Bytes(), 0600); err != nil {
		t.Fatal(err)
	}
	r, err := DecodeFile(context.Background(), path, 24000)
	if err != nil {
		t.Fatal(err)
	}
	pcm, err := io.ReadAll(r)
	r.Close()
	if err != nil || len(pcm) != 4800 {
		t.Fatalf("PCM: len=%d err=%v", len(pcm), err)
	}
	bad := filepath.Join(t.TempDir(), "bad.wav")
	os.WriteFile(bad, []byte("not audio"), 0600)
	r, err = DecodeFile(context.Background(), bad, 24000)
	if err != nil {
		t.Fatal(err)
	}
	_, err = io.ReadAll(r)
	r.Close()
	if err == nil {
		t.Fatal("corrupt audio treated as successful EOF")
	}
}

func TestResample16kContinuityAndFrequencyResponse(t *testing.T) {
	tone := func(hz float64) []byte {
		data := make([]byte, 48000)
		for i := 0; i < len(data)/2; i++ {
			binary.LittleEndian.PutUint16(data[2*i:], uint16(int16(10000*math.Sin(2*math.Pi*hz*float64(i)/24000))))
		}
		return data
	}
	input := tone(1000)
	var whole Downsample16k
	want := whole.Process(input)
	if len(want) != 32000 {
		t.Fatal("incorrect sample count", len(want))
	}
	for _, size := range []int{2, 4, 14, 4800} {
		var chunked Downsample16k
		var got []byte
		for i := 0; i < len(input); i += size {
			got = append(got, chunked.Process(input[i:min(i+size, len(input))])...)
		}
		if !bytes.Equal(got, want) {
			t.Fatal("chunk discontinuity", size)
		}
	}
	rms := func(data []byte) float64 {
		var sum float64
		for i := 512; i+1 < len(data); i += 2 {
			v := float64(int16(binary.LittleEndian.Uint16(data[i:])))
			sum += v * v
		}
		return math.Sqrt(sum / float64((len(data)-512)/2))
	}
	if level := rms(want); level < 6900 || level > 7200 {
		t.Fatal("passband gain", level)
	}
	var high Downsample16k
	if ratio := rms(high.Process(tone(10000))) / rms(want); ratio > 0.02 {
		t.Fatal("aliasing", ratio)
	}
}
