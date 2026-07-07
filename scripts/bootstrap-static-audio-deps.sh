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

THIRD_PARTY_DIR="${THIRD_PARTY_DIR:-$ROOT_DIR/third_party}" \
  sh "$MODULE_DIR/scripts/bootstrap-static-audio-deps.sh"
