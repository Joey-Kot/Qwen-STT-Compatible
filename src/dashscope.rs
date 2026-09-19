// Copyright (C) 2026 Joey Kot
// SPDX-License-Identifier: GPL-3.0-or-later
use crate::{
    config::Config,
    models::{self, Mode},
};
use anyhow::{Result, bail, ensure};
use base64::{Engine, engine::general_purpose::STANDARD};
use reqwest::{
    Client, Method, RequestBuilder,
    multipart::{Form, Part},
};
use serde_json::{Value, json};
use std::{path::Path, sync::Arc, time::Duration};

pub const BASE64_RAW_LIMIT: u64 = (10 << 20) / 4 * 3;
pub const URL_LIMIT: u64 = 2 << 30;
#[derive(Clone)]
pub struct DashScope {
    pub http: Client,
    pub cfg: Arc<Config>,
}
#[derive(Clone, Default)]
pub struct Options {
    pub language: String,
    pub prompt: String,
    pub enable_itn: bool,
    pub rate: u32,
}

pub fn http_client(timeout: Duration) -> Result<Client> {
    Ok(http_builder(timeout).build()?)
}
fn http_builder(timeout: Duration) -> reqwest::ClientBuilder {
    let mut builder = Client::builder()
        .user_agent("qwen-stt-compatible")
        .pool_idle_timeout(Duration::from_secs(90));
    if !timeout.is_zero() {
        builder = builder.timeout(timeout);
    }
    builder
}
impl DashScope {
    pub fn new(cfg: Arc<Config>) -> Result<Self> {
        Ok(Self {
            http: http_client(cfg.upstream_timeout)?,
            cfg,
        })
    }
    fn endpoint(&self, path: &str) -> String {
        format!(
            "{}/{}",
            self.cfg.dashscope_base_url.trim_end_matches('/'),
            path
        )
    }
    fn request(&self, method: Method, path: &str) -> RequestBuilder {
        self.http
            .request(method, self.endpoint(path))
            .bearer_auth(self.cfg.dashscope_api_key.trim())
            .header("Accept", "application/json")
    }
    pub async fn transcribe(&self, path: &Path, model: &str, options: &Options) -> Result<String> {
        let route = models::route(model)?;
        ensure!(
            route.mode != Mode::Realtime,
            "realtime model requires WebSocket"
        );
        ensure!(
            !self.cfg.dashscope_api_key.trim().is_empty(),
            "DASHSCOPE_API_KEY 未配置"
        );
        let size = tokio::fs::metadata(path).await?.len();
        ensure!(
            size <= if route.mode == Mode::Http {
                BASE64_RAW_LIMIT
            } else {
                URL_LIMIT
            },
            "audio exceeds upstream size limit"
        );
        let mut delay = self.cfg.asr_retry_initial_delay;
        for attempt in 0..self.cfg.asr_retry_max_attempts.max(1) {
            let result = if route.mode == Mode::Http {
                self.flash(path, model, options).await
            } else {
                self.asynchronous(path, model, options).await
            };
            match result {
                Ok(text) => return Ok(text),
                Err(e) if attempt + 1 >= self.cfg.asr_retry_max_attempts => return Err(e),
                Err(e) => tracing::warn!(attempt=attempt+1,error=%e,"retrying transcription"),
            }
            tokio::time::sleep(delay.min(self.cfg.asr_retry_max_delay)).await;
            delay = Duration::from_secs_f64(
                (delay.as_secs_f64() * self.cfg.asr_retry_factor)
                    .min(self.cfg.asr_retry_max_delay.as_secs_f64()),
            );
        }
        unreachable!()
    }
    async fn flash(&self, path: &Path, model: &str, o: &Options) -> Result<String> {
        let bytes = tokio::fs::read(path).await?;
        ensure!(
            bytes.len() as u64 <= BASE64_RAW_LIMIT,
            "Base64 audio exceeds 10 MiB"
        );
        let data = format!(
            "data:{};base64,{}",
            content_type(path),
            STANDARD.encode(bytes)
        );
        let qwen = model.to_ascii_lowercase().starts_with("qwen3-asr-flash");
        let mut messages = Vec::new();
        if !o.prompt.trim().is_empty() {
            messages.push(if qwen {
                json!({"role":"system","content":[{"text":o.prompt.trim()}]})
            } else {
                json!({"role":"user","content":[{"type":"input_text","text":o.prompt.trim()}]})
            });
        }
        messages.push(if qwen {
            json!({"role":"user","content":[{"audio":data}]})
        } else {
            json!({"role":"user","content":[{"type":"input_audio","input_audio":{"data":data}}]})
        });
        let mut parameters = if qwen {
            json!({"result_format":"message","asr_options":{"enable_itn":o.enable_itn}})
        } else {
            json!({"format":audio_format(path),"sample_rate":if o.rate==0 {"16000".to_string()} else {o.rate.to_string()}})
        };
        if !o.language.is_empty() {
            if qwen {
                parameters["asr_options"]["language"] = json!(o.language);
            } else {
                parameters["language_hints"] = json!([o.language]);
            }
        }
        let mut request = self
            .request(
                Method::POST,
                "services/aigc/multimodal-generation/generation",
            )
            .json(&json!({"model":model,"input":{"messages":messages},"parameters":parameters}));
        if !qwen {
            request = request.header("X-DashScope-SSE", "disable");
        }
        let response = json_response(request).await?;
        let text = extract_flash(&response["output"]);
        Ok(nonempty(text))
    }
    async fn asynchronous(&self, path: &Path, model: &str, o: &Options) -> Result<String> {
        let qwen_file = model
            .trim()
            .to_ascii_lowercase()
            .starts_with("qwen3-asr-flash-filetrans");
        ensure!(
            !qwen_file
                || (!self.cfg.webdav_url.is_empty() && !self.cfg.webdav_credentials.is_empty()),
            "qwen3-asr-flash-filetrans requires a public audio URL; configure WebDAV upload"
        );
        let (url, cleanup) = self.upload(path, model).await?;
        let _cleanup = cleanup;
        let key = model.trim().to_ascii_lowercase();
        let supports_context =
            key.starts_with("fun-asr") || key.starts_with("qwen-audio-3.0-asr-flash-filetrans");
        let mut input = if qwen_file {
            json!({"file_url":url})
        } else {
            json!({"file_urls":[url]})
        };
        if supports_context && !o.prompt.trim().is_empty() {
            input["context"] =
                json!([{"role":"user","content":[{"type":"input_text","text":o.prompt.trim()}]}]);
        }
        let mut parameters = json!({});
        if qwen_file {
            parameters["enable_itn"] = json!(o.enable_itn);
            parameters["channel_id"] = json!([0]);
            if !o.language.is_empty() {
                parameters["language"] = json!(o.language);
            }
            if !o.prompt.trim().is_empty() {
                parameters["corpus"] = json!({"text":o.prompt.trim()});
            }
        }
        if !o.language.is_empty()
            && (supports_context || key == "paraformer-v2" || key.starts_with("paraformer-v2-"))
        {
            parameters["language_hints"] = json!([o.language]);
        }
        let mut req = self
            .request(Method::POST, "services/audio/asr/transcription")
            .header("X-DashScope-Async", "enable")
            .json(&json!({"model":model,"input":input,"parameters":parameters}));
        if url.starts_with("oss://") {
            req = req.header("X-DashScope-OssResourceResolve", "enable");
        }
        let response = json_response(req).await?;
        let task = response["output"]["task_id"]
            .as_str()
            .or_else(|| response["task_id"].as_str())
            .filter(|s| !s.is_empty())
            .ok_or_else(|| anyhow::anyhow!("missing task_id"))?;
        let mut endpoint = url::Url::parse(&self.endpoint("tasks/"))?;
        endpoint
            .path_segments_mut()
            .map_err(|_| anyhow::anyhow!("invalid tasks URL"))?
            .pop_if_empty()
            .push(task);
        let mut step = 0;
        let done = loop {
            let v = json_response(
                self.http
                    .get(endpoint.clone())
                    .bearer_auth(self.cfg.dashscope_api_key.trim()),
            )
            .await?;
            match v["output"]["task_status"].as_str().unwrap_or("") {
                "SUCCEEDED" => break v,
                "FAILED" | "CANCELED" | "UNKNOWN" => {
                    bail!("DashScope task failed: {}", v["output"])
                }
                _ => {}
            }
            tokio::time::sleep(Duration::from_secs((1u64 << (step / 3).min(3)).min(5))).await;
            step += 1;
        };
        let results: Vec<&Value> = if qwen_file {
            ensure!(
                done["output"]["result"]["transcription_url"]
                    .as_str()
                    .is_some_and(|s| !s.is_empty()),
                "missing async transcription URL"
            );
            vec![&done["output"]["result"]]
        } else {
            done["output"]["results"]
                .as_array()
                .filter(|a| !a.is_empty())
                .ok_or_else(|| anyhow::anyhow!("empty async task results"))?
                .iter()
                .collect()
        };
        let mut text = String::new();
        for item in results {
            ensure!(
                item["subtask_status"]
                    .as_str()
                    .is_none_or(|s| s.is_empty() || s == "SUCCEEDED"),
                "subtask failed: {item}"
            );
            if let Some(url) = item["transcription_url"].as_str().filter(|s| !s.is_empty()) {
                text.push_str(&extract_transcription(
                    &json_response(self.http.get(url)).await?,
                ));
            }
        }
        Ok(nonempty(text.trim().into()))
    }
    async fn upload(&self, path: &Path, model: &str) -> Result<(String, Option<Cleanup>)> {
        let file = tokio::fs::File::open(path).await?;
        let size = file.metadata().await?.len();
        let filename = path
            .file_name()
            .and_then(|s| s.to_str())
            .unwrap_or("audio.ogg");
        if !self.cfg.webdav_url.is_empty() && !self.cfg.webdav_credentials.is_empty() {
            let mut url =
                url::Url::parse(&format!("{}/", self.cfg.webdav_url.trim_end_matches('/')))?;
            url.path_segments_mut()
                .map_err(|_| anyhow::anyhow!("invalid WebDAV URL"))?
                .pop_if_empty()
                .push(&format!("{}-{filename}", crate::id()));
            let (u, p) = self
                .cfg
                .webdav_credentials
                .split_once('@')
                .ok_or_else(|| anyhow::anyhow!("invalid WebDAV credentials"))?;
            let cleanup = Cleanup {
                http: self.http.clone(),
                url: url.to_string(),
                user: u.into(),
                password: p.into(),
            };
            drain_response(
                self.http
                    .put(url.clone())
                    .basic_auth(u, Some(p))
                    .header("Content-Type", content_type(path))
                    .header("Content-Length", size)
                    .body(reqwest::Body::wrap_stream(
                        tokio_util::io::ReaderStream::new(file),
                    )),
            )
            .await?;
            return Ok((url.to_string(), Some(cleanup)));
        }
        let response = json_response(
            self.request(Method::GET, "uploads")
                .query(&[("action", "getPolicy"), ("model", model)]),
        )
        .await?;
        let policy = if response["output"]["upload_host"]
            .as_str()
            .is_some_and(|s| !s.is_empty())
        {
            &response["output"]
        } else {
            &response["data"]
        };
        let host = policy["upload_host"]
            .as_str()
            .filter(|s| !s.is_empty())
            .ok_or_else(|| anyhow::anyhow!("missing upload_host"))?;
        let dir = policy["upload_dir"]
            .as_str()
            .filter(|s| !s.is_empty())
            .ok_or_else(|| anyhow::anyhow!("missing upload_dir"))?;
        let key = format!("{}/{filename}", dir.trim_end_matches('/'));
        let mut form = Form::new()
            .text("key", key.clone())
            .text("success_action_status", "200")
            .text("x-oss-content-type", content_type(path));
        for (field, name) in [
            ("OSSAccessKeyId", "oss_access_key_id"),
            ("Signature", "signature"),
            ("policy", "policy"),
            ("x-oss-object-acl", "x_oss_object_acl"),
            ("x-oss-forbid-overwrite", "x_oss_forbid_overwrite"),
        ] {
            form = form.text(field, policy[name].as_str().unwrap_or("").to_owned());
        }
        let part = Part::stream_with_length(
            reqwest::Body::wrap_stream(tokio_util::io::ReaderStream::new(file)),
            size,
        )
        .file_name(filename.to_string());
        drain_response(self.http.post(host).multipart(form.part("file", part))).await?;
        Ok((format!("oss://{key}"), None))
    }
}
struct Cleanup {
    http: Client,
    url: String,
    user: String,
    password: String,
}
impl Drop for Cleanup {
    fn drop(&mut self) {
        let req = self
            .http
            .delete(&self.url)
            .basic_auth(&self.user, Some(&self.password))
            .timeout(Duration::from_secs(10));
        tokio::spawn(async move {
            if let Err(e) = drain_response(req).await {
                tracing::warn!(error=%e,"WebDAV temporary audio cleanup failed");
            }
        });
    }
}
pub async fn json_response(req: RequestBuilder) -> Result<Value> {
    let mut resp = send(req).await?;
    let mut data = Vec::new();
    while let Some(chunk) = resp.chunk().await? {
        ensure!(
            data.len() + chunk.len() <= 16 << 20,
            "upstream response exceeds 16 MiB"
        );
        data.extend_from_slice(&chunk);
    }
    let value: Value = serde_json::from_slice(&data)?;
    ensure!(
        value["code"].as_str().is_none_or(str::is_empty),
        "{}: {}",
        value["code"],
        value["message"]
    );
    Ok(value)
}
async fn send(req: RequestBuilder) -> Result<reqwest::Response> {
    let (client, request) = req.build_split();
    let request = request?;
    let mut safe = request.url().clone();
    let _ = safe.set_username("");
    let _ = safe.set_password(None);
    safe.set_query(None);
    safe.set_fragment(None);
    tracing::info!(method=%request.method(),url=%safe,"upstream request");
    let mut resp = client.execute(request).await.map_err(|e| e.without_url())?;
    tracing::info!(url=%safe,status=%resp.status(),protocol=?resp.version(),"upstream response");
    if !resp.status().is_success() {
        let status = resp.status();
        let mut data = Vec::new();
        while data.len() < 8192 {
            let Some(chunk) = resp.chunk().await? else {
                break;
            };
            data.extend_from_slice(&chunk[..chunk.len().min(8192 - data.len())]);
        }
        bail!("upstream HTTP {status}: {}", String::from_utf8_lossy(&data));
    }
    Ok(resp)
}
async fn drain_response(req: RequestBuilder) -> Result<()> {
    let mut resp = send(req).await?;
    while resp.chunk().await?.is_some() {}
    Ok(())
}
pub fn content_type(path: &Path) -> &'static str {
    match path
        .extension()
        .and_then(|s| s.to_str())
        .unwrap_or("")
        .to_ascii_lowercase()
        .as_str()
    {
        "ogg" | "oga" => "audio/ogg",
        "wav" => "audio/wav",
        "mp3" => "audio/mpeg",
        "flac" => "audio/flac",
        "m4a" | "mp4" => "audio/mp4",
        _ => "application/octet-stream",
    }
}
fn audio_format(path: &Path) -> String {
    match path
        .extension()
        .and_then(|s| s.to_str())
        .unwrap_or("wav")
        .to_ascii_lowercase()
        .as_str()
    {
        "oga" => "ogg".into(),
        "m4a" => "mp4".into(),
        s => s.into(),
    }
}
fn nonempty(text: String) -> String {
    if text.trim().is_empty() {
        "【该段音频转录出错】".into()
    } else {
        text
    }
}
fn collect(value: &Value, key: &str) -> String {
    match value {
        Value::Object(m) => {
            if let Some(a) = m.get(key).and_then(Value::as_array) {
                let text: String = a.iter().filter_map(|v| v["text"].as_str()).collect();
                return if text.is_empty() && key == "transcripts" {
                    a.iter().map(|v| collect(v, "sentences")).collect()
                } else {
                    text
                };
            }
            m.values()
                .map(|v| collect(v, key))
                .find(|s| !s.is_empty())
                .unwrap_or_default()
        }
        Value::Array(a) => a.iter().map(|v| collect(v, key)).collect(),
        _ => String::new(),
    }
}
fn deep_text(v: &Value) -> String {
    if let Some(s) = v["text"].as_str() {
        return s.into();
    }
    match v {
        Value::Object(m) => m
            .values()
            .map(deep_text)
            .find(|s| !s.is_empty())
            .unwrap_or_default(),
        Value::Array(a) => a
            .iter()
            .map(deep_text)
            .find(|s| !s.is_empty())
            .unwrap_or_default(),
        _ => String::new(),
    }
}
pub fn extract_transcription(v: &Value) -> String {
    for key in ["transcripts", "sentences"] {
        let text = collect(v, key);
        if !text.is_empty() {
            return text;
        }
    }
    deep_text(v)
}
fn extract_flash(v: &Value) -> String {
    if let Some(a) = v["choices"][0]["message"]["content"].as_array() {
        let text: String = a.iter().filter_map(|v| v["text"].as_str()).collect();
        if !text.is_empty() {
            return text;
        }
    }
    deep_text(v)
}

