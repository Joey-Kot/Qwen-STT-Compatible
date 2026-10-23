#!/usr/bin/env sh
set -eu

case "${1:-}" in
  bootstrap-static-audio-deps.sh | build-ffmpeg-audio-static.sh) ;;
  *)
    printf 'usage: %s {bootstrap-static-audio-deps.sh|build-ffmpeg-audio-static.sh}\n' "$0" >&2
    exit 2
    ;;
esac

ROOT_DIR="$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)"
MODULE="github.com/Joey-Kot/ASR-Audio-Preprocess"
cd "$ROOT_DIR"
go mod download "$MODULE"
MODULE_DIR="$(go list -m -f '{{.Dir}}' "$MODULE" | tr -d '\r')"
if [ -z "$MODULE_DIR" ]; then
  printf 'failed to resolve module directory for %s\n' "$MODULE" >&2
  exit 1
fi
if [ -n "${MSYSTEM:-}" ] && CYGPATH="$(command -v cygpath)"; then
  MODULE_DIR="$("$CYGPATH" -u "$MODULE_DIR")"
fi

# Go module archives omit executable bits. Stage the pinned upstream scripts
# with executable permissions so their nested patch helper can run, without
# modifying the shared module cache or maintaining a fork of the build scripts.
BUILD_DIR="$(mktemp -d "${TMPDIR:-/tmp}/qwen-stt-audio.XXXXXXXX")"
trap 'rm -rf "$BUILD_DIR"' 0
trap 'exit 1' HUP INT TERM
cp -R "$MODULE_DIR/scripts" "$BUILD_DIR/scripts"
chmod -R u+w "$BUILD_DIR"
chmod u+x "$BUILD_DIR/scripts/"*.sh

export THIRD_PARTY_DIR="${THIRD_PARTY_DIR:-$ROOT_DIR/third_party}"
export SRC_DIR="${SRC_DIR:-$THIRD_PARTY_DIR/src}"
export PREFIX="${PREFIX:-$THIRD_PARTY_DIR/ffmpeg-audio}"
sh "$BUILD_DIR/scripts/$1"
