// Copyright (C) 2026 Joey Kot
// SPDX-License-Identifier: GPL-3.0-or-later
use axum::{
    Json, Router,
    body::to_bytes,
    extract::Request,
    http::{Method, StatusCode},
    routing::{any, get, post},
};
use clap::Parser;
use qwen_stt_compatible::{
    config::Config,
    dashscope::{DashScope, Options},
};
use serde_json::{Value, json};
use std::{
    sync::{
        Arc, Mutex,
        atomic::{AtomicUsize, Ordering},
    },
    time::Duration,
};

async fn serve(app: Router) -> (String, tokio::task::JoinHandle<()>) {
    let listener = tokio::net::TcpListener::bind("127.0.0.1:0").await.unwrap();
    let url = format!("http://{}", listener.local_addr().unwrap());
    let task = tokio::spawn(async move { axum::serve(listener, app).await.unwrap() });
    (url, task)
}
fn config() -> Config {
    Config::try_parse_from([
        "test",
        "--dashscope-api-key=upstream-key",
        "--asr-retry-initial-delay=1ms",
        "--asr-retry-max-delay=2ms",
    ])
    .unwrap()
}

#[tokio::test]
async fn default_base64_only_for_synchronous_flash_models() {
    use base64::{Engine, engine::general_purpose::STANDARD};
    let (url, task) = serve(Router::new().route(
        "/services/aigc/multimodal-generation/generation",
        post(|req: Request| async move {
            assert!(!req.headers().contains_key("X-DashScope-OssResourceResolve"));
            let v: Value =
                serde_json::from_slice(&to_bytes(req.into_body(), 1 << 20).await.unwrap()).unwrap();
            let content = &v["input"]["messages"][0]["content"][0];
            let data = content["audio"]
                .as_str()
                .or_else(|| content["input_audio"]["data"].as_str())
                .unwrap();
            assert!(data.starts_with("data:audio/ogg;base64,"));
            assert_eq!(
                STANDARD.decode(data.split_once(',').unwrap().1).unwrap(),
                b"audio"
            );
            Json(json!({"output":{"text":"base64"}}))
        }),
    ))
    .await;
    let mut cfg = config();
    assert!(cfg.base64_first);
    cfg.dashscope_base_url = url;
    // Even configured WebDAV must not be contacted in Base64 mode.
    cfg.webdav_url = "http://127.0.0.1:1/dav".into();
    cfg.webdav_credentials = "user@password".into();
    let client = DashScope::new(Arc::new(cfg)).unwrap();
    let dir = tempfile::tempdir().unwrap();
    let path = dir.path().join("audio.ogg");
    std::fs::write(&path, b"audio").unwrap();
    for model in [
        "qwen3-asr-flash",
        "qwen-audio-3.0-asr-flash",
        "fun-asr-flash",
    ] {
        assert_eq!(
            client
                .transcribe(&path, model, &Options::default())
                .await
                .unwrap(),
            "base64"
        );
    }
    task.abort();
}