#[cfg(test)]
mod tests {
    use super::*;
    #[test]
    fn transcript_precedence() {
        let v = json!({"transcripts":[{"text":"完整结果","sentences":[{"text":"重复"}]}]});
        assert_eq!(extract_transcription(&v), "完整结果");
        assert_eq!(
            extract_transcription(
                &json!({"transcripts":[{"sentences":[{"text":"一"},{"text":"二"}]}]})
            ),
            "一二"
        );
    }

    #[tokio::test]
    async fn http2_reuses_connection() {
        use http_body_util::Full;
        use hyper::{Response, body::Bytes, service::service_fn};
        use hyper_util::rt::{TokioExecutor, TokioIo};
        use std::sync::atomic::{AtomicUsize, Ordering};
        let listener = tokio::net::TcpListener::bind("127.0.0.1:0").await.unwrap();
        let address = listener.local_addr().unwrap();
        let connections = Arc::new(AtomicUsize::new(0));
        let count = connections.clone();
        let server = tokio::spawn(async move {
            loop {
                let (socket, _) = listener.accept().await.unwrap();
                count.fetch_add(1, Ordering::SeqCst);
                tokio::spawn(async move {
                    hyper::server::conn::http2::Builder::new(TokioExecutor::new())
                        .serve_connection(
                            TokioIo::new(socket),
                            service_fn(|req: hyper::Request<hyper::body::Incoming>| async move {
                                assert_eq!(req.version(), reqwest::Version::HTTP_2);
                                Ok::<_, std::convert::Infallible>(Response::new(Full::new(
                                    Bytes::from_static(b"ok"),
                                )))
                            }),
                        )
                        .await
                        .unwrap();
                });
            }
        });
        let client = http_builder(Duration::from_secs(3))
            .http2_prior_knowledge()
            .no_proxy()
            .build()
            .unwrap();
        for _ in 0..3 {
            let response = client
                .get(format!("http://{address}"))
                .send()
                .await
                .unwrap();
            assert_eq!(response.version(), reqwest::Version::HTTP_2);
            assert_eq!(response.text().await.unwrap(), "ok");
        }
        assert_eq!(connections.load(Ordering::SeqCst), 1);
        server.abort();
    }
}
