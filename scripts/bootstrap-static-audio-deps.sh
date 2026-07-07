#!/usr/bin/env sh
set -eu

ROOT_DIR="$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)"
MODULE="github.com/Joey-Kot/ASR-Audio-Preprocess"

cd "$ROOT_DIR"
MODULE_DIR="$(go list -m -f '{{.Dir}}' "$MODULE")"
if [ -n "${MSYSTEM:-}" ]; then
  MODULE_DIR="$(cygpath -u "$MODULE_DIR")"
fi

THIRD_PARTY_DIR="${THIRD_PARTY_DIR:-$ROOT_DIR/third_party}" \
  sh "$MODULE_DIR/scripts/bootstrap-static-audio-deps.sh"