#[tokio::test]
async fn flash_payload_retry_and_models() {
    let deleted = Arc::new(AtomicUsize::new(0));
    let deletes = deleted.clone();
    let (files, files_task) = serve(Router::new().route(
        "/dav/{file}",
        any(move |req: Request| {
            let deletes = deletes.clone();
            async move {
                assert!(req.headers().contains_key("Authorization"));
                if req.method() == Method::DELETE {
                    deletes.fetch_add(1, Ordering::SeqCst);
                    StatusCode::NO_CONTENT
                } else {
                    assert_eq!(req.method(), Method::PUT);
                    assert_eq!(
                        to_bytes(req.into_body(), 1024).await.unwrap().as_ref(),
                        b"audio"
                    );
                    StatusCode::CREATED
                }
            }
        }),
    ))
    .await;
    let calls = Arc::new(AtomicUsize::new(0));
    let payloads = Arc::new(Mutex::new(Vec::new()));
    let counter = calls.clone();
    let captured = payloads.clone();
    let (url,task)=serve(Router::new().route("/services/aigc/multimodal-generation/generation",post(move |req:Request|{let count=counter.clone();let captured=captured.clone();async move {
        assert_eq!(req.headers()["Authorization"],"Bearer upstream-key");assert!(!req.headers().contains_key("X-DashScope-OssResourceResolve"));let body=to_bytes(req.into_body(),16<<20).await.unwrap();captured.lock().unwrap().push(serde_json::from_slice::<Value>(&body).unwrap());
        if count.fetch_add(1,Ordering::SeqCst)==0 {return (StatusCode::SERVICE_UNAVAILABLE,Json(json!({"message":"retry"})))}
        (StatusCode::OK,Json(json!({"output":{"choices":[{"message":{"content":[{"text":"hello "},{"text":"world"}]}}]}})))
    }}))).await;
    let dir = tempfile::tempdir().unwrap();
    let path = dir.path().join("a.ogg");
    std::fs::write(&path, b"audio").unwrap();
    let mut cfg = config();
    cfg.dashscope_base_url = url;
    cfg.base64_first = false;
    cfg.webdav_url = format!("{files}/dav");
    cfg.webdav_credentials = "user@password".into();
    let client = DashScope::new(Arc::new(cfg)).unwrap();
    let options = Options {
        language: "en".into(),
        prompt: "names".into(),
        enable_itn: true,
        rate: 16000,
    };
    for model in [
        "qwen3-asr-flash",
        "qwen-audio-3.0-asr-flash",
        "fun-asr-flash-2026-06-15",
    ] {
        assert_eq!(
            client.transcribe(&path, model, &options).await.unwrap(),
            "hello world"
        );
    }
    assert_eq!(calls.load(Ordering::SeqCst), 4);
    tokio::time::timeout(Duration::from_secs(3), async {
        while deleted.load(Ordering::SeqCst) != 4 {
            tokio::task::yield_now().await;
        }
    })
    .await
    .unwrap();
    let payloads = payloads.lock().unwrap();
    for payload in payloads.iter() {
        let content = &payload["input"]["messages"][1]["content"][0];
        let url = content["audio"]
            .as_str()
            .or_else(|| content["input_audio"]["data"].as_str())
            .unwrap();
        assert!(url.starts_with(&format!("{files}/dav/")), "{url}");
    }
    let q = &payloads[0];
    assert_eq!(q["input"]["messages"][0]["role"], "system");
    assert_eq!(q["parameters"]["asr_options"]["enable_itn"], true);
    assert_eq!(q["parameters"]["asr_options"]["language"], "en");
    let a = &payloads[2];
    assert_eq!(
        a["input"]["messages"][0]["content"][0]["type"],
        "input_text"
    );
    assert_eq!(a["parameters"]["sample_rate"], "16000");
    assert_eq!(a["parameters"]["language_hints"], json!(["en"]));
    for payload in &payloads[2..] {
        assert!(payload["parameters"].get("asr_options").is_none());
        assert!(payload["parameters"].get("enable_itn").is_none());
    }
    task.abort();
    files_task.abort();
}

#[tokio::test]
async fn async_webdav_and_cleanup_on_success_and_failure() {
    for (success, model, itn) in [
        (true, "fun-asr", false),
        (true, "qwen-audio-3.0-asr-flash-filetrans", false),
        (true, "paraformer-v2", false),
        (false, "fun-asr", false),
        (true, "qwen3-asr-flash-filetrans", true),
        (true, "qwen3-asr-flash-filetrans", false),
        (false, "qwen3-asr-flash-filetrans", true),
    ] {
        let qwen_file = model == "qwen3-asr-flash-filetrans";
        let deleted = Arc::new(AtomicUsize::new(0));
        let uploads = Arc::new(Mutex::new(Vec::new()));
        let delete_count = deleted.clone();
        let uploaded = uploads.clone();
        let (files, files_task) = serve(Router::new().route(
            "/dav/{file}",
            any(move |req: Request| {
                let deleted = delete_count.clone();
                let uploaded = uploaded.clone();
                async move {
                    assert!(
                        req.headers()
                            .get("Authorization")
                            .unwrap()
                            .to_str()
                            .unwrap()
                            .starts_with("Basic ")
                    );
                    if req.method() == Method::DELETE {
                        deleted.fetch_add(1, Ordering::SeqCst);
                        return StatusCode::NO_CONTENT;
                    }
                    assert_eq!(req.method(), Method::PUT);
                    uploaded.lock().unwrap().push(req.uri().to_string());
                    assert_eq!(
                        to_bytes(req.into_body(), 1024).await.unwrap().as_ref(),
                        b"audio"
                    );
                    StatusCode::CREATED
                }
            }),
        ))
        .await;
        let (result,result_task)=serve(Router::new().route("/result",get(|req:Request|async move {assert!(req.headers().get("Authorization").is_none());Json(json!({"transcripts":[{"text":"recognized","sentences":[{"text":"do not duplicate"}]}]}))}))).await;
        let result_url = format!("{result}/result");
        let (url,task)=serve(Router::new().route("/services/audio/asr/transcription",post(move |req:Request|async move {
            assert_eq!(req.headers()["X-DashScope-Async"],"enable");
            assert!(!req.headers().contains_key("X-DashScope-OssResourceResolve"));
            let v:Value=serde_json::from_slice(&to_bytes(req.into_body(),1<<20).await.unwrap()).unwrap();
            assert_eq!(v["model"], model);
            if qwen_file {
                assert!(v["input"]["file_url"].as_str().unwrap().contains("/dav/"));
                assert!(v["input"].get("file_urls").is_none());
                assert_eq!(v["parameters"], json!({"enable_itn":itn,"channel_id":[0],"language":"en","corpus":{"text":"names"}}));
            } else {
                assert!(v["input"]["file_urls"][0].as_str().unwrap().contains("/dav/"));
                assert!(v["parameters"].get("enable_itn").is_none());
            }
            Json(json!({"output":{"task_id":"task"}}))
        })).route("/tasks/task",get(move ||{let url=result_url.clone();async move {Json(if !success {json!({"output":{"task_status":"FAILED","message":"failure"}})} else if qwen_file {json!({"output":{"task_status":"SUCCEEDED","result":{"transcription_url":url}}})} else {json!({"output":{"task_status":"SUCCEEDED","results":[{"subtask_status":"SUCCEEDED","transcription_url":url}]}})})}}))).await;
        let mut cfg = config();
        cfg.dashscope_base_url = url;
        cfg.webdav_url = format!("{files}/dav");
        cfg.webdav_credentials = "user@pass@word".into();
        cfg.asr_retry_max_attempts = 1;
        let dir = tempfile::tempdir().unwrap();
        let path = dir.path().join("audio.ogg");
        std::fs::write(&path, b"audio").unwrap();
        let result = DashScope::new(Arc::new(cfg))
            .unwrap()
            .transcribe(
                &path,
                model,
                &Options {
                    enable_itn: itn,
                    language: "en".into(),
                    prompt: "names".into(),
                    ..Default::default()
                },
            )
            .await;
        if success {
            assert_eq!(result.unwrap(), "recognized")
        } else {
            assert!(result.is_err())
        }
        tokio::time::timeout(Duration::from_secs(3), async {
            while deleted.load(Ordering::SeqCst) == 0 {
                tokio::task::yield_now().await
            }
        })
        .await
        .unwrap();
        assert_eq!(uploads.lock().unwrap().len(), 1);
        task.abort();
        files_task.abort();
        result_task.abort();
    }
}

