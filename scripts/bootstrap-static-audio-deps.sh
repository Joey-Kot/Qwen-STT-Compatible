#!/usr/bin/env sh
set -eu

ROOT_DIR="$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)"

# Prepare complete downloads before delegating compilation and GPG verification.
# Keep source defaults aligned with the pinned ASR-Audio-Preprocess module, and
# export them so the downloader and upstream build always use the same files.
export THIRD_PARTY_DIR="${THIRD_PARTY_DIR:-$ROOT_DIR/third_party}"
export SRC_DIR="${SRC_DIR:-$THIRD_PARTY_DIR/src}"
export FFMPEG_VERSION="${FFMPEG_VERSION:-9.0.2}"
export OPUS_VERSION="${OPUS_VERSION:-1.5.2}"
export FFMPEG_SHA256="${FFMPEG_SHA256:-84960df915059e8754fef2cd7c9afeb614062b1b5458ec471eecee619ee04e98}"
export OPUS_SHA256="${OPUS_SHA256:-65c1d2f78b9f2fb20082c38cbe47c951ad5839345876e46941612ee87f9a7ce1}"
export FFMPEG_ARCHIVE_EXT="${FFMPEG_ARCHIVE_EXT:-tar.gz}"
export FFMPEG_URL="${FFMPEG_URL:-https://www.ffmpeg.org/releases/ffmpeg-$FFMPEG_VERSION.$FFMPEG_ARCHIVE_EXT}"
export FFMPEG_SIG_URL="${FFMPEG_SIG_URL:-$FFMPEG_URL.asc}"
export FFMPEG_PGP_KEY_URL="${FFMPEG_PGP_KEY_URL:-https://ffmpeg.org/ffmpeg-devel.asc}"
export OPUS_URL="${OPUS_URL:-https://downloads.xiph.org/releases/opus/opus-$OPUS_VERSION.tar.gz}"

sh "$ROOT_DIR/scripts/download-file.sh" "$OPUS_URL" "$SRC_DIR/opus-$OPUS_VERSION.tar.gz" "${OPUS_SHA256:-}"
sh "$ROOT_DIR/scripts/download-file.sh" "$FFMPEG_URL" "$SRC_DIR/ffmpeg-$FFMPEG_VERSION.$FFMPEG_ARCHIVE_EXT" "${FFMPEG_SHA256:-}"
sh "$ROOT_DIR/scripts/download-file.sh" "$FFMPEG_SIG_URL" "$SRC_DIR/ffmpeg-$FFMPEG_VERSION.$FFMPEG_ARCHIVE_EXT.asc"
sh "$ROOT_DIR/scripts/download-file.sh" "$FFMPEG_PGP_KEY_URL" "$SRC_DIR/ffmpeg-devel.asc"

sh "$ROOT_DIR/scripts/run-audio-dependency-build.sh" bootstrap-static-audio-deps.sh
