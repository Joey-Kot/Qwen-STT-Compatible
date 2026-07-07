#!/usr/bin/env sh
set -eu

ROOT_DIR="$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)"
MODULE="github.com/Joey-Kot/ASR-Audio-Preprocess"

cd "$ROOT_DIR"
MODULE_DIR="$(go list -m -f '{{.Dir}}' "$MODULE")"
if [ -n "${MSYSTEM:-}" ]; then
  MODULE_DIR="$(cygpath -u "$MODULE_DIR")"
fi

FFMPEG_DIR="${FFMPEG_DIR:-$ROOT_DIR/third_party/src/ffmpeg}" \
PREFIX="${PREFIX:-$ROOT_DIR/third_party/ffmpeg-audio}" \
  sh "$MODULE_DIR/scripts/build-ffmpeg-audio-static.sh"
