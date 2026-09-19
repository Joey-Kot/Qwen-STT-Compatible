// Copyright (C) 2026 Joey Kot
// SPDX-License-Identifier: GPL-3.0-or-later
use crate::{
    audio,
    bridge::{Bridge, OrderedItems},
    config::{Config, boolean},
    dashscope::{DashScope, Options},
    models::{self, Mode, Protocol},
    realtime::{self, Event, Settings},
};
use anyhow::{Result, bail, ensure};
use axum::{
    Json, Router,
    extract::{DefaultBodyLimit, Multipart, Query, Request, State, WebSocketUpgrade},
    http::{Method, StatusCode, header},
    middleware::{self, Next},
    response::{
        IntoResponse, Response, Sse,
        sse::{Event as SseEvent, KeepAlive},
    },
    routing::any,
};
use futures_util::{Stream, StreamExt, TryStreamExt, stream};
use serde_json::json;
use std::{
    collections::HashMap, convert::Infallible, path::PathBuf, pin::Pin, process::Stdio, sync::Arc,
    time::Duration,
};
use subtle::ConstantTimeEq;
use tempfile::TempDir;
use tokio::{
    io::{AsyncReadExt, AsyncWriteExt},
    sync::{OwnedSemaphorePermit, Semaphore},
    time::{Instant, sleep_until},
};

