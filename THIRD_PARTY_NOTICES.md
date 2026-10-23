# Third-Party Notices

The server statically links the following components. Release packages include
their complete license texts in `THIRD_PARTY_LICENSES/`.

## ASR-Audio-Preprocess

- Version: v0.0.0-20261023044719-82196363aea0
- Source: https://github.com/Joey-Kot/ASR-Audio-Preprocess
- License: GPL-3.0-or-later
- Full license text: `LICENSE`

## FFmpeg

- Version: 8.1.3 with the CVE-2026-107678 backport supplied by ASR-Audio-Preprocess
- Source: https://ffmpeg.org/
- License: LGPL-2.1-or-later for the distributed build. The delegated build
  scripts do not enable `--enable-gpl`, `--enable-version3`, or
  `--enable-nonfree`.
- Full license text: `THIRD_PARTY_LICENSES/FFmpeg-LGPL-2.1-or-later.txt`
- Build configuration: `scripts/bootstrap-static-audio-deps.sh`

## Opus

- Version: 1.5.2
- Source: https://opus-codec.org/
- License: BSD-3-Clause
- Full license text: `THIRD_PARTY_LICENSES/Opus-BSD-3-Clause.txt`
- Build configuration: `scripts/bootstrap-static-audio-deps.sh`

## Gorilla WebSocket

- Version: v1.5.4-0.20240701034025-d67f41855da4
- Source: https://github.com/gorilla/websocket
- License: BSD-2-Clause
- Full license text: `THIRD_PARTY_LICENSES/Gorilla-WebSocket-BSD-2-Clause.txt`

## Go supplementary networking library

- Version: golang.org/x/net v0.61.0 (WebSocket proxy support)
- Source: https://go.googlesource.com/net
- License: BSD-3-Clause
- Full license text: `THIRD_PARTY_LICENSES/Go-x-net-BSD-3-Clause.txt`

Realtime file decoding uses a separately installed FFmpeg executable from
`PATH`. Its license and security updates are managed by that installation;
the statically linked FFmpeg fixes above do not update this executable.

## Project License

- GPL-3.0-or-later
