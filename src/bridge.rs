// Copyright (C) 2026 Joey Kot
// SPDX-License-Identifier: GPL-3.0-or-later
use crate::{
    audio::Resampler,
    config::Config,
    models::{self, Protocol},
    realtime::{self, Event, Item, Kind, Session, Settings},
};
use anyhow::{Result, bail, ensure};
use axum::extract::ws::{Message, WebSocket};
use base64::{Engine, engine::general_purpose::STANDARD};
use serde::Deserialize;
use serde_json::{Value, json};
use std::{
    collections::{HashMap, VecDeque},
    sync::Arc,
};
use tokio::{
    sync::mpsc,
    time::{Instant, timeout},
};

#[derive(Deserialize)]
#[serde(deny_unknown_fields)]
struct ClientEvent {
    #[serde(rename = "type")]
    kind: String,
    #[serde(default)]
    event_id: String,
    #[serde(default)]
    audio: String,
    session: Option<Value>,
}
#[derive(Default)]
struct BridgeItem {
    id: String,
    text: String,
    committed: bool,
    done: bool,
}
struct Turn {
    session: Session,
    forward: tokio::task::JoinHandle<()>,
    settings: Settings,
    protocol: Protocol,
    item: String,
    text: String,
    committed: bool,
    samples: i64,
    consumed: i64,
    resampler: Resampler,
    base_ms: i64,
    sentence: i64,
    order: u64,
    delayed: bool,
    queued: VecDeque<Event>,
    queued_bytes: usize,
    items: HashMap<String, BridgeItem>,
    cleared: bool,
    manual_bound: bool,
}
impl Drop for Turn {
    fn drop(&mut self) {
        self.forward.abort();
    }
}
pub struct Bridge {
    cfg: Arc<Config>,
    socket: WebSocket,
    settings: Settings,
    id: String,
    active: Option<String>,
    previous: Option<String>,
    turns: HashMap<String, Turn>,
    tx: mpsc::Sender<(String, Event)>,
    rx: mpsc::Receiver<(String, Event)>,
    total: i64,
    order: u64,
}
impl Bridge {
    pub async fn serve(cfg: Arc<Config>, socket: WebSocket, settings: Settings) {
        let (tx, rx) = mpsc::channel(32);
        let mut bridge = Self {
            cfg,
            socket,
            settings,
            id: format!("sess_{}", crate::id()),
            active: None,
            previous: None,
            turns: HashMap::new(),
            tx,
            rx,
            total: 0,
            order: 0,
        };
        if let Err(e) = bridge.run().await {
            tracing::info!(session=%bridge.id,error=%e,"realtime session closed");
        }
    }
    async fn send(&mut self, kind: &str, mut value: Value) -> Result<()> {
        value["type"] = json!(kind);
        value["event_id"] = json!(format!("event_{}", crate::id()));
        timeout(
            self.cfg.realtime_write_timeout,
            self.socket.send(Message::Text(value.to_string().into())),
        )
        .await??;
        Ok(())
    }
    async fn error(&mut self, id: &str, error: &str, fatal: bool) -> Result<()> {
        self.send("error",json!({"error":{"type":if fatal {"server_error"} else {"invalid_request_error"},"code":if fatal {"upstream_error"} else {"invalid_event"},"message":error,"event_id":id}})).await
    }
    async fn run(&mut self) -> Result<()> {
        self.send(
            "session.created",
            json!({"session":self.settings.session(&self.id)}),
        )
        .await?;
        let mut idle = Instant::now() + self.cfg.realtime_idle_timeout;
        loop {
            tokio::select! {
                _=tokio::time::sleep_until(idle)=>bail!("downstream idle timeout"),
                message=self.socket.recv()=>{
                    idle=Instant::now()+self.cfg.realtime_idle_timeout;
                    let Some(message)=message else {return Ok(())};
                    let message=message?;
                    let text=match message {Message::Text(t)=>t,Message::Close(_)=>return Ok(()),Message::Ping(p)=>{self.socket.send(Message::Pong(p)).await?;continue},Message::Pong(_)=>continue,_=>{self.error("","only JSON text messages are supported",false).await?;continue}};
                    let event=match serde_json::from_str::<ClientEvent>(&text) {Ok(e)=>e,Err(e)=>{self.error("",&e.to_string(),false).await?;continue}};
                    let id=event.event_id.clone();
                    if let Err(e)=self.handle(event).await {let fatal=e.downcast_ref::<UpstreamError>().is_some();self.error(&id,&e.to_string(),fatal).await?;if fatal {return Err(e)}}
                    if let Err(e)=self.drain().await {self.error("",&e.to_string(),true).await?;return Err(e)}
                }
                Some((key,event))=self.rx.recv()=>{
                    if let Err(e)=self.upstream(key,event).await {self.error("",&e.to_string(),true).await?;return Err(e)}
                }
            }
        }
    }
    fn blocked(&self, turn: &Turn) -> bool {
        self.turns.values().any(|t| {
            t.order < turn.order
                && t.protocol == Protocol::Qwen
                && t.settings.vad
                && t.committed
                && !t.cleared
        })
    }
    async fn start_turn(&mut self) -> Result<String> {
        ensure!(
            !self.settings.model.is_empty(),
            "set transcription.model using session.update first"
        );
        ensure!(self.turns.len() < 4, "pending audio turn limit reached");
        let mut session = realtime::start(self.cfg.clone(), self.settings.clone())
            .await
            .map_err(upstream_error)?;
        let key = crate::id();
        let forward_key = key.clone();
        let tx = self.tx.clone();
        let mut events = std::mem::replace(&mut session.events, mpsc::channel(1).1);
        let forward = tokio::spawn(async move {
            loop {
                let event = events
                    .recv()
                    .await
                    .unwrap_or(Event::Error("upstream closed without completion".into()));
                let done = matches!(&event, Event::Finished | Event::Error(_));
                if tx.send((forward_key.clone(), event)).await.is_err() || done {
                    break;
                }
            }
        });
        let route = models::route(&self.settings.model)?;
        self.order += 1;
        self.turns.insert(
            key.clone(),
            Turn {
                session,
                forward,
                settings: self.settings.clone(),
                protocol: route.protocol,
                item: String::new(),
                text: String::new(),
                committed: false,
                samples: 0,
                consumed: 0,
                resampler: Resampler::new(route.rate),
                base_ms: self.total / 24,
                sentence: 0,
                order: self.order,
                delayed: false,
                queued: VecDeque::new(),
                queued_bytes: 0,
                items: HashMap::new(),
                cleared: false,
                manual_bound: false,
            },
        );
        self.active = Some(key.clone());
        Ok(key)
    }
    async fn handle(&mut self, event: ClientEvent) -> Result<()> {
        match event.kind.as_str() {
            "session.update" => {
                let next = self.settings.update(
                    event
                        .session
                        .as_ref()
                        .ok_or_else(|| anyhow::anyhow!("session must be an object"))?,
                )?;
                if let Some(turn) = self.active.as_ref().and_then(|k| self.turns.get_mut(k)) {
                    let mut previous = self.settings.clone();
                    previous.prompt = next.prompt.clone();
                    ensure!(
                        previous == next,
                        "only prompt may change during audio input; commit or clear first"
                    );
                    if self.settings.prompt != next.prompt {
                        turn.session
                            .prompt(next.prompt.clone())
                            .await
                            .map_err(upstream_error)?;
                        turn.settings = next.clone();
                    }
                }
                self.settings = next;
                self.send(
                    "session.updated",
                    json!({"session":self.settings.session(&self.id)}),
                )
                .await
            }
            "input_audio_buffer.append" => {
                let data = STANDARD.decode(&event.audio)?;
                ensure!(
                    !data.is_empty() && data.len().is_multiple_of(2),
                    "audio must be Base64 encoded complete PCM16 samples"
                );
                let key = match self.active.clone() {
                    Some(k) => k,
                    None => self.start_turn().await?,
                };
                let turn = self.turns.get_mut(&key).unwrap();
                turn.samples += data.len() as i64 / 2;
                self.total += data.len() as i64 / 2;
                turn.session
                    .audio(turn.resampler.process(&data)?)
                    .await
                    .map_err(upstream_error)
            }
            "input_audio_buffer.commit" => {
                let key = self
                    .active
                    .clone()
                    .ok_or_else(|| anyhow::anyhow!("at least 100 ms of audio required"))?;
                ensure!(
                    self.turns[&key].samples - self.turns[&key].consumed >= 2400,
                    "at least 100 ms of uncommitted audio required"
                );
                let mut turn = self.turns.remove(&key).unwrap();
                if turn.protocol != Protocol::Qwen || !turn.settings.vad {
                    if turn.protocol != Protocol::Qwen && turn.settings.vad && !turn.item.is_empty()
                    {
                        self.send(
                            "input_audio_buffer.speech_stopped",
                            json!({"item_id":turn.item,"audio_end_ms":self.total/24}),
                        )
                        .await?;
                    }
                    if turn.item.is_empty() {
                        turn.item = format!("item_{}", crate::id());
                    }
                    turn.delayed = self.blocked(&turn);
                    if !turn.delayed {
                        self.commit(&turn.item).await?;
                        if turn.protocol != Protocol::Qwen {
                            self.delta(&turn.item, &turn.text).await?;
                        }
                    }
                }
                turn.committed = true;
                turn.session.finish().await.map_err(upstream_error)?;
                self.active = None;
                self.turns.insert(key, turn);
                Ok(())
            }
            "input_audio_buffer.clear" => {
                if let Some(key) = self.active.take()
                    && let Some(turn) = self.turns.get_mut(&key)
                {
                    if turn.protocol == Protocol::Qwen
                        && turn.items.values().any(|i| i.committed && !i.done)
                    {
                        turn.cleared = true;
                        turn.committed = true;
                        turn.session.finish().await.map_err(upstream_error)?;
                    } else {
                        self.turns.remove(&key);
                    }
                }
                self.send("input_audio_buffer.cleared", json!({})).await
            }
            _ => bail!("unsupported client event: {}", event.kind),
        }
    }
    async fn commit(&mut self, item: &str) -> Result<()> {
        let previous = self.previous.clone();
        self.send(
            "input_audio_buffer.committed",
            json!({"item_id":item,"previous_item_id":previous}),
        )
        .await?;
        self.previous = Some(item.into());
        self.send("conversation.item.added",json!({"previous_item_id":previous,"item":{"id":item,"object":"realtime.item","type":"message","status":"completed","role":"user","content":[{"type":"input_audio","transcript":null}]}})).await
    }
    async fn delta(&mut self, item: &str, text: &str) -> Result<()> {
        if text.is_empty() {
            return Ok(());
        }
        self.send(
            "conversation.item.input_audio_transcription.delta",
            json!({"item_id":item,"content_index":0,"delta":text}),
        )
        .await
    }
    async fn completed(&mut self, item: &str, text: &str) -> Result<()> {
        self.send(
            "conversation.item.input_audio_transcription.completed",
            json!({"item_id":item,"content_index":0,"transcript":text}),
        )
        .await
    }
    async fn failed(&mut self, item: &str, error: &str) -> Result<()> {
        self.send("conversation.item.input_audio_transcription.failed",json!({"item_id":item,"content_index":0,"error":{"type":"server_error","code":"upstream_error","message":error}})).await
    }
    async fn upstream(&mut self, key: String, event: Event) -> Result<()> {
        let Some(mut turn) = self.turns.remove(&key) else {
            return Ok(());
        };
        if self.blocked(&turn) {
            // Coalesce adjacent confirmed-prefix updates while preserving the delta.
            if let (Some(Event::Item(last)), Event::Item(next)) = (turn.queued.back_mut(), &event)
                && last.kind == Kind::Delta
                && next.kind == Kind::Delta
                && last.id == next.id
            {
                turn.queued_bytes -= event_size(&Event::Item(last.clone()));
                last.delta.push_str(&next.delta);
                last.text = next.text.clone();
                turn.queued_bytes += event_size(&Event::Item(last.clone()));
                ensure!(
                    turn.queued_bytes <= 8 << 20,
                    "ordered event buffer exceeds 8 MiB"
                );
                self.turns.insert(key, turn);
                return Ok(());
            }
            turn.queued_bytes += event_size(&event);
            ensure!(
                turn.queued.len() < 64 && turn.queued_bytes <= 8 << 20,
                "ordered event buffer full"
            );
            turn.queued.push_back(event);
            self.turns.insert(key, turn);
            return Ok(());
        }
        if !self.process(&mut turn, event).await? {
            self.turns.insert(key, turn);
        }
        self.drain().await
    }
    async fn drain(&mut self) -> Result<()> {
        loop {
            let key = self
                .turns
                .iter()
                .filter(|(_, t)| (t.delayed || !t.queued.is_empty()) && !self.blocked(t))
                .min_by_key(|(_, t)| t.order)
                .map(|(k, _)| k.clone());
            let Some(key) = key else { return Ok(()) };
            let mut turn = self.turns.remove(&key).unwrap();
            if turn.delayed {
                self.commit(&turn.item).await?;
                if turn.protocol != Protocol::Qwen {
                    self.delta(&turn.item, &turn.text).await?;
                }
                turn.delayed = false;
            }
            let queued = std::mem::take(&mut turn.queued);
            turn.queued_bytes = 0;
            let mut done = false;
            for event in queued {
                done = self.process(&mut turn, event).await?;
                if done {
                    break;
                }
            }
            if !done {
                self.turns.insert(key, turn);
            }
        }
    }
    async fn process(&mut self, turn: &mut Turn, event: Event) -> Result<bool> {
        if let Event::Error(error) = &event {
            if turn.protocol == Protocol::Qwen {
                for item in turn.items.values().filter(|i| i.committed && !i.done) {
                    self.failed(&item.id, error).await?;
                }
                if !turn.settings.vad && !turn.item.is_empty() && !turn.manual_bound {
                    self.failed(&turn.item, error).await?;
                }
            } else if !turn.item.is_empty() {
                self.failed(&turn.item, error).await?;
            }
            bail!(error.clone());
        }
        if turn.protocol == Protocol::Qwen {
            return self.qwen(turn, event).await;
        }
        match event {
            Event::Sentence(s) => {
                if turn.settings.vad && !turn.committed {
                    if turn.item.is_empty() {
                        turn.item = format!("item_{}", crate::id());
                        turn.sentence = s.id;
                        self.send(
                            "input_audio_buffer.speech_started",
                            json!({"item_id":turn.item,"audio_start_ms":turn.base_ms+s.begin_time}),
                        )
                        .await?;
                    }
                    ensure!(
                        turn.sentence == s.id,
                        "sentence changed before final result"
                    );
                    if s.final_result {
                        let end = s
                            .end_time
                            .filter(|t| *t >= 0)
                            .ok_or_else(|| anyhow::anyhow!("VAD final result missing end_time"))?;
                        turn.consumed = turn.consumed.max((end * 24).min(turn.samples));
                        self.send(
                            "input_audio_buffer.speech_stopped",
                            json!({"item_id":turn.item,"audio_end_ms":turn.base_ms+end}),
                        )
                        .await?;
                        self.commit(&turn.item).await?;
                        self.delta(&turn.item, &s.text).await?;
                        self.completed(&turn.item, &s.text).await?;
                        turn.item.clear();
                    }
                } else if s.final_result {
                    ensure!(
                        turn.text.len() + s.text.len() <= 1 << 20,
                        "turn text exceeds 1 MiB"
                    );
                    turn.text.push_str(&s.text);
                    if turn.committed {
                        self.delta(&turn.item, &s.text).await?;
                    }
                }
            }
            Event::Finished => {
                ensure!(turn.committed, "upstream ended before commit");
                if !turn.item.is_empty() {
                    self.completed(&turn.item, &turn.text).await?;
                }
                return Ok(true);
            }
            _ => {}
        }
        Ok(false)
    }
    async fn qwen(&mut self, turn: &mut Turn, event: Event) -> Result<bool> {
        match event {
            Event::Item(e) => {
                if turn.cleared && turn.items.get(&e.id).is_none_or(|i| !i.committed) {
                    return Ok(false);
                }
                if !turn.items.contains_key(&e.id) {
                    ensure!(turn.items.len() < 4096, "item limit exceeded");
                    let mut item = BridgeItem {
                        id: format!("item_{}", crate::id()),
                        ..Default::default()
                    };
                    if !turn.settings.vad {
                        ensure!(
                            turn.committed && !turn.manual_bound,
                            "Qwen manual item does not match local turn"
                        );
                        item.id = turn.item.clone();
                        item.committed = true;
                        turn.manual_bound = true;
                    }
                    turn.items.insert(e.id.clone(), item);
                }
                let item = turn.items.get_mut(&e.id).unwrap();
                if item.done {
                    return Ok(false);
                }
                match e.kind {
                    Kind::Started => {
                        self.send(
                            "input_audio_buffer.speech_started",
                            json!({"item_id":item.id,"audio_start_ms":turn.base_ms+e.time}),
                        )
                        .await?
                    }
                    Kind::Stopped => {
                        turn.consumed = turn.consumed.max((e.time * 24).min(turn.samples));
                        self.send(
                            "input_audio_buffer.speech_stopped",
                            json!({"item_id":item.id,"audio_end_ms":turn.base_ms+e.time}),
                        )
                        .await?;
                    }
                    Kind::Committed => {
                        if !item.committed {
                            self.commit(&item.id).await?;
                            item.committed = true;
                            self.delta(&item.id, &item.text).await?;
                        }
                    }
                    Kind::Delta | Kind::Completed => {
                        item.text = e.text.clone();
                        if item.committed {
                            self.delta(&item.id, &e.delta).await?;
                        }
                        if e.kind == Kind::Completed {
                            ensure!(
                                item.committed,
                                "item completed before commit acknowledgement"
                            );
                            item.done = true;
                            item.text.clear();
                            self.completed(&item.id, &e.text).await?;
                        }
                    }
                    Kind::Failed => {
                        ensure!(item.committed, "failed item missing commit acknowledgement");
                        item.done = true;
                        item.text.clear();
                        self.failed(&item.id, &e.error).await?;
                    }
                }
            }
            Event::Finished => {
                ensure!(turn.committed, "upstream ended before commit");
                ensure!(
                    !turn.items.values().any(|i| i.committed && !i.done),
                    "unfinished committed items"
                );
                ensure!(
                    turn.settings.vad || turn.manual_bound,
                    "manual commit returned no item"
                );
                return Ok(true);
            }
            _ => {}
        }
        Ok(false)
    }
}
fn event_size(event: &Event) -> usize {
    match event {
        Event::Item(i) => i.text.len() + i.delta.len() + i.id.len() + i.previous.len(),
        Event::Sentence(s) => s.text.len(),
        Event::Error(e) => e.len(),
        Event::Finished => 0,
    }
}
#[derive(Debug)]
struct UpstreamError(String);
impl std::fmt::Display for UpstreamError {
    fn fmt(&self, f: &mut std::fmt::Formatter<'_>) -> std::fmt::Result {
        f.write_str(&self.0)
    }
}
impl std::error::Error for UpstreamError {}
fn upstream_error(e: anyhow::Error) -> anyhow::Error {
    UpstreamError(e.to_string()).into()
}

