// Copyright (C) 2026 Joey Kot
// SPDX-License-Identifier: GPL-3.0-or-later
use axum::{
    Json, Router,
    body::{Bytes, to_bytes},
    extract::Request,
    http::{Method, StatusCode},
    response::IntoResponse,
    routing::{any, post},
};
use clap::Parser;
use qwen_stt_compatible::{
    audio,
    config::Config,
    dashscope::FLASH_FILE_LIMIT,
    httpapi::{App, router},
};
use serde_json::{Value, json};
use std::{
    sync::{
        Arc,
        atomic::{AtomicUsize, Ordering},
    },
    time::Duration,
};

fn cfg() -> Config {
    Config::try_parse_from([
        "test",
        "--api-token=key",
        "--dashscope-api-key=upstream",
        "--api-segment-length=3s",
        "--api-concurrency=2",
    ])
    .unwrap()
}

#[tokio::test]
async fn preprocess_speech_modes_and_no_speech() {
    let mut config = cfg();
    config.padding = Duration::ZERO;
    for split in [false, true] {
        let work = Arc::new(tempfile::tempdir().unwrap());
        let paths = audio::prepare(
            &config,
            "tests/fixtures/jfk.flac".into(),
            work.clone(),
            16000,
            split,
            FLASH_FILE_LIMIT,
        )
        .await
        .unwrap();
        assert!(paths.len() > 1);
        for path in paths {
            assert!(path.starts_with(work.path()));
            assert_eq!(path.extension().unwrap(), "ogg");
            assert!(std::fs::metadata(path).unwrap().len() <= FLASH_FILE_LIMIT);
        }
    }
    let work = Arc::new(tempfile::tempdir().unwrap());
    let path = work.path().join("silence.wav");
    let mut writer = hound::WavWriter::create(
        &path,
        hound::WavSpec {
            channels: 1,
            sample_rate: 16000,
            bits_per_sample: 16,
            sample_format: hound::SampleFormat::Int,
        },
    )
    .unwrap();
    for _ in 0..16000 {
        writer.write_sample(0i16).unwrap();
    }
    writer.finalize().unwrap();
    assert!(
        audio::prepare(&config, path, work, 16000, false, FLASH_FILE_LIMIT)
            .await
            .unwrap()
            .is_empty()
    );
}

#[tokio::test]
async fn multipart_segments_concurrency_order_and_sse() {
    let listener = tokio::net::TcpListener::bind("127.0.0.1:0").await.unwrap();
    let upstream = format!("http://{}", listener.local_addr().unwrap());
    let issued = Arc::new(AtomicUsize::new(0));
    let active = Arc::new(AtomicUsize::new(0));
    let maximum = Arc::new(AtomicUsize::new(0));
    let calls = issued.clone();
    let expected_itn = Arc::new(std::sync::atomic::AtomicBool::new(true));
    let itn = expected_itn.clone();
    let count = active.clone();
    let peak = maximum.clone();
    let mock = Router::new().route(
        "/services/aigc/multimodal-generation/generation",
        post(move |Json(value): Json<Value>| {
            let itn = itn.clone();
            let calls = calls.clone();
            let active = count.clone();
            let peak = peak.clone();
            async move {
                assert_eq!(value["model"], "qwen3-asr-flash");
                assert_eq!(
                    value["parameters"]["asr_options"]["enable_itn"],
                    itn.load(Ordering::SeqCst)
                );
                assert!(
                    value["input"]["messages"][0]["content"][0]["audio"]
                        .as_str()
                        .unwrap()
                        .starts_with("http://")
                );
                let index = calls.fetch_add(1, Ordering::SeqCst);
                let current = active.fetch_add(1, Ordering::SeqCst) + 1;
                peak.fetch_max(current, Ordering::SeqCst);
                tokio::time::sleep(Duration::from_millis(if index.is_multiple_of(2) {
                    60
                } else {
                    1
                }))
                .await;
                active.fetch_sub(1, Ordering::SeqCst);
                Json(json!({"output":{"text":format!("{index},")}}))
            }
        }),
    );
    let mock = mock.merge(file_store());
    let mock_task = tokio::spawn(async move { axum::serve(listener, mock).await.unwrap() });
    let mut config = cfg();
    config.webdav_url = format!("{upstream}/dav");
    config.base64_first = false;
    config.webdav_credentials = "user@password".into();
    config.dashscope_base_url = upstream;
    config.enable_itn = true;
    let app = App::new(config).unwrap();
    let listener = tokio::net::TcpListener::bind("127.0.0.1:0").await.unwrap();
    let url = format!(
        "http://{}/v1/audio/transcriptions",
        listener.local_addr().unwrap()
    );
    let service = router(app);
    let task = tokio::spawn(async move { axum::serve(listener, service).await.unwrap() });
    let client = reqwest::Client::new();
    for (stream, override_itn) in [(false, None), (true, Some("false")), (false, Some("true"))] {
        expected_itn.store(override_itn != Some("false"), Ordering::SeqCst);
        issued.store(0, Ordering::SeqCst);
        let mut form = reqwest::multipart::Form::new();
        if let Some(value) = override_itn {
            form = form.text("enable_itn", value);
        }
        let response = client
            .post(&url)
            .bearer_auth("key")
            .multipart(
                form.text("model", "qwen3-asr-flash")
                    .text("stream", stream.to_string())
                    .part(
                        "file",
                        reqwest::multipart::Part::bytes(
                            std::fs::read("tests/fixtures/jfk.flac").unwrap(),
                        )
                        .file_name("jfk.flac"),
                    ),
            )
            .send()
            .await
            .unwrap();
        assert_eq!(response.status(), 200);
        let text = response.text().await.unwrap();
        let total = issued.load(Ordering::SeqCst);
        assert!(total > 1);
        let transcript = if stream {
            assert!(text.contains("transcript.text.done"));
            assert!(text.ends_with("data: [DONE]\n\n"));
            let done = text
                .lines()
                .filter_map(|line| line.strip_prefix("data: "))
                .filter_map(|data| serde_json::from_str::<Value>(data).ok())
                .find(|v| v["type"] == "transcript.text.done")
                .unwrap();
            done["text"].as_str().unwrap().to_string()
        } else {
            let body: Value = serde_json::from_str(&text).unwrap();
            body["text"].as_str().unwrap().to_string()
        };
        // TCP arrival order differs from source-segment order; all results must
        // occur exactly once. Deterministic ordering is tested separately.
        let mut indices: Vec<usize> = transcript
            .split(',')
            .filter(|s| !s.is_empty())
            .map(|s| s.parse().unwrap())
            .collect();
        indices.sort();
        assert_eq!(indices, (0..total).collect::<Vec<_>>());
    }
    assert_eq!(maximum.load(Ordering::SeqCst), 2);
    task.abort();
    mock_task.abort();
}

