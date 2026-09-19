// Copyright (C) 2026 Joey Kot
// SPDX-License-Identifier: GPL-3.0-or-later
use base64::{Engine, engine::general_purpose::STANDARD};
use clap::Parser;
use futures_util::{SinkExt, StreamExt};
use qwen_stt_compatible::{
    config::Config,
    httpapi::{App, router},
};
use serde_json::{Value, json};
use std::{sync::Arc, time::Duration};
use tokio::{
    net::{TcpListener, TcpStream},
    sync::mpsc,
    time::timeout,
};
use tokio_tungstenite::{
    MaybeTlsStream, WebSocketStream,
    tungstenite::{Message, client::IntoClientRequest},
};
type Ws = WebSocketStream<MaybeTlsStream<TcpStream>>;
struct Connection {
    output: mpsc::UnboundedSender<Value>,
    input: mpsc::UnboundedReceiver<Value>,
    id: String,
}
struct Harness {
    url: String,
    connections: mpsc::UnboundedReceiver<Connection>,
    app: Arc<App>,
    server: tokio::task::JoinHandle<()>,
    upstream: tokio::task::JoinHandle<()>,
}
impl Drop for Harness {
    fn drop(&mut self) {
        self.server.abort();
        self.upstream.abort();
    }
}
impl Harness {
    async fn new(qwen: bool) -> Self {
        let listener = TcpListener::bind("127.0.0.1:0").await.unwrap();
        let address = listener.local_addr().unwrap();
        let (tx, connections) = mpsc::unbounded_channel();
        let upstream = tokio::spawn(async move {
            loop {
                let (stream, _) = listener.accept().await.unwrap();
                let tx = tx.clone();
                tokio::spawn(async move {
                    let mut ws = tokio_tungstenite::accept_async(stream).await.unwrap();
                    let id = if qwen {
                        ws.send(Message::Text(
                            json!({"type":"session.created"}).to_string().into(),
                        ))
                        .await
                        .unwrap();
                        let update: Value = serde_json::from_str(
                            ws.next().await.unwrap().unwrap().to_text().unwrap(),
                        )
                        .unwrap();
                        assert_eq!(update["type"], "session.update");
                        assert_eq!(update["session"]["sample_rate"], 16000);
                        ws.send(Message::Text(
                            json!({"type":"session.updated"}).to_string().into(),
                        ))
                        .await
                        .unwrap();
                        String::new()
                    } else {
                        let run: Value = serde_json::from_str(
                            ws.next().await.unwrap().unwrap().to_text().unwrap(),
                        )
                        .unwrap();
                        assert_eq!(run["header"]["action"], "run-task");
                        let id = run["header"]["task_id"].as_str().unwrap().to_owned();
                        ws.send(Message::Text(
                            json!({"header":{"event":"task-started","task_id":id}})
                                .to_string()
                                .into(),
                        ))
                        .await
                        .unwrap();
                        id
                    };
                    let (output, mut out) = mpsc::unbounded_channel::<Value>();
                    let (input_tx, input) = mpsc::unbounded_channel();
                    if tx.send(Connection { output, input, id }).is_err() {
                        return;
                    }
                    loop {
                        tokio::select! {message=ws.next()=>{let Some(Ok(message))=message else {break};let value=match message {Message::Text(t)=>serde_json::from_str(&t).unwrap(),Message::Binary(b)=>json!({"type":"audio","bytes":b.len()}),Message::Close(_)=>break,_=>continue};if input_tx.send(value).is_err(){break}},Some(value)=out.recv()=>{if ws.send(Message::Text(value.to_string().into())).await.is_err(){break}}}
                    }
                });
            }
        });
        let mut cfg = Config::try_parse_from([
            "test",
            "--api-token=key",
            "--dashscope-api-key=upstream-key",
            "--realtime-finish-timeout=1s",
            "--realtime-concurrency=2",
        ])
        .unwrap();
        cfg.dashscope_ws_url = format!("ws://{address}");
        cfg.dashscope_qwen_ws_url = format!("ws://{address}");
        let app = App::new(cfg).unwrap();
        let listener = TcpListener::bind("127.0.0.1:0").await.unwrap();
        let url = format!("http://{}", listener.local_addr().unwrap());
        let router = router(app.clone());
        let server = tokio::spawn(async move { axum::serve(listener, router).await.unwrap() });
        Self {
            url,
            connections,
            app,
            server,
            upstream,
        }
    }
    async fn connect(&self, model: &str) -> Ws {
        let mut req = format!(
            "{}/v1/realtime?model={model}",
            self.url.replace("http:", "ws:")
        )
        .into_client_request()
        .unwrap();
        req.headers_mut()
            .insert("Authorization", "Bearer key".parse().unwrap());
        let (mut ws, _) = tokio_tungstenite::connect_async(req).await.unwrap();
        let e = next(&mut ws, "session.created").await;
        assert_eq!(e["session"]["audio"]["input"]["format"]["rate"], 24000);
        ws
    }
    async fn connection(&mut self) -> Connection {
        timeout(Duration::from_secs(3), self.connections.recv())
            .await
            .unwrap()
            .unwrap()
    }
}
async fn send(ws: &mut Ws, v: Value) {
    ws.send(Message::Text(v.to_string().into())).await.unwrap();
}
async fn next(ws: &mut Ws, kind: &str) -> Value {
    timeout(Duration::from_secs(4), async {
        loop {
            let message = ws.next().await.unwrap().unwrap();
            if let Message::Text(t) = message {
                let v: Value = serde_json::from_str(&t).unwrap();
                if v["type"] == kind {
                    return v;
                }
                assert_ne!(v["type"], "error", "unexpected error: {v}");
            }
        }
    })
    .await
    .unwrap()
}
async fn append(ws: &mut Ws) {
    send(
        ws,
        json!({"type":"input_audio_buffer.append","audio":STANDARD.encode(vec![0u8;4800])}),
    )
    .await;
}
async fn finished_input(c: &mut Connection, qwen: bool) {
    timeout(Duration::from_secs(3), async {
        while let Some(v) = c.input.recv().await {
            if (qwen && v["type"] == "session.finish")
                || (!qwen && v["header"]["action"] == "finish-task")
            {
                return;
            }
        }
        panic!("upstream closed before finish")
    })
    .await
    .unwrap();
}
fn qwen_result(c: &Connection, id: &str, text: &str, finish: bool) {
    for v in [
        json!({"type":"input_audio_buffer.committed","item_id":id}),
        json!({"type":"conversation.item.input_audio_transcription.text","item_id":id,"text":text}),
        json!({"type":"conversation.item.input_audio_transcription.completed","item_id":id,"transcript":text}),
    ] {
        c.output.send(v).unwrap();
    }
    if finish {
        c.output.send(json!({"type":"session.finished"})).unwrap();
    }
}
fn sentence(c: &Connection, begin: i64, end: Option<i64>, text: &str, final_result: bool) {
    c.output.send(json!({"header":{"task_id":c.id,"event":"result-generated"},"payload":{"output":{"sentence":{"sentence_id":begin+1,"begin_time":begin,"end_time":end,"text":text,"sentence_end":final_result}}}})).unwrap();
}

