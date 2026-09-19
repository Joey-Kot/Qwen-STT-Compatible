// Copyright (C) 2026 Joey Kot
// SPDX-License-Identifier: GPL-3.0-or-later
use crate::{
    config::Config,
    models::{self, Mode, Protocol},
};
use anyhow::{Result, bail, ensure};
use base64::{Engine, engine::general_purpose::STANDARD};
use futures_util::{SinkExt, StreamExt};
use serde::{Deserialize, Serialize};
use serde_json::{Value, json};
use std::{
    collections::{HashMap, VecDeque},
    sync::Arc,
    time::Duration,
};
use tokio::{
    net::TcpStream,
    sync::{mpsc, oneshot},
    time::{Instant, timeout},
};
use tokio_tungstenite::{
    MaybeTlsStream, WebSocketStream,
    tungstenite::{Message, client::IntoClientRequest},
};
use tokio_util::sync::CancellationToken;
use tower::Service;

trait Transport: tokio::io::AsyncRead + tokio::io::AsyncWrite + Unpin + Send {}
impl<T: tokio::io::AsyncRead + tokio::io::AsyncWrite + Unpin + Send> Transport for T {}
type Socket = WebSocketStream<MaybeTlsStream<Box<dyn Transport>>>;

async fn connect(
    request: tokio_tungstenite::tungstenite::handshake::client::Request,
    config: tokio_tungstenite::tungstenite::protocol::WebSocketConfig,
) -> Result<Socket> {
    connect_using(
        request,
        config,
        hyper_util::client::proxy::matcher::Matcher::from_env(),
    )
    .await
}
async fn connect_using(
    request: tokio_tungstenite::tungstenite::handshake::client::Request,
    config: tokio_tungstenite::tungstenite::protocol::WebSocketConfig,
    matcher: hyper_util::client::proxy::matcher::Matcher,
) -> Result<Socket> {
    let url = url::Url::parse(&request.uri().to_string())?;
    let host = url
        .host_str()
        .ok_or_else(|| anyhow::anyhow!("missing WebSocket host"))?;
    let port = url
        .port_or_known_default()
        .ok_or_else(|| anyhow::anyhow!("missing WebSocket port"))?;
    let scheme = if url.scheme() == "wss" {
        "https"
    } else {
        "http"
    };
    let destination: axum::http::Uri = format!("{scheme}://{host}:{port}/").parse()?;
    let loopback = host.eq_ignore_ascii_case("localhost")
        || host
            .trim_matches(['[', ']'])
            .parse::<std::net::IpAddr>()
            .is_ok_and(|ip| ip.is_loopback());
    let proxy = if loopback {
        None
    } else {
        matcher.intercept(&destination)
    };
    let transport: Box<dyn Transport> = if let Some(proxy) = proxy {
        let connector = hyper_rustls::HttpsConnectorBuilder::new()
            .with_native_roots()?
            .https_or_http()
            .enable_http1()
            .build();
        let mut tunnel =
            hyper_util::client::legacy::connect::proxy::Tunnel::new(proxy.uri().clone(), connector);
        if let Some(auth) = proxy.basic_auth() {
            tunnel = tunnel.with_auth(auth.clone());
        }
        Box::new(hyper_util::rt::TokioIo::new(
            tunnel.call(destination).await?,
        ))
    } else {
        Box::new(TcpStream::connect((host.trim_matches(['[', ']']), port)).await?)
    };
    let (socket, _) =
        tokio_tungstenite::client_async_tls_with_config(request, transport, Some(config), None)
            .await?;
    Ok(socket)
}
#[derive(Clone, Debug, PartialEq, Eq)]
pub struct Settings {
    pub model: String,
    pub language: String,
    pub prompt: String,
    pub vad: bool,
    pub silence_ms: u32,
    pub silence_explicit: bool,
}
impl Settings {
    pub fn new(model: String) -> Self {
        Self {
            model,
            language: String::new(),
            prompt: String::new(),
            vad: false,
            silence_ms: 1300,
            silence_explicit: false,
        }
    }
    pub fn validate(&self) -> Result<()> {
        ensure!(
            models::route(&self.model)?.mode == Mode::Realtime,
            "model is not realtime"
        );
        ensure!(
            models::language(&self.model, &self.language),
            "model does not support this language"
        );
        ensure!(
            self.prompt.is_empty() || models::context(&self.model),
            "model does not support prompt context"
        );
        ensure!(
            self.prompt.chars().count() <= 400,
            "prompt exceeds 400 characters"
        );
        ensure!(
            !models::paraformer_v1(&self.model) || !self.silence_explicit,
            "Paraformer v1 does not support custom silence_duration_ms"
        );
        ensure!(
            self.silence_ms == 0 || (200..=6000).contains(&self.silence_ms),
            "silence_duration_ms must be 200..=6000"
        );
        Ok(())
    }
    pub fn session(&self, id: &str) -> Value {
        json!({"id":id,"object":"realtime.transcription_session","type":"transcription","audio":{"input":{"format":{"type":"audio/pcm","rate":24000},"transcription":{"model":self.model,"language":self.language,"prompt":self.prompt},"turn_detection":if !self.vad {Value::Null} else if models::paraformer_v1(&self.model) {json!({"type":"server_vad"})} else {json!({"type":"server_vad","silence_duration_ms":self.silence_ms})},"noise_reduction":null}},"include":[]})
    }
    pub fn update(&self, v: &Value) -> Result<Self> {
        fields(v, &["type", "audio", "include"])?;
        let mut next = self.clone();
        if let Some(t) = v.get("type") {
            ensure!(t == "transcription", "only type=transcription is supported");
        }
        if let Some(include) = v.get("include") {
            ensure!(
                include.as_array().is_some_and(Vec::is_empty),
                "include/logprobs unsupported"
            );
        }
        if let Some(audio) = v.get("audio") {
            fields(audio, &["input"])?;
            if let Some(input) = audio.get("input") {
                fields(
                    input,
                    &[
                        "format",
                        "transcription",
                        "turn_detection",
                        "noise_reduction",
                    ],
                )?;
                if let Some(format) = input.get("format") {
                    fields(format, &["type", "rate"])?;
                    if let Some(t) = format.get("type") {
                        ensure!(t == "audio/pcm", "only PCM16 is supported");
                    }
                    if let Some(r) = format.get("rate") {
                        ensure!(r == 24000, "PCM input must be 24000 Hz");
                    }
                }
                if let Some(t) = input.get("transcription") {
                    fields(t, &["model", "language", "prompt"])?;
                    for (key, target) in [
                        ("model", &mut next.model),
                        ("language", &mut next.language),
                        ("prompt", &mut next.prompt),
                    ] {
                        if let Some(v) = t.get(key) {
                            *target = v
                                .as_str()
                                .ok_or_else(|| anyhow::anyhow!("{key} must be a string"))?
                                .into();
                        }
                    }
                }
                if let Some(noise) = input.get("noise_reduction") {
                    ensure!(noise.is_null(), "noise_reduction must be null");
                }
                if let Some(vad) = input.get("turn_detection") {
                    if vad.is_null() {
                        next.vad = false;
                        next.silence_explicit = false;
                    } else {
                        fields(vad, &["type", "silence_duration_ms"])?;
                        ensure!(
                            vad["type"] == "server_vad",
                            "only server_vad or null supported"
                        );
                        next.vad = true;
                        if models::paraformer_v1(&next.model) {
                            next.silence_explicit = false;
                        }
                        if let Some(s) = vad.get("silence_duration_ms") {
                            next.silence_ms = u32::try_from(
                                s.as_u64()
                                    .ok_or_else(|| anyhow::anyhow!("invalid silence duration"))?,
                            )?;
                            next.silence_explicit = true;
                            ensure!(
                                (200..=6000).contains(&next.silence_ms),
                                "silence duration out of range"
                            );
                        }
                    }
                }
            }
        }
        next.validate()?;
        Ok(next)
    }
}
pub fn fields(v: &Value, allowed: &[&str]) -> Result<()> {
    let object = v
        .as_object()
        .ok_or_else(|| anyhow::anyhow!("expected an object"))?;
    for (key, value) in object {
        ensure!(allowed.contains(&key.as_str()), "unsupported field: {key}");
        ensure!(
            !value.is_null() || key == "turn_detection" || key == "noise_reduction",
            "{key} cannot be null"
        );
    }
    Ok(())
}

