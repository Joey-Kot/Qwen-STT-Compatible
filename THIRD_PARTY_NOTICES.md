# Third-Party Notices

Qwen STT Compatible is GPL-3.0-or-later. Copyright (C) 2026 Joey Kot.

## ASR-Audio-Preprocess

- Rust crate: smartaudio 0.1.0
- Source: https://github.com/Joey-Kot/ASR-Audio-Preprocess
- Pinned revision: 8e132bf8eddd361c2a03c074d9da85e0e5298dd2
- License: GPL-3.0-or-later; complete text in LICENSE.

## Statically linked audio libraries

| Component | Version | License text |
|---|---|---|
| FFmpeg | 8.1.2 | THIRD_PARTY_LICENSES/FFmpeg-GPL-3.0-or-later.txt |
| Opus | 1.5.2 | THIRD_PARTY_LICENSES/Opus-BSD-3-Clause.txt |
| LAME | 3.100 | THIRD_PARTY_LICENSES/LAME-LGPL-2.0-only.txt |
| libogg | 1.3.5 | THIRD_PARTY_LICENSES/Xiph-BSD-3-Clause.txt |
| libvorbis | 1.3.7 | THIRD_PARTY_LICENSES/Xiph-BSD-3-Clause.txt |
| OpenCore AMR | 0.1.6 | THIRD_PARTY_LICENSES/opencore-amr-Apache-2.0.txt |
| Speex | 1.2.1 | THIRD_PARTY_LICENSES/Speex-BSD-3-Clause.txt |
| vo-amrwbenc | 0.1.3 | THIRD_PARTY_LICENSES/vo-amrwbenc-Apache-2.0.txt |

Native build scripts are synchronized with the pinned audio library.
The FFmpeg configuration enables GPL and version 3, disables nonfree, and
does not link libavfilter. Source URLs and checksums are recorded in
scripts/bootstrap-static-audio-deps.sh and scripts/audio-sources.sha256.
OpenCore AMR and vo-amrwbenc NOTICE files accompany their license texts.

Earshot 1.2.2 is MIT OR Apache-2.0. The complete texts are
THIRD_PARTY_LICENSES/Earshot-MIT.txt and Earshot-Apache-2.0.txt.
Source: https://github.com/pykeio/earshot.

## Rust dependencies

Cargo.lock records exact Rust dependency versions, including Tokio, Axum,
Reqwest, Rustls, Tungstenite, Serde and Clap. Release packages include
vendored-dependency license files in THIRD_PARTY_LICENSES/rust/.

The Docker runtime separately installs Debian's FFmpeg executable for realtime
file decoding. Debian provides that package's license and build configuration.

## Test fixture

tests/fixtures/jfk.flac is the public-domain speech excerpt distributed with
the MIT-licensed Whisper test suite. Its source revision, checksum and license
are retained in tests/fixtures/README.md and Whisper-LICENSE.
