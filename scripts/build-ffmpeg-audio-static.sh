#!/usr/bin/env sh
set -eu

ROOT_DIR="$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)"
MODULE="github.com/Joey-Kot/ASR-Audio-Preprocess"

cd "$ROOT_DIR"
go mod download "$MODULE"
MODULE_DIR="$(go list -m -f '{{.Dir}}' "$MODULE" | tr -d '\r')"
if [ -z "$MODULE_DIR" ]; then
  echo "failed to resolve module directory for $MODULE" >&2
  exit 1
fi
if [ -n "${MSYSTEM:-}" ] && command -v cygpath >/dev/null 2>&1; then
  MODULE_DIR="$(cygpath -u "$MODULE_DIR")"
fi

FFMPEG_DIR="${FFMPEG_DIR:-$ROOT_DIR/third_party/src/ffmpeg}" \
PREFIX="${PREFIX:-$ROOT_DIR/third_party/ffmpeg-audio}" \
  sh "$MODULE_DIR/scripts/build-ffmpeg-audio-static.sh"