#[derive(Clone, Debug, Default, Deserialize, Serialize)]
pub struct Sentence {
    #[serde(default, rename = "sentence_id")]
    pub id: i64,
    #[serde(default)]
    pub text: String,
    #[serde(default, rename = "sentence_begin")]
    pub begin: bool,
    #[serde(default, rename = "sentence_end")]
    pub final_result: bool,
    #[serde(default)]
    pub heartbeat: bool,
    #[serde(default)]
    pub begin_time: i64,
    pub end_time: Option<i64>,
    #[serde(default)]
    pub words: Vec<Value>,
}
#[derive(Clone, Debug, PartialEq, Eq)]
pub enum Kind {
    Started,
    Stopped,
    Committed,
    Delta,
    Completed,
    Failed,
}
#[derive(Clone, Debug)]
pub struct Item {
    pub kind: Kind,
    pub id: String,
    pub previous: String,
    pub delta: String,
    pub text: String,
    pub time: i64,
    pub error: String,
}
#[derive(Clone, Debug)]
pub enum Event {
    Sentence(Sentence),
    Item(Item),
    Finished,
    Error(String),
}
enum Command {
    Audio(Vec<u8>),
    Prompt(String),
    Finish,
}
struct Request {
    command: Command,
    ack: oneshot::Sender<std::result::Result<(), String>>,
}
pub struct Session {
    tx: mpsc::Sender<Request>,
    pub events: mpsc::Receiver<Event>,
    cancel: CancellationToken,
}
impl Drop for Session {
    fn drop(&mut self) {
        self.cancel.cancel();
    }
}
impl Session {
    async fn command(&self, command: Command) -> Result<()> {
        let (tx, rx) = oneshot::channel();
        self.tx
            .send(Request { command, ack: tx })
            .await
            .map_err(|_| anyhow::anyhow!("upstream closed"))?;
        rx.await?.map_err(anyhow::Error::msg)
    }
    pub async fn audio(&self, data: Vec<u8>) -> Result<()> {
        ensure!(data.len().is_multiple_of(2), "PCM must be sample-aligned");
        self.command(Command::Audio(data)).await
    }
    pub async fn prompt(&self, prompt: String) -> Result<()> {
        self.command(Command::Prompt(prompt)).await
    }
    pub async fn finish(&self) -> Result<()> {
        self.command(Command::Finish).await
    }
}
fn control(kind: &str, mut fields: Value) -> Message {
    fields["type"] = json!(kind);
    fields["event_id"] = json!(format!("event_{}", crate::id()));
    Message::Text(fields.to_string().into())
}
fn task_message(action: &str, id: &str, payload: Value) -> Message {
    Message::Text(
        json!({"header":{"action":action,"task_id":id,"streaming":"duplex"},"payload":payload})
            .to_string()
            .into(),
    )
}
fn prompt_input(prompt: &str) -> Value {
    if prompt.is_empty() {
        json!({})
    } else {
        json!({"context":[{"role":"user","content":[{"type":"input_text","text":prompt}]}]})
    }
}
async fn write(socket: &mut Socket, msg: Message, limit: Duration) -> Result<()> {
    timeout(limit, socket.send(msg)).await??;
    Ok(())
}
async fn read(socket: &mut Socket) -> Result<Value> {
    loop {
        match socket.next().await {
            Some(Ok(Message::Text(t))) => return Ok(serde_json::from_str(&t)?),
            Some(Ok(Message::Ping(p))) => socket.send(Message::Pong(p)).await?,
            Some(Ok(Message::Pong(_))) => {}
            Some(Err(e)) => return Err(e.into()),
            _ => bail!("upstream closed before completion"),
        }
    }
}