#[tokio::test]
async fn recognition_merges_source_order_despite_out_of_order_completion() {
    use qwen_stt_compatible::{dashscope::Options, httpapi::recognize};
    let listener = tokio::net::TcpListener::bind("127.0.0.1:0").await.unwrap();
    let address = listener.local_addr().unwrap();
    let mock = Router::new().route(
        "/services/aigc/multimodal-generation/generation",
        post(|Json(v): Json<Value>| async move {
            let uri = v["input"]["messages"][0]["content"][0]["audio"]
                .as_str()
                .unwrap();
            assert!(uri.starts_with("http://"));
            let text = reqwest::get(uri)
                .await
                .unwrap()
                .error_for_status()
                .unwrap()
                .text()
                .await
                .unwrap();
            if text == "first" {
                tokio::time::sleep(Duration::from_millis(80)).await;
            }
            Json(json!({"output":{"text":text}}))
        }),
    );
    let mock = mock.merge(file_store());
    let task = tokio::spawn(async move { axum::serve(listener, mock).await.unwrap() });
    let mut config = cfg();
    config.dashscope_base_url = format!("http://{address}");
    config.webdav_url = format!("http://{address}/dav");
    config.base64_first = false;
    config.webdav_credentials = "user@password".into();
    let app = App::new(config).unwrap();
    let dir = tempfile::tempdir().unwrap();
    let mut paths = Vec::new();
    for text in ["first", "second", "third"] {
        let path = dir.path().join(format!("{text}.ogg"));
        std::fs::write(&path, text).unwrap();
        paths.push(path);
    }
    assert_eq!(
        recognize(app, paths, "qwen3-asr-flash".into(), Options::default())
            .await
            .unwrap(),
        "firstsecondthird"
    );
    task.abort();
}

fn file_store() -> Router {
    let files = Arc::new(std::sync::Mutex::new(std::collections::HashMap::<
        String,
        Bytes,
    >::new()));
    Router::new().route(
        "/dav/{file}",
        any(move |req: Request| {
            let files = files.clone();
            async move {
                let path = req.uri().path().to_owned();
                match *req.method() {
                    Method::PUT => {
                        let bytes = to_bytes(req.into_body(), 16 << 20).await.unwrap();
                        files.lock().unwrap().insert(path, bytes);
                        StatusCode::CREATED.into_response()
                    }
                    Method::GET => files
                        .lock()
                        .unwrap()
                        .get(&path)
                        .cloned()
                        .unwrap()
                        .into_response(),
                    Method::DELETE => {
                        files.lock().unwrap().remove(&path);
                        StatusCode::NO_CONTENT.into_response()
                    }
                    _ => StatusCode::METHOD_NOT_ALLOWED.into_response(),
                }
            }
        }),
    )
}
