// Copyright (C) 2026 Joey Kot
// SPDX-License-Identifier: GPL-3.0-or-later
use anyhow::{Result, ensure};
use clap::Parser;
use std::time::Duration;

#[derive(Clone, Parser)]
#[command(version, about = "OpenAI-compatible DashScope transcription service")]
pub struct Config {
    #[arg(long, env = "LISTEN", default_value = ":8080")]
    pub listen: String,
    #[arg(long, env = "API_TOKEN", default_value = "", hide_env_values = true)]
    pub api_token: String,
    #[arg(
        long,
        env = "DASHSCOPE_API_KEY",
        default_value = "",
        hide_env_values = true
    )]
    pub dashscope_api_key: String,
    #[arg(
        long,
        env = "DASHSCOPE_HTTP_BASE_URL",
        default_value = "https://dashscope.aliyuncs.com/api/v1"
    )]
    pub dashscope_base_url: String,
    #[arg(long, env = "WEBDAV_URL", default_value = "")]
    pub webdav_url: String,
    #[arg(
        long,
        env = "WEBDAV_CREDENTIALS",
        default_value = "",
        hide_env_values = true
    )]
    pub webdav_credentials: String,
    #[arg(long, env = "API_CONCURRENCY", default_value_t = 10)]
    pub api_concurrency: usize,
    #[arg(long, env = "API_SEGMENT_LENGTH", default_value = "175", value_parser = seconds)]
    pub api_segment_length: Duration,
    #[arg(long, env = "SKIP_TRIM", default_value = "false", value_parser = boolean, action = clap::ArgAction::Set)]
    pub skip_trim: bool,
    #[arg(long, env = "LIBAV_CODEC_THREADS", default_value_t = 1)]
    pub libav_codec_threads: u16,
    #[arg(long, env = "PADDING_LENGTH", default_value = "100", value_parser = milliseconds)]
    pub padding: Duration,
    /// Speech onset threshold for non-realtime audio preprocessing (0.5..=1.0).
    #[arg(long, env = "VAD_START_THRESHOLD", default_value_t = 0.6, value_parser = vad_threshold)]
    pub vad_start_threshold: f32,
    #[arg(long, env = "OUTPUT_BITRATE", default_value = "128k", value_parser = bitrate)]
    pub output_bitrate: u32,
    #[arg(long, env = "ENABLE_ITN", default_value = "false", value_parser = boolean, action = clap::ArgAction::Set)]
    pub enable_itn: bool,
    #[arg(long, env = "UPSTREAM_TIMEOUT_SECONDS", default_value = "30", value_parser = seconds)]
    pub upstream_timeout: Duration,
    #[arg(long, env = "ASR_RETRY_MAX_ATTEMPTS", default_value_t = 4)]
    pub asr_retry_max_attempts: usize,
    #[arg(long, env = "ASR_RETRY_INITIAL_DELAY", default_value = "0.5", value_parser = seconds)]
    pub asr_retry_initial_delay: Duration,
    #[arg(long, env = "ASR_RETRY_FACTOR", default_value_t = 2.)]
    pub asr_retry_factor: f64,
    #[arg(long, env = "ASR_RETRY_MAX_DELAY", default_value = "8", value_parser = seconds)]
    pub asr_retry_max_delay: Duration,
    #[arg(long, env = "MAX_UPLOAD_MB", default_value_t = 500)]
    pub max_upload_mb: usize,
    #[arg(
        long,
        env = "DASHSCOPE_WS_URL",
        default_value = "wss://dashscope.aliyuncs.com/api-ws/v1/inference"
    )]
    pub dashscope_ws_url: String,
    #[arg(
        long,
        env = "DASHSCOPE_QWEN_WS_URL",
        default_value = "wss://dashscope.aliyuncs.com/api-ws/v1/realtime"
    )]
    pub dashscope_qwen_ws_url: String,
    #[arg(long, env = "DASHSCOPE_WORKSPACE", default_value = "")]
    pub dashscope_workspace: String,
    #[arg(long, env = "REALTIME_CONNECT_TIMEOUT_SECONDS", default_value = "10", value_parser = seconds)]
    pub realtime_connect_timeout: Duration,
    #[arg(long, env = "REALTIME_START_TIMEOUT_SECONDS", default_value = "10", value_parser = seconds)]
    pub realtime_start_timeout: Duration,
    #[arg(long, env = "REALTIME_FINISH_TIMEOUT_SECONDS", default_value = "30", value_parser = seconds)]
    pub realtime_finish_timeout: Duration,
    #[arg(long, env = "REALTIME_WRITE_TIMEOUT_SECONDS", default_value = "10", value_parser = seconds)]
    pub realtime_write_timeout: Duration,
    #[arg(long, env = "REALTIME_IDLE_TIMEOUT_SECONDS", default_value = "120", value_parser = seconds)]
    pub realtime_idle_timeout: Duration,
    #[arg(long, env = "REALTIME_CONCURRENCY", default_value_t = 10)]
    pub realtime_concurrency: usize,
}
pub fn boolean(v: &str) -> std::result::Result<bool, String> {
    match v.trim().to_ascii_lowercase().as_str() {
        "true" | "t" | "1" => Ok(true),
        "false" | "f" | "0" => Ok(false),
        _ => Err("must be 0/1 or true/false".into()),
    }
}
fn duration(v: &str, unit: f64) -> std::result::Result<Duration, String> {
    if let Ok(n) = v.parse::<f64>() {
        return Duration::try_from_secs_f64(n * unit).map_err(|e| e.to_string());
    }
    humantime::parse_duration(v).map_err(|e| e.to_string())
}
fn seconds(v: &str) -> std::result::Result<Duration, String> {
    duration(v, 1.)
}
fn milliseconds(v: &str) -> std::result::Result<Duration, String> {
    duration(v, 0.001)
}
fn vad_threshold(v: &str) -> std::result::Result<f32, String> {
    let value = v.parse::<f32>().map_err(|e| e.to_string())?;
    smartaudio::VadConfig {
        start_threshold: value,
        ..Default::default()
    }
    .validate()
    .map_err(|e| e.to_string())?;
    Ok(value)
}
fn bitrate(v: &str) -> std::result::Result<u32, String> {
    let value = v.trim().to_ascii_lowercase();
    let (n, scale) = value
        .strip_suffix('k')
        .map_or((value.as_str(), 1), |n| (n, 1000));
    n.parse::<u32>()
        .ok()
        .and_then(|n| n.checked_mul(scale))
        .filter(|n| *n > 0)
        .ok_or("invalid output bitrate".into())
}
impl Config {
    pub fn tokens(&self) -> Vec<&str> {
        self.api_token
            .split(',')
            .map(str::trim)
            .filter(|v| !v.is_empty())
            .collect()
    }
    pub fn validate(&self) -> Result<()> {
        ensure!(
            self.max_upload_mb > 0 && self.max_upload_mb <= (usize::MAX - (32 << 20)) / (1 << 20),
            "max-upload-mb out of range"
        );
        ensure!(
            self.realtime_concurrency > 0,
            "realtime concurrency must be positive"
        );
        for d in [
            self.realtime_connect_timeout,
            self.realtime_start_timeout,
            self.realtime_finish_timeout,
            self.realtime_write_timeout,
            self.realtime_idle_timeout,
        ] {
            ensure!(!d.is_zero(), "realtime timeouts must be positive");
        }
        for address in [&self.dashscope_ws_url, &self.dashscope_qwen_ws_url] {
            let u = url::Url::parse(address)?;
            ensure!(
                u.scheme() == "wss"
                    && u.host_str().is_some()
                    && u.username().is_empty()
                    && u.password().is_none()
                    && u.fragment().is_none(),
                "realtime URL must be wss without credentials or fragment"
            );
        }
        if !self.webdav_url.is_empty() && !self.webdav_credentials.is_empty() {
            let u = url::Url::parse(&self.webdav_url)?;
            ensure!(
                u.scheme() == "https"
                    && u.host_str().is_some()
                    && u.username().is_empty()
                    && u.password().is_none()
                    && u.query().is_none()
                    && u.fragment().is_none(),
                "webdav-url must be a public HTTPS URL"
            );
            ensure!(
                self.webdav_credentials
                    .split_once('@')
                    .is_some_and(|(u, p)| !u.is_empty() && !p.is_empty()),
                "webdav-credentials must use user@password form"
            );
        }
        Ok(())
    }
    pub fn validate_offline(&self) -> Result<()> {
        smartaudio::VadConfig {
            padding_ms: 0,
            start_threshold: self.vad_start_threshold,
        }
        .validate()?;
        ensure!(
            self.api_concurrency > 0 && self.asr_retry_max_attempts > 0,
            "offline concurrency and retry attempts must be positive"
        );
        ensure!(
            !self.asr_retry_initial_delay.is_zero()
                && !self.asr_retry_max_delay.is_zero()
                && self.asr_retry_factor.is_finite()
                && self.asr_retry_factor > 0.,
            "invalid retry settings"
        );
        ensure!(
            self.padding <= Duration::from_secs(1),
            "padding must be 0..=1000ms"
        );
        ensure!(
            !self.api_segment_length.is_zero(),
            "api-segment-length must be positive"
        );
        Ok(())
    }
}
#[cfg(test)]
mod tests {
    use super::*;
    #[test]
    fn removed_options_and_units() {
        for flag in [
            "--fixed-slice-length=5s",
            "--fixed-slice-workers=16",
            "--segment-workers=0",
            "--silent-interval=700ms",
        ] {
            assert!(Config::try_parse_from(["test", flag]).is_err());
        }
        assert_eq!(seconds("0.5").unwrap(), Duration::from_millis(500));
        assert_eq!(seconds("750ms").unwrap(), Duration::from_millis(750));
        assert_eq!(milliseconds("100").unwrap(), Duration::from_millis(100));
        assert_eq!(bitrate("128k").unwrap(), 128000);
        assert!(seconds("NaN").is_err());
    }
    #[test]
    fn vad_threshold_bounds() {
        assert_eq!(
            Config::try_parse_from(["test"])
                .unwrap()
                .vad_start_threshold,
            smartaudio::VadConfig::default().start_threshold
        );
        for value in ["0.5", "0.6", "0.9", "1.0"] {
            let cfg = Config::try_parse_from(["test", &format!("--vad-start-threshold={value}")])
                .unwrap();
            assert_eq!(cfg.vad_start_threshold, value.parse::<f32>().unwrap());
            cfg.validate_offline().unwrap();
        }
        for value in ["0.49", "1.01", "NaN", "inf", "-inf", "text"] {
            assert!(
                Config::try_parse_from(["test", &format!("--vad-start-threshold={value}")])
                    .is_err()
            );
        }
    }
}
