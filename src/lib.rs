// Copyright (C) 2026 Joey Kot
// SPDX-License-Identifier: GPL-3.0-or-later
pub mod audio;
pub mod bridge;
pub mod config;
pub mod dashscope;
pub mod httpapi;
pub mod models;
pub mod realtime;

pub fn id() -> String {
    uuid::Uuid::new_v4().simple().to_string()
}
