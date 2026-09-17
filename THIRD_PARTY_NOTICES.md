# Third-Party Notices

The server statically links the following components. Release packages include
their complete license texts in `THIRD_PARTY_LICENSES/`.

## ASR-Audio-Preprocess

- Version: v0.0.0-20260901092746-72922741cf43
- Source: https://github.com/Joey-Kot/ASR-Audio-Preprocess
- License: GPL-3.0-or-later
- Full license text: `LICENSE`

## FFmpeg

- Version: 8.1.2
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

- Version: v1.5.3
- Source: https://github.com/gorilla/websocket
- License: BSD-2-Clause
- Full license text: `THIRD_PARTY_LICENSES/Gorilla-WebSocket-BSD-2-Clause.txt`

The Docker runtime also installs the distribution's FFmpeg executable for
streaming file decoding. Its package licenses and build configuration are
provided by Debian; it is separate from the statically linked FFmpeg above.

## Project License

- GPL-3.0-or-later