#[tokio::test]
async fn qwen_manual_turns_complete_out_of_order() {
    let mut h = Harness::new(true).await;
    let mut ws = h.connect("qwen3-asr-flash-realtime").await;
    append(&mut ws).await;
    let mut first = h.connection().await;
    send(&mut ws, json!({"type":"input_audio_buffer.commit"})).await;
    let a = next(&mut ws, "input_audio_buffer.committed").await;
    finished_input(&mut first, true).await;
    append(&mut ws).await;
    let mut second = h.connection().await;
    send(&mut ws, json!({"type":"input_audio_buffer.commit"})).await;
    let b = next(&mut ws, "input_audio_buffer.committed").await;
    assert_eq!(b["previous_item_id"], a["item_id"]);
    finished_input(&mut second, true).await;
    qwen_result(&second, "b", "second", true);
    let done = next(
        &mut ws,
        "conversation.item.input_audio_transcription.completed",
    )
    .await;
    assert_eq!(done["item_id"], b["item_id"]);
    assert_eq!(done["transcript"], "second");
    qwen_result(&first, "a", "first", true);
    let done = next(
        &mut ws,
        "conversation.item.input_audio_transcription.completed",
    )
    .await;
    assert_eq!(done["item_id"], a["item_id"]);
    ws.close(None).await.unwrap();
}

