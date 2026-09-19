// Copyright (C) 2026 Joey Kot
// SPDX-License-Identifier: GPL-3.0-or-later
use anyhow::{Result, bail};
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum Mode {
    Http,
    Async,
    Realtime,
}
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum Protocol {
    General,
    Qwen,
    Paraformer,
}
#[derive(Debug, Clone, Copy)]
pub struct Route {
    pub rate: u32,
    pub mode: Mode,
    pub protocol: Protocol,
}
pub fn route(model: &str) -> Result<Route> {
    use {Mode::*, Protocol::*};
    let key = model.trim().to_ascii_lowercase();
    for (prefix, rate, mode, protocol) in [
        ("paraformer-realtime-8k-v2", 8000, Realtime, Paraformer),
        ("paraformer-realtime-8k-v1", 8000, Realtime, Paraformer),
        ("paraformer-realtime-v2", 24000, Realtime, Paraformer),
        ("paraformer-realtime-v1", 16000, Realtime, Paraformer),
        ("qwen3-asr-flash-realtime", 16000, Realtime, Qwen),
        (
            "qwen-audio-3.0-asr-flash-streaming",
            24000,
            Realtime,
            General,
        ),
        ("fun-asr-flash-8k-realtime", 8000, Realtime, General),
        ("fun-asr-realtime", 24000, Realtime, General),
        ("qwen-audio-3.0-asr-flash-filetrans", 16000, Async, General),
        ("qwen-audio-3.0-asr-flash", 16000, Http, General),
        ("qwen3-asr-flash-filetrans", 16000, Async, General),
        ("qwen3-asr-flash", 16000, Http, General),
        ("fun-asr-flash", 16000, Http, General),
        ("fun-asr", 16000, Async, General),
        ("paraformer-8k", 8000, Async, General),
        ("paraformer", 16000, Async, General),
    ] {
        if key.starts_with(prefix) {
            return Ok(Route {
                rate,
                mode,
                protocol,
            });
        }
    }
    bail!("unsupported model: {model:?}")
}
pub const MODELS: &[&str] = &[
    "paraformer-realtime-v2",
    "paraformer-realtime-v1",
    "paraformer-realtime-8k-v2",
    "paraformer-realtime-8k-v1",
    "qwen3-asr-flash-realtime",
    "qwen3-asr-flash-realtime-2026-02-10",
    "qwen3-asr-flash-realtime-2025-10-27",
    "qwen-audio-3.0-asr-flash-streaming",
    "fun-asr-realtime",
    "fun-asr-realtime-2025-11-07",
    "fun-asr-realtime-2026-02-28",
    "fun-asr-realtime-2025-09-15",
    "fun-asr-flash-8k-realtime",
    "fun-asr-flash-8k-realtime-2026-01-28",
    "qwen-audio-3.0-asr-flash-filetrans",
    "qwen-audio-3.0-asr-flash",
    "qwen3-asr-flash",
    "qwen3-asr-flash-filetrans",
    "qwen3-asr-flash-2025-09-08",
    "fun-asr",
    "fun-asr-2025-11-07",
    "fun-asr-2025-08-25",
    "fun-asr-mtl",
    "fun-asr-mtl-2025-08-25",
    "fun-asr-flash-2026-06-15",
    "paraformer-v2",
    "paraformer-v1",
    "paraformer-8k-v1",
    "paraformer-mtl-v1",
];
pub fn context(model: &str) -> bool {
    matches!(
        model.trim().to_ascii_lowercase().as_str(),
        "qwen-audio-3.0-asr-flash-streaming" | "fun-asr-realtime" | "fun-asr-realtime-2025-11-07"
    )
}
pub fn paraformer_v1(model: &str) -> bool {
    let k = model.to_ascii_lowercase();
    k.starts_with("paraformer-realtime-v1") || k.starts_with("paraformer-realtime-8k-v1")
}
pub fn language(model: &str, lang: &str) -> bool {
    if lang.is_empty() {
        return true;
    }
    let key = model.trim().to_ascii_lowercase();
    let langs = if key.starts_with("paraformer-realtime") {
        "zh en ja yue ko de fr ru"
    } else if key.starts_with("qwen3-asr-flash-realtime") {
        "zh yue en ja de ko ru fr pt ar it es hi id th tr uk vi cs da fil fi is ms no pl sv"
    } else if key.starts_with("fun-asr-flash-8k-realtime") {
        "zh"
    } else if key == "fun-asr-realtime-2026-02-28" {
        "zh en ja"
    } else if key == "fun-asr-realtime-2025-09-15" {
        "zh en"
    } else {
        "zh en ja ko vi th id ms tl hi ar fr de es pt ru it nl sv da fi no el pl cs hu ro bg hr sk"
    };
    langs.split_whitespace().any(|s| s == lang)
}
#[cfg(test)]
mod tests {
    use super::*;
    #[test]
    fn specificity() {
        for model in MODELS {
            assert!(route(model).is_ok());
        }
        assert_eq!(route("paraformer-realtime-8k-v1").unwrap().rate, 8000);
        assert_eq!(
            route("qwen-audio-3.0-asr-flash-filetrans").unwrap().mode,
            Mode::Async
        );
        assert!(route("unknown").is_err());
        assert_eq!(
            route("qwen3-asr-flash-filetrans").unwrap().mode,
            Mode::Async
        );
    }
}
