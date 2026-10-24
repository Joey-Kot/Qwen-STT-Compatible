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

export THIRD_PARTY_DIR="${THIRD_PARTY_DIR:-$ROOT_DIR/third_party}"
export SRC_DIR="${SRC_DIR:-$THIRD_PARTY_DIR/src}"
export PREFIX="${PREFIX:-$THIRD_PARTY_DIR/ffmpeg-audio}"
# Run read-only module scripts through sh; no executable bits or staging needed.
exec sh "$MODULE_DIR/scripts/$1"