#[tokio::test]
async fn oss_policy_and_resolution_header() {
    let (upload, upload_task) = serve(Router::new().route(
        "/",
        post(|req: Request| async move {
            assert!(!req.headers().contains_key("Authorization"));
            let bytes = to_bytes(req.into_body(), 1 << 20).await.unwrap();
            let body = String::from_utf8_lossy(&bytes);
            assert!(body.contains("name=\"OSSAccessKeyId\""));
            assert!(body.contains("name=\"file\""));
            StatusCode::OK
        }),
    ))
    .await;
    let (result, result_task) = serve(Router::new().route(
        "/",
        get(|| async { Json(json!({"transcripts":[{"text":"OSS"}]})) }),
    ))
    .await;
    let (url,task)=serve(Router::new().route("/uploads",get(move ||{let host=upload.clone();async move {Json(json!({"data":{"upload_host":host,"upload_dir":"prefix","oss_access_key_id":"id","signature":"signature","policy":"policy"}}))}}))
        .route("/services/aigc/multimodal-generation/generation",post(|req:Request|async move {
            assert_eq!(req.headers()["X-DashScope-OssResourceResolve"],"enable");
            let v:Value=serde_json::from_slice(&to_bytes(req.into_body(),1<<20).await.unwrap()).unwrap();
            let content=&v["input"]["messages"][0]["content"][0];
            let url=content["audio"].as_str().or_else(||content["input_audio"]["data"].as_str()).unwrap();
            assert!(url.starts_with("oss://prefix/"));
            Json(json!({"output":{"text":"OSS"}}))
        }))
        .route("/services/audio/asr/transcription",post(|req:Request|async move {
            assert_eq!(req.headers()["X-DashScope-OssResourceResolve"],"enable");
            let v:Value=serde_json::from_slice(&to_bytes(req.into_body(),1<<20).await.unwrap()).unwrap();
            let single=v["model"]=="qwen3-asr-flash-filetrans";
            let url=if single {&v["input"]["file_url"]} else {&v["input"]["file_urls"][0]};
            assert!(url.as_str().unwrap().starts_with("oss://prefix/"));
            Json(json!({"task_id":if single {"single"} else {"oss"}}))
        }))
        .route("/tasks/{task}",get(move |axum::extract::Path(task):axum::extract::Path<String>|{let url=result.clone();async move {Json(if task=="single" {json!({"output":{"task_status":"SUCCEEDED","result":{"transcription_url":url}}})} else {json!({"output":{"task_status":"SUCCEEDED","results":[{"transcription_url":url}]}})})}}))).await;
    let mut cfg = config();
    cfg.dashscope_base_url = url;
    cfg.base64_first = false;
    let dir = tempfile::tempdir().unwrap();
    let path = dir.path().join("a.ogg");
    std::fs::write(&path, b"audio").unwrap();
    let client = DashScope::new(Arc::new(cfg)).unwrap();
    for model in [
        "qwen3-asr-flash",
        "qwen-audio-3.0-asr-flash",
        "fun-asr-flash",
        "qwen3-asr-flash-filetrans",
        "qwen-audio-3.0-asr-flash-filetrans",
        "fun-asr",
        "paraformer-v2",
    ] {
        assert_eq!(
            client
                .transcribe(&path, model, &Options::default())
                .await
                .unwrap(),
            "OSS"
        );
    }
    task.abort();
    upload_task.abort();
    result_task.abort();
}