/// File transcription consumes Qwen stable prefixes in audio order, even when
/// later items complete first. Completed IDs are deduplicated by the adapter.
#[derive(Default)]
pub struct OrderedItems {
    items: HashMap<String, (String, usize, bool)>,
    order: VecDeque<String>,
    bytes: usize,
}
impl OrderedItems {
    pub fn accept(&mut self, e: &Item) -> Result<String> {
        ensure!(e.kind != Kind::Failed, "{}", e.error);
        if !self.items.contains_key(&e.id) {
            ensure!(self.order.len() < 64, "pending item limit exceeded");
            self.items.insert(e.id.clone(), (String::new(), 0, false));
            self.order.push_back(e.id.clone());
        }
        let item = self.items.get_mut(&e.id).unwrap();
        if e.kind == Kind::Delta || e.kind == Kind::Completed {
            ensure!(e.text.starts_with(&item.0), "changed confirmed prefix");
            self.bytes += e.text.len() - item.0.len();
            ensure!(self.bytes <= 8 << 20, "pending text limit exceeded");
            item.0 = e.text.clone();
            item.2 = e.kind == Kind::Completed;
        }
        let mut delta = String::new();
        while let Some(id) = self.order.front() {
            let item = self.items.get_mut(id).unwrap();
            delta.push_str(&item.0[item.1..]);
            item.1 = item.0.len();
            if !item.2 {
                break;
            }
            self.bytes -= item.0.len();
            self.items.remove(id);
            self.order.pop_front();
        }
        Ok(delta)
    }
    pub fn is_empty(&self) -> bool {
        self.order.is_empty()
    }
}