pub async fn start(cfg: Arc<Config>, settings: Settings) -> Result<Session> {
    settings.validate()?;
    ensure!(
        !cfg.dashscope_api_key.trim().is_empty(),
        "DASHSCOPE_API_KEY 未配置"
    );
    let route = models::route(&settings.model)?;
    let qwen = route.protocol == Protocol::Qwen;
    let mut url = url::Url::parse(if qwen {
        &cfg.dashscope_qwen_ws_url
    } else {
        &cfg.dashscope_ws_url
    })?;
    if qwen {
        let pairs: Vec<_> = url
            .query_pairs()
            .filter(|(k, _)| k != "model")
            .map(|(k, v)| (k.into_owned(), v.into_owned()))
            .collect();
        url.set_query(None);
        url.query_pairs_mut()
            .extend_pairs(pairs)
            .append_pair("model", &settings.model);
    }
    let mut req = url.as_str().into_client_request()?;
    req.headers_mut().insert(
        "Authorization",
        format!("Bearer {}", cfg.dashscope_api_key.trim()).parse()?,
    );
    req.headers_mut()
        .insert("User-Agent", "qwen-stt-compatible".parse()?);
    if !cfg.dashscope_workspace.is_empty() {
        req.headers_mut()
            .insert("X-DashScope-WorkSpace", cfg.dashscope_workspace.parse()?);
    }
    let wsconfig = tokio_tungstenite::tungstenite::protocol::WebSocketConfig::default()
        .max_message_size(Some(1 << 20))
        .max_frame_size(Some(1 << 20));
    let mut socket = timeout(cfg.realtime_connect_timeout, connect(req, wsconfig)).await??;
    let id = uuid::Uuid::new_v4().to_string();
    timeout(cfg.realtime_start_timeout,async {
        if qwen {
            let v=read(&mut socket).await?; ensure!(v["type"]=="session.created","expected session.created: {v}");
            let mut transcription=json!({}); if !settings.language.is_empty() {transcription["language"]=json!(settings.language);}
            let vad=if settings.vad {json!({"type":"server_vad","threshold":0.0,"silence_duration_ms":if settings.silence_ms==0 {800} else {settings.silence_ms}})} else {Value::Null};
            write(&mut socket,control("session.update",json!({"session":{"input_audio_format":"pcm","sample_rate":route.rate,"input_audio_transcription":transcription,"turn_detection":vad}})),cfg.realtime_write_timeout).await?;
            let v=read(&mut socket).await?; ensure!(v["type"]=="session.updated","expected session.updated: {v}");
        } else {
            let mut p=json!({"format":"pcm","sample_rate":route.rate});
            if !models::paraformer_v1(&settings.model) {p["heartbeat"]=json!(true); if settings.silence_ms>0 {p["max_sentence_silence"]=json!(settings.silence_ms);}}
            if !settings.language.is_empty() {p["language_hints"]=json!([settings.language]);}
            write(&mut socket,task_message("run-task",&id,json!({"task_group":"audio","task":"asr","function":"recognition","model":settings.model,"parameters":p,"input":prompt_input(&settings.prompt)})),cfg.realtime_write_timeout).await?;
            let v=read(&mut socket).await?; ensure!(v["header"]["event"]=="task-started" && v["header"]["task_id"]==id,"task initialization failed: {v}");
        }
        Ok::<_,anyhow::Error>(())
    }).await??;
    let (tx, rx) = mpsc::channel(4);
    let (events, receiver) = mpsc::channel(32);
    let cancel = CancellationToken::new();
    let token = cancel.clone();
    tokio::spawn(async move {
        let work = run(socket, rx, &events, cfg, settings, id, route.protocol);
        tokio::select! { _=token.cancelled()=>{}, result=work=>{if let Err(e)=result {tokio::select! {_=token.cancelled()=>{},_=events.send(Event::Error(e.to_string()))=>{}}}} }
    });
    Ok(Session {
        tx,
        events: receiver,
        cancel,
    })
}
async fn run(
    mut socket: Socket,
    mut commands: mpsc::Receiver<Request>,
    events: &mpsc::Sender<Event>,
    cfg: Arc<Config>,
    settings: Settings,
    id: String,
    protocol: Protocol,
) -> Result<()> {
    let mut finishing = false;
    let mut deadline = Instant::now() + Duration::from_secs(86400 * 365);
    let mut state = Normalizer::new(protocol);
    let mut pending = VecDeque::new();
    let mut ended = false;
    loop {
        tokio::select! {
            permit=events.reserve(), if !pending.is_empty()=>{
                let event=pending.pop_front().expect("queued event");
                let done=matches!(event,Event::Finished);
                permit.map_err(|_|anyhow::anyhow!("receiver closed"))?.send(event);
                if done {return Ok(())}
            }
            _=tokio::time::sleep_until(deadline), if finishing=>bail!("upstream finish timeout"),
            cmd=commands.recv()=>{
                let Some(cmd)=cmd else {return Ok(())};
                let result=async {
                    ensure!(!finishing,"upstream already finishing");
                    match cmd.command {
                        Command::Audio(data)=>{for chunk in data.chunks(3200) {let msg=if protocol==Protocol::Qwen {control("input_audio_buffer.append",json!({"audio":STANDARD.encode(chunk)}))} else {Message::Binary(chunk.to_vec().into())}; write(&mut socket,msg,cfg.realtime_write_timeout).await?;}}
                        Command::Prompt(prompt)=>{ensure!(models::context(&settings.model),"prompt update unsupported"); let input=if prompt.is_empty() {json!({"context":[]})} else {prompt_input(&prompt)}; write(&mut socket,task_message("continue-task",&id,json!({"input":input})),cfg.realtime_write_timeout).await?;}
                        Command::Finish=>{finishing=true; deadline=Instant::now()+cfg.realtime_finish_timeout; if protocol==Protocol::Qwen {if !settings.vad {write(&mut socket,control("input_audio_buffer.commit",json!({})),cfg.realtime_write_timeout).await?;} write(&mut socket,control("session.finish",json!({})),cfg.realtime_write_timeout).await?;} else {write(&mut socket,task_message("finish-task",&id,json!({"input":{}})),cfg.realtime_write_timeout).await?;}}
                    }
                    Ok::<_,anyhow::Error>(())
                }.await;
                let error=result.as_ref().err().map(ToString::to_string); let _=cmd.ack.send(result.map_err(|e|e.to_string())); if let Some(e)=error {bail!(e)}
            }
            raw=read(&mut socket), if pending.len()<32 && !ended=>{
                let raw=raw?;
                if protocol!=Protocol::Qwen {ensure!(raw["header"]["task_id"]==id,"upstream task_id mismatch");}
                if let Some(event)=state.accept(raw,finishing)? {
                    ended=matches!(event,Event::Finished); pending.push_back(event);
                }
            }
        }
    }
}
#[derive(Default)]
struct QwenItem {
    text: String,
    started: bool,
    stopped: bool,
    committed: bool,
    done: bool,
}
pub struct Normalizer {
    protocol: Protocol,
    last_final: i64,
    para_id: i64,
    para_begin: i64,
    para_active: bool,
    items: HashMap<String, QwenItem>,
    pending: usize,
    text_bytes: usize,
}
impl Normalizer {
    pub fn new(protocol: Protocol) -> Self {
        Self {
            protocol,
            last_final: 0,
            para_id: 0,
            para_begin: 0,
            para_active: false,
            items: HashMap::new(),
            pending: 0,
            text_bytes: 0,
        }
    }
    pub fn accept(&mut self, v: Value, finishing: bool) -> Result<Option<Event>> {
        if self.protocol == Protocol::Qwen {
            return self.qwen(v, finishing);
        }
        match v["header"]["event"].as_str().unwrap_or("") {
            "task-failed" => bail!(
                "{}: {}",
                v["header"]["error_code"],
                v["header"]["error_message"]
            ),
            "task-finished" => {
                ensure!(finishing, "upstream ended prematurely");
                ensure!(!self.para_active, "unfinished Paraformer sentence");
                Ok(Some(Event::Finished))
            }
            "result-generated" => {
                let raw = &v["payload"]["output"]["sentence"];
                if raw.is_null() {
                    return Ok(None);
                }
                let mut s: Sentence = serde_json::from_value(raw.clone())?;
                if s.heartbeat {
                    return Ok(None);
                }
                if self.protocol == Protocol::Paraformer {
                    ensure!(s.begin_time >= 0, "invalid Paraformer begin_time");
                    if self.para_id > 0
                        && (s.begin_time < self.para_begin
                            || (!self.para_active && s.begin_time == self.para_begin))
                    {
                        return Ok(None);
                    }
                    ensure!(
                        !self.para_active || self.para_begin == s.begin_time,
                        "Paraformer changed sentence before final result"
                    );
                    s.begin = !self.para_active;
                    if !self.para_active {
                        self.para_id += 1;
                        self.para_begin = s.begin_time;
                        self.para_active = true;
                    }
                    s.id = self.para_id;
                    if s.final_result {
                        self.para_active = false;
                        if s.end_time.is_none_or(|t| t < s.begin_time) {
                            s.end_time = s
                                .words
                                .iter()
                                .filter_map(|w| w["end_time"].as_i64())
                                .filter(|t| *t >= s.begin_time)
                                .max();
                        }
                    }
                }
                if s.id <= self.last_final {
                    return Ok(None);
                }
                if s.final_result {
                    self.last_final = s.id;
                }
                s.words.clear();
                Ok(Some(Event::Sentence(s)))
            }
            _ => Ok(None),
        }
    }
    fn qwen(&mut self, v: Value, finishing: bool) -> Result<Option<Event>> {
        let typ = v["type"].as_str().unwrap_or("");
        if typ == "error" {
            bail!("{}: {}", v["error"]["code"], v["error"]["message"])
        }
        if typ == "session.finished" {
            ensure!(finishing, "upstream ended prematurely");
            ensure!(self.pending == 0, "unfinished Qwen items");
            return Ok(Some(Event::Finished));
        }
        let kind = match typ {
            "input_audio_buffer.speech_started" => Kind::Started,
            "input_audio_buffer.speech_stopped" => Kind::Stopped,
            "input_audio_buffer.committed" => Kind::Committed,
            "conversation.item.input_audio_transcription.text" => Kind::Delta,
            "conversation.item.input_audio_transcription.completed" => Kind::Completed,
            "conversation.item.input_audio_transcription.failed" => Kind::Failed,
            _ => return Ok(None),
        };
        let id = v["item_id"].as_str().unwrap_or("");
        let previous = v["previous_item_id"].as_str().unwrap_or("");
        ensure!(
            !id.is_empty()
                && id.len() <= 256
                && previous.len() <= 256
                && v.get("content_index").is_none_or(|i| i == 0),
            "invalid Qwen item identity"
        );
        if !self.items.contains_key(id) {
            ensure!(
                self.items.len() < 4096 && self.pending < 64,
                "Qwen item limit exceeded"
            );
            self.items.insert(id.into(), QwenItem::default());
            self.pending += 1;
        }
        let state = self.items.get_mut(id).unwrap();
        if state.done {
            return Ok(None);
        }
        let mut item = Item {
            kind: kind.clone(),
            id: id.into(),
            previous: previous.into(),
            delta: String::new(),
            text: String::new(),
            time: 0,
            error: String::new(),
        };
        match kind {
            Kind::Started | Kind::Stopped => {
                let key = if kind == Kind::Started {
                    "audio_start_ms"
                } else {
                    "audio_end_ms"
                };
                item.time = v[key]
                    .as_i64()
                    .filter(|t| *t >= 0)
                    .ok_or_else(|| anyhow::anyhow!("missing {key}"))?;
                let seen = if kind == Kind::Started {
                    &mut state.started
                } else {
                    &mut state.stopped
                };
                if *seen {
                    return Ok(None);
                }
                *seen = true;
            }
            Kind::Committed => {
                if state.committed {
                    return Ok(None);
                }
                state.committed = true;
            }
            Kind::Delta | Kind::Completed => {
                let text = v[if kind == Kind::Completed {
                    "transcript"
                } else {
                    "text"
                }]
                .as_str()
                .unwrap_or("");
                ensure!(
                    text.len() <= 1 << 20 && text.starts_with(&state.text),
                    "invalid confirmed text prefix or oversized text"
                );
                item.delta = text[state.text.len()..].into();
                item.text = text.into();
                self.text_bytes += item.delta.len();
                ensure!(self.text_bytes <= 8 << 20, "pending text exceeds 8 MiB");
                state.text = text.into();
                if kind == Kind::Completed {
                    self.pending -= 1;
                    self.text_bytes -= state.text.len();
                    state.text.clear();
                    state.done = true;
                }
            }
            Kind::Failed => {
                item.error = format!("{}: {}", v["error"]["code"], v["error"]["message"]);
                self.pending -= 1;
                self.text_bytes -= state.text.len();
                state.text.clear();
                state.done = true;
            }
        }
        Ok(Some(Event::Item(item)))
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    #[test]
    fn strict_settings() {
        let s = Settings::new("qwen3-asr-flash-realtime".into());
        for v in [
            json!({"unknown":1}),
            json!({"audio":{"input":{"format":{"rate":16000}}}}),
            json!({"audio":{"input":{"transcription":{"prompt":"unsupported"}}}}),
            json!({"audio":null}),
        ] {
            assert!(s.update(&v).is_err());
        }
    }
    #[test]
    fn qwen_stable_text_and_dedup() {
        let mut n = Normalizer::new(Protocol::Qwen);
        let a = json!({"type":"conversation.item.input_audio_transcription.text","item_id":"a","text":"hello"});
        assert!(n.accept(a, false).unwrap().is_some());
        assert!(n.accept(json!({"type":"conversation.item.input_audio_transcription.text","item_id":"a","text":"changed"}),false).is_err());
    }
    #[tokio::test]
    #[allow(clippy::result_large_err)] // The tungstenite handshake callback fixes this error type.
    async fn websocket_connect_proxy_preserves_auth_separation() {
        use tokio::io::{AsyncReadExt, AsyncWriteExt};
        let listener = tokio::net::TcpListener::bind("127.0.0.1:0").await.unwrap();
        let address = listener.local_addr().unwrap();
        let server = tokio::spawn(async move {
            let (mut socket, _) = listener.accept().await.unwrap();
            let mut headers = Vec::new();
            while !headers.ends_with(b"\r\n\r\n") {
                headers.push(socket.read_u8().await.unwrap());
                assert!(headers.len() < 8192);
            }
            let text = String::from_utf8(headers).unwrap().to_ascii_lowercase();
            assert!(text.starts_with("connect asr.example:80 http/1.1"));
            assert!(text.contains("proxy-authorization: basic"));
            assert!(!text.contains("bearer"));
            socket
                .write_all(b"HTTP/1.1 200 Connection established\r\n\r\n")
                .await
                .unwrap();
            let mut socket = tokio_tungstenite::accept_hdr_async(
                socket,
                |request: &tokio_tungstenite::tungstenite::handshake::server::Request, response| {
                    assert_eq!(request.headers()["authorization"], "Bearer key");
                    assert!(!request.headers().contains_key("proxy-authorization"));
                    Ok(response)
                },
            )
            .await
            .unwrap();
            socket.send(Message::Text("ok".into())).await.unwrap();
        });
        let matcher = hyper_util::client::proxy::matcher::Matcher::builder()
            .http(format!("http://user:pass@{address}"))
            .build();
        let mut request = "ws://asr.example/".into_client_request().unwrap();
        request
            .headers_mut()
            .insert("authorization", "Bearer key".parse().unwrap());
        let mut socket = timeout(
            Duration::from_secs(3),
            connect_using(request, Default::default(), matcher),
        )
        .await
        .unwrap()
        .unwrap();
        assert_eq!(
            socket.next().await.unwrap().unwrap().to_text().unwrap(),
            "ok"
        );
        server.await.unwrap();
    }
}