pub struct App {
    pub cfg: Arc<Config>,
    pub client: DashScope,
    pub api: Semaphore,
    pub realtime: Arc<Semaphore>,
}
impl App {
    pub fn new(cfg: Config) -> Result<Arc<Self>> {
        let cfg = Arc::new(cfg);
        Ok(Arc::new(Self {
            client: DashScope::new(cfg.clone())?,
            api: Semaphore::new(cfg.api_concurrency.max(1)),
            realtime: Arc::new(Semaphore::new(cfg.realtime_concurrency)),
            cfg,
        }))
    }
}
#[derive(Debug)]
pub struct ApiError(pub StatusCode, pub String, pub &'static str);
impl ApiError {
    fn bad(message: impl ToString) -> Self {
        Self(
            StatusCode::BAD_REQUEST,
            message.to_string(),
            "invalid_request_error",
        )
    }
    fn server(message: impl ToString) -> Self {
        Self(StatusCode::BAD_GATEWAY, message.to_string(), "server_error")
    }
}
impl From<anyhow::Error> for ApiError {
    fn from(e: anyhow::Error) -> Self {
        Self::server(e)
    }
}
impl IntoResponse for ApiError {
    fn into_response(self) -> Response {
        (
            self.0,
            Json(json!({"error":{"message":self.1,"type":self.2,"param":null,"code":null}})),
        )
            .into_response()
    }
}
pub fn router(app: Arc<App>) -> Router {
    let limit = app.cfg.max_upload_mb * (1 << 20) + (32 << 20);
    let routes = Router::new()
        .route("/health", any(|| async { Json(json!({"status":"ok"})) }))
        .route("/v1/models", any(list_models))
        .route("/v1/audio/transcriptions", any(transcriptions))
        .route("/v1/realtime", any(websocket))
        .fallback(|| async {
            ApiError(
                StatusCode::NOT_FOUND,
                "not found".into(),
                "invalid_request_error",
            )
        })
        .layer(DefaultBodyLimit::max(limit))
        .layer(middleware::from_fn_with_state(app.clone(), authorize))
        .layer(tower_http::cors::CorsLayer::permissive())
        .with_state(app);
    Router::new()
        .fallback_service(routes)
        .layer(middleware::from_fn(normalize_path))
}
async fn normalize_path(mut request: Request, next: Next) -> Response {
    let path = request.uri().path();
    if path.starts_with("/v1/") && path.ends_with('/') {
        let normalized = path.trim_end_matches('/');
        let target = match request.uri().query() {
            Some(q) => format!("{normalized}?{q}"),
            None => normalized.to_string(),
        };
        let mut parts = request.uri().clone().into_parts();
        parts.path_and_query = Some(target.parse().expect("normalized valid URI"));
        *request.uri_mut() = axum::http::Uri::from_parts(parts).expect("valid URI parts");
    }
    next.run(request).await
}
async fn authorize(State(app): State<Arc<App>>, request: Request, next: Next) -> Response {
    if request.method() == Method::OPTIONS {
        return StatusCode::NO_CONTENT.into_response();
    }
    if request.uri().path() == "/health" {
        return next.run(request).await;
    }
    let tokens = app.cfg.tokens();
    if tokens.is_empty() {
        return ApiError(
            StatusCode::INTERNAL_SERVER_ERROR,
            "API_TOKEN 未配置".into(),
            "server_error",
        )
        .into_response();
    }
    let valid = |candidate: &str| {
        tokens.iter().fold(false, |matched, expected| {
            matched | bool::from(expected.as_bytes().ct_eq(candidate.as_bytes()))
        })
    };
    let key = request
        .headers()
        .get("x-api-key")
        .and_then(|h| h.to_str().ok());
    let bearer = request
        .headers()
        .get(header::AUTHORIZATION)
        .and_then(|h| h.to_str().ok())
        .and_then(|s| s.strip_prefix("Bearer "));
    if key.is_some_and(valid) || bearer.is_some_and(valid) {
        return next.run(request).await;
    }
    let mut response = ApiError(
        StatusCode::UNAUTHORIZED,
        "Missing or invalid authentication token".into(),
        "authentication_error",
    )
    .into_response();
    response
        .headers_mut()
        .insert(header::WWW_AUTHENTICATE, "Bearer".parse().unwrap());
    response
}
fn method_error() -> ApiError {
    ApiError(
        StatusCode::METHOD_NOT_ALLOWED,
        "method not allowed".into(),
        "invalid_request_error",
    )
}
async fn list_models(method: Method) -> Response {
    if method != Method::GET {
        return method_error().into_response();
    }
    Json(json!({"object":"list","data":models::MODELS.iter().map(|m|json!({"id":m,"object":"model","owned_by":"dashscope"})).collect::<Vec<_>>()})).into_response()
}
fn slot(app: &App) -> std::result::Result<OwnedSemaphorePermit, ApiError> {
    app.realtime.clone().try_acquire_owned().map_err(|_| {
        ApiError(
            StatusCode::TOO_MANY_REQUESTS,
            "实时识别并发已达上限".into(),
            "rate_limit_error",
        )
    })
}
async fn websocket(
    State(app): State<Arc<App>>,
    method: Method,
    Query(query): Query<HashMap<String, String>>,
    headers: axum::http::HeaderMap,
    ws: std::result::Result<
        WebSocketUpgrade,
        axum::extract::ws::rejection::WebSocketUpgradeRejection,
    >,
) -> std::result::Result<Response, ApiError> {
    if method != Method::GET {
        return Err(method_error());
    }
    if query
        .get("intent")
        .is_some_and(|v| !v.is_empty() && v != "transcription")
    {
        return Err(ApiError::bad("only intent=transcription is supported"));
    }
    if let Some(origin) = headers.get(header::ORIGIN).and_then(|h| h.to_str().ok()) {
        let origin = url::Url::parse(origin).map_err(ApiError::bad)?;
        let host = headers
            .get(header::HOST)
            .and_then(|h| h.to_str().ok())
            .unwrap_or("");
        let authority = origin[url::Position::BeforeHost..url::Position::AfterPort].to_string();
        if !authority.eq_ignore_ascii_case(host) {
            return Err(ApiError(
                StatusCode::FORBIDDEN,
                "cross-origin WebSocket not allowed".into(),
                "invalid_request_error",
            ));
        }
    }
    let settings = Settings::new(query.get("model").cloned().unwrap_or_default());
    if !settings.model.is_empty() {
        settings.validate().map_err(ApiError::bad)?;
    }
    let permit = slot(&app)?;
    let ws = ws.map_err(ApiError::bad)?;
    Ok(ws
        .protocols(["realtime"])
        .max_message_size(1 << 20)
        .max_frame_size(1 << 20)
        .on_upgrade(move |socket| async move {
            let _permit = permit;
            Bridge::serve(app.cfg.clone(), socket, settings).await
        }))
}
async fn transcriptions(
    State(app): State<Arc<App>>,
    method: Method,
    multipart: std::result::Result<Multipart, axum::extract::multipart::MultipartRejection>,
) -> std::result::Result<Response, ApiError> {
    if method != Method::POST {
        return Err(method_error());
    }
    let mut multipart = multipart.map_err(ApiError::bad)?;
    let work = Arc::new(
        tempfile::Builder::new()
            .prefix("qwen-stt-")
            .tempdir()
            .map_err(ApiError::server)?,
    );
    let mut fields = HashMap::new();
    let mut input = None;
    while let Some(mut field) = multipart.next_field().await.map_err(ApiError::bad)? {
        let name = field.name().unwrap_or("").to_owned();
        if name == "file" && input.is_none() {
            let path = work.path().join(format!(
                "input_{}",
                safe_filename(field.file_name().unwrap_or("audio"))
            ));
            let mut file = tokio::fs::File::create(&path)
                .await
                .map_err(ApiError::server)?;
            let mut size = 0usize;
            while let Some(chunk) = field.chunk().await.map_err(ApiError::bad)? {
                size += chunk.len();
                if size > app.cfg.max_upload_mb * (1 << 20) {
                    return Err(ApiError::bad("文件超过上传大小限制"));
                }
                file.write_all(&chunk).await.map_err(ApiError::server)?;
            }
            file.flush().await.map_err(ApiError::server)?;
            input = Some(path);
        } else {
            let mut data = Vec::new();
            while let Some(chunk) = field.chunk().await.map_err(ApiError::bad)? {
                if data.len() + chunk.len() > 1 << 20 {
                    return Err(ApiError::bad("form field too large"));
                }
                data.extend_from_slice(&chunk);
            }
            fields
                .entry(name)
                .or_insert(String::from_utf8(data).map_err(ApiError::bad)?);
        }
    }
    let input = input.ok_or_else(|| ApiError::bad("未选择文件"))?;
    let value = |key: &str| fields.get(key).map(String::as_str).unwrap_or("");
    let model = value("model").trim().to_string();
    let route = models::route(&model).map_err(ApiError::bad)?;
    let stream = boolean(value("stream")).unwrap_or(false);
    let options = Options {
        language: normalize_language(value("language")),
        prompt: value("prompt").into(),
        enable_itn: boolean(value("enable_itn")).unwrap_or(app.cfg.enable_itn),
        rate: route.rate,
    };
    if route.mode == Mode::Realtime {
        if !value("response_format").is_empty() && value("response_format") != "json" {
            return Err(ApiError::bad(
                "realtime files only support response_format=json",
            ));
        }
        let permit = slot(&app)?;
        let mut settings = Settings::new(model);
        settings.language = options.language;
        settings.prompt = options.prompt;
        settings.silence_ms = 0;
        settings.vad = route.protocol == Protocol::Qwen;
        settings.validate().map_err(ApiError::bad)?;
        let mut output = realtime_file(app.cfg.clone(), input, work, settings, permit).await?;
        if stream {
            let first = match tokio::time::timeout(Duration::from_secs(15), output.next()).await {
                Ok(Some(piece)) => piece?,
                Ok(None) => return Err(ApiError::server("missing final result")),
                Err(_) => Piece::Heartbeat,
            };
            return Ok(sse(
                Box::pin(stream::once(async move { Ok(first) }).chain(output)),
                false,
            ));
        }
        let mut result = None;
        while let Some(event) = output.next().await {
            match event? {
                Piece::Done(text) => result = Some(text),
                Piece::Delta(_) | Piece::Heartbeat => {}
            }
        }
        return result
            .map(|text| Json(json!({"status":"success","text":text})).into_response())
            .ok_or_else(|| ApiError::server("missing final result"));
    }
    let max_bytes = crate::dashscope::file_limit(route.mode, app.cfg.base64_first);
    let segments = audio::prepare(
        &app.cfg,
        input,
        work.clone(),
        route.rate,
        stream || app.cfg.skip_trim,
        max_bytes,
    )
    .await?;
    let text = recognize(app.clone(), segments, model, options).await?;
    if stream {
        let chunks = split_text(&text, 240);
        let output = stream::iter(
            chunks
                .into_iter()
                .map(|s| Ok(Piece::Delta(s)))
                .chain(std::iter::once(Ok(Piece::Done(text)))),
        );
        Ok(sse(Box::pin(output), true))
    } else {
        Ok(Json(json!({"status":"success","text":text})).into_response())
    }
}
pub async fn recognize(
    app: Arc<App>,
    segments: Vec<PathBuf>,
    model: String,
    options: Options,
) -> Result<String> {
    let results: Vec<String> = stream::iter(segments)
        .map(|path| {
            let app = app.clone();
            let options = options.clone();
            let model = model.clone();
            async move {
                let _permit = app.api.acquire().await?;
                app.client.transcribe(&path, &model, &options).await
            }
        })
        .buffered(app.cfg.api_concurrency.max(1))
        .try_collect()
        .await?;
    Ok(results.concat())
}
pub fn safe_filename(name: &str) -> String {
    let base = name.rsplit(['/', '\\']).next().unwrap_or("");
    let clean: String = base
        .chars()
        .filter(|c| c.is_ascii_alphanumeric() || "._-".contains(*c))
        .collect();
    if clean.is_empty() || clean == "." || clean == ".." {
        "unknown_file".into()
    } else {
        clean
    }
}
fn normalize_language(v: &str) -> String {
    let v = v.trim().to_ascii_lowercase();
    if (2..=3).contains(&v.len()) && v.bytes().all(|c| c.is_ascii_lowercase()) {
        v
    } else {
        String::new()
    }
}
fn split_text(text: &str, size: usize) -> Vec<String> {
    let chars: Vec<_> = text.chars().collect();
    if chars.is_empty() {
        return vec![String::new()];
    }
    chars.chunks(size).map(|c| c.iter().collect()).collect()
}
enum Piece {
    Delta(String),
    Done(String),
    Heartbeat,
}
type Output = Pin<Box<dyn Stream<Item = Result<Piece>> + Send>>;
fn sse(mut output: Output, pseudo: bool) -> Response {
    let stream = async_stream::stream! {
        while let Some(piece)=output.next().await {
            if matches!(piece, Ok(Piece::Heartbeat)) {
                yield Ok::<_,Infallible>(SseEvent::default().comment("keepalive"));
                continue;
            }
            let (value,done,error)=match piece {Ok(Piece::Delta(delta))=>(json!({"type":"transcript.text.delta","delta":delta}),false,false),Ok(Piece::Done(text))=>(json!({"type":"transcript.text.done","text":text}),true,false),Ok(Piece::Heartbeat)=>unreachable!(),Err(e)=>(json!({"type":"error","error":{"type":"server_error","message":e.to_string()}}),false,true)};
            yield Ok::<_,Infallible>(SseEvent::default().data(value.to_string()));
            if done {if pseudo {yield Ok(SseEvent::default().data("[DONE]"));}break}
            if error {break}
        }
    };
    let mut response = Sse::new(stream)
        .keep_alive(
            KeepAlive::new()
                .interval(Duration::from_secs(15))
                .text("keepalive"),
        )
        .into_response();
    response
        .headers_mut()
        .insert("X-Accel-Buffering", "no".parse().unwrap());
    response
}
async fn realtime_file(
    cfg: Arc<Config>,
    path: PathBuf,
    work: Arc<TempDir>,
    settings: Settings,
    permit: OwnedSemaphorePermit,
) -> Result<Output> {
    let rate = models::route(&settings.model)?.rate;
    let mut decoder = tokio::process::Command::new("ffmpeg")
        .args([
            "-nostdin",
            "-hide_banner",
            "-loglevel",
            "error",
            "-threads",
            "1",
            "-i",
        ])
        .arg(path)
        .args([
            "-map",
            "0:a:0",
            "-vn",
            "-ac",
            "1",
            "-ar",
            &rate.to_string(),
            "-c:a",
            "pcm_s16le",
            "-threads",
            "1",
            "-f",
            "s16le",
            "pipe:1",
        ])
        .stdout(Stdio::piped())
        .stderr(Stdio::piped())
        .kill_on_drop(true)
        .spawn()?;
    let mut pcm = decoder.stdout.take().unwrap();
    let mut stderr = decoder.stderr.take().unwrap();
    let diagnostics = tokio::spawn(async move {
        let mut output = Vec::new();
        let mut buffer = [0u8; 1024];
        loop {
            match stderr.read(&mut buffer).await {
                Ok(0) | Err(_) => break,
                Ok(n) => {
                    let keep = n.min(4096 - output.len());
                    output.extend_from_slice(&buffer[..keep]);
                }
            }
        }
        output
    });
    let mut first = vec![0u8; rate as usize / 10 * 2];
    let n = read_pcm(&mut pcm, &mut first).await?;
    first.truncate(n);
    if first.is_empty() {
        let status = decoder.wait().await?;
        let diag = diagnostics.await?;
        bail!(
            "empty or invalid audio ({status}): {}",
            String::from_utf8_lossy(&diag)
        );
    }
    let mut session = realtime::start(cfg, settings).await?;
    let mut events = std::mem::replace(&mut session.events, tokio::sync::mpsc::channel(1).1);
    Ok(Box::pin(async_stream::try_stream! {
        let _work=work;let _permit=permit;
        let sender=async {
            let start=Instant::now();let mut samples=0u64;let mut data=first;
            loop {
                ensure!(data.len().is_multiple_of(2),"decoded PCM is not sample-aligned");
                samples+=data.len() as u64/2;session.audio(data).await?;
                sleep_until(start+Duration::from_secs_f64(samples as f64/rate as f64)).await;
                let mut next=vec![0;rate as usize/10*2];let n=read_pcm(&mut pcm,&mut next).await?;
                if n==0 {break}next.truncate(n);data=next;
            }
            let status=decoder.wait().await?;let diag=diagnostics.await?;
            ensure!(status.success(),"audio decoding failed: {}",String::from_utf8_lossy(&diag));session.finish().await
        };
        tokio::pin!(sender);
        let mut sent=false;let mut text=String::new();let mut items=OrderedItems::default();
        loop {
            let next=tokio::select! { result=&mut sender, if !sent=>result.map(|_|None), event=events.recv()=>event.map(Some).ok_or_else(||anyhow::anyhow!("upstream closed without completion")) };
            let Some(event)=next? else {sent=true;continue};
            let delta=match event {
                Event::Sentence(s) if s.final_result=>s.text,
                Event::Item(i)=>items.accept(&i)?,
                Event::Error(e)=>Err(anyhow::anyhow!(e))?,
                Event::Finished=>{
                    if !items.is_empty() {Err(anyhow::anyhow!("unfinished transcription items"))?;}
                    if !sent {sender.as_mut().await?;}
                    yield Piece::Done(text);break
                },
                _=>String::new()
            };
            if !delta.is_empty() {if text.len()+delta.len()>8<<20 {Err(anyhow::anyhow!("transcript exceeds 8 MiB"))?;}text.push_str(&delta);yield Piece::Delta(delta);}
        }
    }))
}
async fn read_pcm(
    reader: &mut (impl tokio::io::AsyncRead + Unpin),
    buffer: &mut [u8],
) -> Result<usize> {
    let mut n = 0;
    while n < buffer.len() {
        let read = reader.read(&mut buffer[n..]).await?;
        if read == 0 {
            break;
        }
        n += read;
    }
    Ok(n)
}

#[cfg(test)]
mod tests {
    use super::*;
    use axum::body::Body;
    use clap::Parser;
    use http_body_util::BodyExt;
    use serde_json::Value;
    use tower::ServiceExt;
    #[tokio::test]
    async fn auth_models_health_and_errors() {
        let cfg = Config::try_parse_from(["test", "--api-token=test-key"]).unwrap();
        let app = router(App::new(cfg).unwrap());
        for (uri, token, status) in [
            ("/health", false, 200),
            ("/v1/models", false, 401),
            ("/v1/models", true, 200),
            ("/missing", true, 404),
        ] {
            let mut req = Request::builder().uri(uri);
            if token {
                req = req.header("Authorization", "Bearer test-key");
            }
            let response = app
                .clone()
                .oneshot(req.body(Body::empty()).unwrap())
                .await
                .unwrap();
            assert_eq!(response.status().as_u16(), status);
            let body = response.into_body().collect().await.unwrap().to_bytes();
            let _: Value = serde_json::from_slice(&body).unwrap();
        }
    }
    #[test]
    fn unicode_chunks_and_filenames() {
        assert_eq!(split_text("你好世界", 2), vec!["你好", "世界"]);
        assert_eq!(safe_filename("../../语音.wav"), ".wav");
        assert_eq!(normalize_language("zh-CN"), "");
    }
}
