// Copyright (C) 2026 Joey Kot
// SPDX-License-Identifier: GPL-3.0-or-later
use crate::config::Config;
use anyhow::{Result, ensure};
use smartaudio::{Cancellation, Control, Input, Mode, Processor};
use std::{path::PathBuf, sync::Arc};
use tempfile::TempDir;

struct CancelOnDrop(Cancellation);
impl Drop for CancelOnDrop {
    fn drop(&mut self) {
        self.0.cancel();
    }
}

pub async fn prepare(
    cfg: &Config,
    input: PathBuf,
    work: Arc<TempDir>,
    rate: u32,
    skip_trim: bool,
    max_bytes: u64,
) -> Result<Vec<PathBuf>> {
    cfg.validate_offline()?;
    let config = smartaudio::Config {
        mode: if skip_trim {
            Mode::Split
        } else {
            Mode::Process
        },
        vad: smartaudio::VadConfig {
            padding_ms: cfg.padding.as_millis() as u32,
            start_threshold: cfg.vad_start_threshold,
        },
        output: smartaudio::Output {
            sample_rate: rate,
            bitrate: Some(cfg.output_bitrate),
            threads: cfg.libav_codec_threads,
            ..Default::default()
        },
        max_duration_ms: u64::try_from(cfg.api_segment_length.as_millis())?,
        max_bytes: Some(max_bytes),
        work_dir: Some(work.path().join("work")),
        ..Default::default()
    };
    let guard = CancelOnDrop(Cancellation::default());
    let cancellation = guard.0.clone();
    let result = tokio::task::spawn_blocking(move || {
        let control = Control {
            cancellation,
            progress: None,
        };
        // Keep the request directory alive until native code has stopped, including cancellation.
        Processor.run(
            &Input::new(input),
            &work.path().join("segments"),
            &config,
            &control,
        )
    })
    .await??;
    tracing::info!(status=?result.status, segments=result.output_file_count, input_seconds=result.input_duration_seconds, output_seconds=result.output_duration_seconds, "audio prepared");
    Ok(result.files.into_iter().map(|f| f.path).collect())
}

/// Stateful low-pass conversion from downstream 24 kHz PCM16 to 8/16 kHz.
pub struct Resampler {
    history: Vec<f64>,
    coefficients: Vec<f64>,
    position: usize,
    phase: usize,
    up: usize,
    rate: u32,
}
impl Resampler {
    pub fn new(rate: u32) -> Self {
        let up = if rate == 16000 { 2 } else { 1 };
        let length = if up == 2 { 127 } else { 63 };
        let mut coefficients = Vec::with_capacity(length);
        for i in 0..length {
            let x = i as f64 - (length / 2) as f64;
            let v = if x == 0. {
                0.3
            } else {
                (0.3 * std::f64::consts::PI * x).sin() / (std::f64::consts::PI * x)
            };
            coefficients.push(
                v * (0.54
                    - 0.46 * (2. * std::f64::consts::PI * i as f64 / (length - 1) as f64).cos()),
            );
        }
        let sum: f64 = coefficients.iter().sum();
        coefficients.iter_mut().for_each(|v| *v *= up as f64 / sum);
        Self {
            history: vec![0.; length],
            coefficients,
            position: 0,
            phase: 0,
            up,
            rate,
        }
    }
    pub fn process(&mut self, data: &[u8]) -> Result<Vec<u8>> {
        ensure!(
            data.len().is_multiple_of(2),
            "PCM must contain complete 16-bit samples"
        );
        if self.rate == 24000 {
            return Ok(data.to_vec());
        }
        let mut out = Vec::with_capacity(data.len() * self.up / 3 + 2);
        for sample in data.as_chunks::<2>().0 {
            let mut value = i16::from_le_bytes([sample[0], sample[1]]) as f64;
            for _ in 0..self.up {
                self.history[self.position] = value;
                value = 0.;
                self.position = (self.position + 1) % self.history.len();
                self.phase += 1;
                if self.phase != 3 {
                    continue;
                }
                self.phase = 0;
                let mut sum = 0.;
                for (i, c) in self.coefficients.iter().enumerate() {
                    sum += c * self.history
                        [(self.position + self.history.len() - 1 - i) % self.history.len()];
                }
                let sum = sum.clamp(-32768., 32767.);
                let sample = if self.up == 2 {
                    sum.round() as i16
                } else {
                    sum as i16
                };
                out.extend_from_slice(&sample.to_le_bytes());
            }
        }
        Ok(out)
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    fn tone(hz: f64) -> Vec<u8> {
        (0..24000)
            .flat_map(|i| {
                ((10000. * (2. * std::f64::consts::PI * hz * i as f64 / 24000.).sin()) as i16)
                    .to_le_bytes()
            })
            .collect()
    }
    fn rms(p: &[u8]) -> f64 {
        let values: Vec<_> = p[512..]
            .as_chunks::<2>()
            .0
            .iter()
            .map(|b| f64::from(i16::from_le_bytes([b[0], b[1]])))
            .collect();
        (values.iter().map(|v| v * v).sum::<f64>() / values.len() as f64).sqrt()
    }
    #[test]
    fn continuity_and_alias_rejection() {
        for rate in [8000, 16000] {
            let input = tone(1000.);
            let whole = Resampler::new(rate).process(&input).unwrap();
            assert_eq!(whole.len(), rate as usize * 2);
            for size in [2, 14, 4800] {
                let mut r = Resampler::new(rate);
                let chunks: Vec<u8> = input
                    .chunks(size)
                    .flat_map(|c| r.process(c).unwrap())
                    .collect();
                assert_eq!(whole, chunks);
            }
            let high = Resampler::new(rate)
                .process(&tone(if rate == 8000 { 7000. } else { 10000. }))
                .unwrap();
            assert!(rms(&high) / rms(&whole) < 0.05);
        }
    }
}
