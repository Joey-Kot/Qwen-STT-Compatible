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

# Prepare complete downloads before delegating compilation and GPG verification.
# Keep source defaults aligned with the pinned ASR-Audio-Preprocess module, and
# export them so the downloader and upstream build always use the same files.
export THIRD_PARTY_DIR="${THIRD_PARTY_DIR:-$ROOT_DIR/third_party}"
export SRC_DIR="${SRC_DIR:-$THIRD_PARTY_DIR/src}"
export FFMPEG_VERSION="${FFMPEG_VERSION:-8.1.2}"
export OPUS_VERSION="${OPUS_VERSION:-1.5.2}"
export FFMPEG_ARCHIVE_EXT="${FFMPEG_ARCHIVE_EXT:-tar.gz}"
export FFMPEG_URL="${FFMPEG_URL:-https://ffmpeg.org/releases/ffmpeg-$FFMPEG_VERSION.$FFMPEG_ARCHIVE_EXT}"
export FFMPEG_SIG_URL="${FFMPEG_SIG_URL:-$FFMPEG_URL.asc}"
export FFMPEG_PGP_KEY_URL="${FFMPEG_PGP_KEY_URL:-https://ffmpeg.org/ffmpeg-devel.asc}"
export OPUS_URL="${OPUS_URL:-https://downloads.xiph.org/releases/opus/opus-$OPUS_VERSION.tar.gz}"

sh "$ROOT_DIR/scripts/download-file.sh" "$OPUS_URL" "$SRC_DIR/opus-$OPUS_VERSION.tar.gz" "${OPUS_SHA256:-}"
sh "$ROOT_DIR/scripts/download-file.sh" "$FFMPEG_URL" "$SRC_DIR/ffmpeg-$FFMPEG_VERSION.$FFMPEG_ARCHIVE_EXT" "${FFMPEG_SHA256:-}"
sh "$ROOT_DIR/scripts/download-file.sh" "$FFMPEG_SIG_URL" "$SRC_DIR/ffmpeg-$FFMPEG_VERSION.$FFMPEG_ARCHIVE_EXT.asc"
sh "$ROOT_DIR/scripts/download-file.sh" "$FFMPEG_PGP_KEY_URL" "$SRC_DIR/ffmpeg-devel.asc"

sh "$MODULE_DIR/scripts/bootstrap-static-audio-deps.sh"
