// Copyright (C) 2026 Joey Kot
// SPDX-License-Identifier: GPL-3.0-or-later
use std::process::Command;
#[test]
fn help_excludes_removed_options_and_secrets() {
    let output = Command::new(env!("CARGO_BIN_EXE_qwen-stt-compatible"))
        .arg("--help")
        .env("API_TOKEN", "do-not-print-this-token")
        .env("DASHSCOPE_API_KEY", "do-not-print-this-key")
        .env("WEBDAV_CREDENTIALS", "do-not-print-this-password")
        .output()
        .unwrap();
    assert!(output.status.success());
    let help = String::from_utf8(output.stdout).unwrap();
    for removed in [
        "--fixed-slice-length",
        "--fixed-slice-workers",
        "--segment-workers",
        "--silent-interval",
        "--enable-lid",
        "ENABLE_LID",
        "FFMPEG_WORKS",
        "FFMPEG_SEGMENT_LENGTH",
        "SEGMENT_WORKERS",
        "SILENT_INTERVAL",
        "do-not-print",
    ] {
        assert!(!help.contains(removed), "{removed}");
    }
    for kept in [
        "--api-concurrency",
        "--api-segment-length",
        "--padding",
        "--vad-start-threshold",
        "VAD_START_THRESHOLD",
        "--output-bitrate",
        "--libav-codec-threads",
        "--skip-trim",
        "--base64-first",
        "BASE64_FIRST",
    ] {
        assert!(help.contains(kept), "{kept}");
    }
}