#[tokio::test]
async fn clear_cancels_uncommitted_audio_and_disconnect_releases_slot() {
    let mut h = Harness::new(false).await;
    let mut ws = h.connect("fun-asr-realtime").await;
    append(&mut ws).await;
    let mut old = h.connection().await;
    send(&mut ws, json!({"type":"input_audio_buffer.clear"})).await;
    next(&mut ws, "input_audio_buffer.cleared").await;
    timeout(Duration::from_secs(3), async {
        while old.input.recv().await.is_some() {}
    })
    .await
    .unwrap();
    append(&mut ws).await;
    let mut current = h.connection().await;
    send(&mut ws, json!({"type":"input_audio_buffer.commit"})).await;
    let committed = next(&mut ws, "input_audio_buffer.committed").await;
    assert!(committed["previous_item_id"].is_null());
    finished_input(&mut current, false).await;
    sentence(&current, 0, Some(100), "new", true);
    current
        .output
        .send(json!({"header":{"task_id":current.id,"event":"task-finished"}}))
        .unwrap();
    let done = next(
        &mut ws,
        "conversation.item.input_audio_transcription.completed",
    )
    .await;
    assert_eq!(done["transcript"], "new");
    ws.close(None).await.unwrap();
    timeout(Duration::from_secs(3), async {
        while h.app.realtime.available_permits() != 2 {
            tokio::task::yield_now().await
        }
    })
    .await
    .unwrap();
}

#[tokio::test]
async fn paraformer_vad_boundaries_and_delayed_final_preserve_tail() {
    let mut h = Harness::new(false).await;
    let mut ws = h.connect("paraformer-realtime-v2").await;
    send(&mut ws,json!({"type":"session.update","session":{"audio":{"input":{"turn_detection":{"type":"server_vad"}}}}})).await;
    next(&mut ws, "session.updated").await;
    append(&mut ws).await;
    append(&mut ws).await;
    let current = h.connection().await;
    sentence(&current, 0, None, "draft", false);
    next(&mut ws, "input_audio_buffer.speech_started").await;
    sentence(&current, 0, Some(100), "first", true);
    let first = next(
        &mut ws,
        "conversation.item.input_audio_transcription.completed",
    )
    .await;
    assert_eq!(first["transcript"], "first");
    send(&mut ws, json!({"type":"input_audio_buffer.commit"})).await;
    let tail = next(&mut ws, "input_audio_buffer.committed").await;
    assert_eq!(tail["previous_item_id"], first["item_id"]);
    sentence(&current, 100, Some(200), "tail", true);
    current
        .output
        .send(json!({"header":{"task_id":current.id,"event":"task-finished"}}))
        .unwrap();
    assert_eq!(
        next(
            &mut ws,
            "conversation.item.input_audio_transcription.completed"
        )
        .await["transcript"],
        "tail"
    );
}

