// Copyright (C) 2026 Joey Kot
// SPDX-License-Identifier: GPL-3.0-or-later
use anyhow::Result;
use clap::Parser;
use qwen_stt_compatible::{
    config::Config,
    httpapi::{App, router},
};

#[tokio::main]
async fn main() -> Result<()> {
    tracing_subscriber::fmt()
        .with_env_filter(
            tracing_subscriber::EnvFilter::try_from_default_env().unwrap_or_else(|_| "info".into()),
        )
        .init();
    let cfg = Config::parse();
    cfg.validate()?;
    let address = if cfg.listen.starts_with(':') {
        format!("0.0.0.0{}", cfg.listen)
    } else {
        cfg.listen.clone()
    };
    let listener = tokio::net::TcpListener::bind(&address).await?;
    tracing::info!(address=%listener.local_addr()?,"listening");
    axum::serve(listener, router(App::new(cfg)?))
        .with_graceful_shutdown(shutdown())
        .await?;
    Ok(())
}
async fn shutdown() {
    #[cfg(unix)]
    {
        let mut terminate =
            tokio::signal::unix::signal(tokio::signal::unix::SignalKind::terminate())
                .expect("install SIGTERM handler");
        tokio::select! {_=tokio::signal::ctrl_c()=>{},_=terminate.recv()=>{}}
    }
    #[cfg(not(unix))]
    {
        let _ = tokio::signal::ctrl_c().await;
    }
}
