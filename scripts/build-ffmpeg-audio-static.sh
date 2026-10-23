#!/usr/bin/env sh
set -eu

ROOT_DIR="$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)"

FFMPEG_DIR="${FFMPEG_DIR:-$ROOT_DIR/third_party/src/ffmpeg-8.1.3}" \
PREFIX="${PREFIX:-$ROOT_DIR/third_party/ffmpeg-audio}" \
  sh "$ROOT_DIR/scripts/run-audio-dependency-build.sh" build-ffmpeg-audio-static.sh