#[tokio::test]
async fn qwen_clear_preserves_committed_item_and_item_failure_is_not_fatal() {
    let mut h = Harness::new(true).await;
    let mut ws = h.connect("qwen3-asr-flash-realtime").await;
    send(&mut ws,json!({"type":"session.update","session":{"audio":{"input":{"turn_detection":{"type":"server_vad"}}}}})).await;
    next(&mut ws, "session.updated").await;
    append(&mut ws).await;
    let mut c = h.connection().await;
    c.output
        .send(json!({"type":"input_audio_buffer.speech_started","item_id":"a","audio_start_ms":0}))
        .unwrap();
    c.output
        .send(json!({"type":"input_audio_buffer.committed","item_id":"a"}))
        .unwrap();
    let item = next(&mut ws, "input_audio_buffer.committed").await;
    send(&mut ws, json!({"type":"input_audio_buffer.clear"})).await;
    next(&mut ws, "input_audio_buffer.cleared").await;
    finished_input(&mut c, true).await;
    c.output.send(json!({"type":"conversation.item.input_audio_transcription.failed","item_id":"a","error":{"code":"test","message":"failure"}})).unwrap();
    let failed = next(
        &mut ws,
        "conversation.item.input_audio_transcription.failed",
    )
    .await;
    assert_eq!(failed["item_id"], item["item_id"]);
    c.output.send(json!({"type":"session.finished"})).unwrap();
    send(
        &mut ws,
        json!({"type":"session.update","session":{"audio":{"input":{"turn_detection":null}}}}),
    )
    .await;
    next(&mut ws, "session.updated").await;
}

#[tokio::test]
async fn qwen_vad_flush_orders_later_manual_commit() {
    let mut h = Harness::new(true).await;
    let mut ws = h.connect("qwen3-asr-flash-realtime").await;
    send(&mut ws,json!({"type":"session.update","session":{"audio":{"input":{"turn_detection":{"type":"server_vad"}}}}})).await;
    next(&mut ws, "session.updated").await;
    append(&mut ws).await;
    let mut a = h.connection().await;
    send(&mut ws, json!({"type":"input_audio_buffer.commit"})).await;
    finished_input(&mut a, true).await;
    send(
        &mut ws,
        json!({"type":"session.update","session":{"audio":{"input":{"turn_detection":null}}}}),
    )
    .await;
    next(&mut ws, "session.updated").await;
    append(&mut ws).await;
    let mut b = h.connection().await;
    send(&mut ws, json!({"type":"input_audio_buffer.commit"})).await;
    finished_input(&mut b, true).await;
    qwen_result(&b, "b", "later", true);
    a.output
        .send(json!({"type":"input_audio_buffer.speech_started","item_id":"a","audio_start_ms":0}))
        .unwrap();
    qwen_result(&a, "a", "earlier", true);
    let first = next(&mut ws, "input_audio_buffer.committed").await;
    let second = next(&mut ws, "input_audio_buffer.committed").await;
    assert_eq!(second["previous_item_id"], first["item_id"]);
    assert_eq!(
        next(
            &mut ws,
            "conversation.item.input_audio_transcription.completed"
        )
        .await["transcript"],
        "later"
    );
}

#[tokio::test]
async fn upstream_failure_and_finish_timeout_do_not_complete() {
    for fail in [true, false] {
        let mut h = Harness::new(false).await;
        let mut ws = h.connect("fun-asr-realtime").await;
        append(&mut ws).await;
        let mut c = h.connection().await;
        send(&mut ws, json!({"type":"input_audio_buffer.commit"})).await;
        next(&mut ws, "input_audio_buffer.committed").await;
        finished_input(&mut c, false).await;
        if fail {
            c.output.send(json!({"header":{"task_id":c.id,"event":"task-failed","error_message":"failure"}})).unwrap();
        }
        next(
            &mut ws,
            "conversation.item.input_audio_transcription.failed",
        )
        .await;
        let error = next(&mut ws, "error").await;
        assert_eq!(error["error"]["type"], "server_error");
    }
}

