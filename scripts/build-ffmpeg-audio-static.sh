#!/usr/bin/env sh
set -eu
ROOT_DIR="$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)"
FFMPEG_DIR="${FFMPEG_DIR:-$ROOT_DIR/third_party/src/ffmpeg-8.1.2}"
PREFIX="${PREFIX:-$ROOT_DIR/third_party/ffmpeg-audio}"
JOBS="${JOBS:-4}"
CC="${CC:-cc}"
PKG_CONFIG_BIN="${PKG_CONFIG_BIN:-pkg-config}"
. "$ROOT_DIR/scripts/audio-components.sh"
cd "$FFMPEG_DIR"
export PKG_CONFIG_PATH="$PREFIX/lib/pkgconfig"
./configure \
  --prefix="$PREFIX" --cc="$CC" --pkg-config="$PKG_CONFIG_BIN" --pkg-config-flags=--static \
  --extra-cflags="-I$PREFIX/include" --extra-ldflags="-L$PREFIX/lib" \
  --enable-static --disable-shared --disable-programs --disable-doc --disable-debug \
  --disable-network --disable-autodetect --disable-everything --disable-iamf --disable-x86asm \
  --disable-avfilter --disable-avdevice --disable-swscale --enable-small --enable-pic \
  --enable-gpl --enable-version3 --enable-avcodec --enable-avformat --enable-avutil --enable-swresample \
  --enable-protocol=file --enable-demuxer="$demuxers" --enable-parser="$parsers" \
  --enable-decoder="$decoders" --enable-encoder="$encoders" --enable-muxer="$muxers" \
  --enable-libopus --enable-libmp3lame --enable-libvorbis --enable-libspeex \
  --enable-libopencore-amrnb --enable-libvo-amrwbenc ${FFMPEG_CONFIGURE_FLAGS:-}
check_components() {
  kind="$1"
  for component in $(printf '%s' "$2" | tr ',' ' '); do
    symbol="CONFIG_$(printf '%s' "$component" | tr '[:lower:]' '[:upper:]')_$kind"
    if ! grep -qx "#define $symbol 1" config_components.h; then
      printf 'Required component was not enabled: %s\n' "$symbol" >&2
      exit 1
    fi
  done
}
check_components DEMUXER "$demuxers"
check_components PARSER "$parsers"
check_components DECODER "$decoders"
check_components ENCODER "$encoders"
check_components MUXER "$muxers"
make -j"$JOBS"
make install