#[tokio::test]
async fn realtime_file_errors_before_and_after_sse_starts() {
    for late in [false, true] {
        let mut h = Harness::new(true).await;
        let response = tokio::spawn(
            reqwest::Client::new()
                .post(format!("{}/v1/audio/transcriptions", h.url))
                .bearer_auth("key")
                .multipart(
                    reqwest::multipart::Form::new()
                        .text("model", "qwen3-asr-flash-realtime")
                        .text("stream", "true")
                        .part(
                            "file",
                            reqwest::multipart::Part::bytes(
                                include_bytes!("fixtures/jfk.flac").to_vec(),
                            )
                            .file_name("test.flac"),
                        ),
                )
                .send(),
        );
        let c = h.connection().await;
        if late {
            c.output.send(json!({"type":"input_audio_buffer.speech_started","item_id":"a","audio_start_ms":0})).unwrap();
            c.output
                .send(json!({"type":"input_audio_buffer.committed","item_id":"a"}))
                .unwrap();
            c.output.send(json!({"type":"conversation.item.input_audio_transcription.text","item_id":"a","text":"early"})).unwrap();
            let response = timeout(Duration::from_secs(3), response)
                .await
                .unwrap()
                .unwrap()
                .unwrap();
            assert_eq!(response.status(), 200);
            c.output
                .send(json!({"type":"error","error":{"message":"test failure"}}))
                .unwrap();
            let body = response.text().await.unwrap();
            assert!(body.contains("test failure"), "{body}");
            assert!(!body.contains("transcript.text.done"));
        } else {
            c.output
                .send(json!({"type":"error","error":{"message":"test failure"}}))
                .unwrap();
            let response = timeout(Duration::from_secs(3), response)
                .await
                .unwrap()
                .unwrap()
                .unwrap();
            assert_eq!(response.status(), 502);
            let body: Value = response.json().await.unwrap();
            assert!(
                body["error"]["message"]
                    .as_str()
                    .unwrap()
                    .contains("test failure"),
                "unexpected error response: {body}"
            );
        }
    }
}

#[tokio::test]
async fn realtime_file_streams_stable_prefix_before_upload_finishes() {
    let mut h = Harness::new(true).await;
    let mut wav = std::io::Cursor::new(Vec::new());
    {
        let mut writer = hound::WavWriter::new(
            &mut wav,
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
    }
    let client = reqwest::Client::new();
    let response = client
        .post(format!("{}/v1/audio/transcriptions", h.url))
        .bearer_auth("key")
        .multipart(
            reqwest::multipart::Form::new()
                .text("model", "qwen3-asr-flash-realtime")
                .text("stream", "true")
                .part(
                    "file",
                    reqwest::multipart::Part::bytes(wav.into_inner()).file_name("test.wav"),
                ),
        )
        .send();
    let response = tokio::spawn(response);
    let mut c = h.connection().await;
    c.output
        .send(json!({"type":"input_audio_buffer.speech_started","item_id":"a","audio_start_ms":0}))
        .unwrap();
    c.output
        .send(json!({"type":"input_audio_buffer.committed","item_id":"a"}))
        .unwrap();
    c.output.send(json!({"type":"conversation.item.input_audio_transcription.text","item_id":"a","text":"early"})).unwrap();
    let response = response.await.unwrap().unwrap();
    assert_eq!(response.status(), 200);
    let mut body = response.bytes_stream();
    let chunk = timeout(Duration::from_millis(500), body.next())
        .await
        .unwrap()
        .unwrap()
        .unwrap();
    assert!(String::from_utf8_lossy(&chunk).contains("early"));
    finished_input(&mut c, true).await;
    c.output.send(json!({"type":"conversation.item.input_audio_transcription.completed","item_id":"a","transcript":"early final"})).unwrap();
    c.output.send(json!({"type":"session.finished"})).unwrap();
    let mut tail = String::new();
    while let Some(chunk) = body.next().await {
        tail.push_str(&String::from_utf8_lossy(&chunk.unwrap()));
    }
    assert!(tail.contains("transcript.text.done"));
    assert!(tail.contains("early final"));
    assert!(!tail.contains("[DONE]"));
}
