#!/usr/bin/env sh
# Bootstrap static dependencies, then invoke the shared FFmpeg builder.
# An optional first argument reuses a root containing downloaded archives.
set -eu
ROOT_DIR="$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)"
THIRD_PARTY_DIR="${1:-${THIRD_PARTY_DIR:-$ROOT_DIR/third_party}}"
mkdir -p "$THIRD_PARTY_DIR"
THIRD_PARTY_DIR="$(CDPATH= cd -- "$THIRD_PARTY_DIR" && pwd)"
SRC_DIR="${SRC_DIR:-$THIRD_PARTY_DIR/src}"
if [ -n "${1:-}" ]; then SRC_DIR="$THIRD_PARTY_DIR"; fi
PREFIX="${PREFIX:-$THIRD_PARTY_DIR/ffmpeg-audio}"
JOBS="${JOBS:-4}"
mkdir -p "$SRC_DIR" "$PREFIX"
export PREFIX JOBS
export PKG_CONFIG_PATH="$PREFIX/lib/pkgconfig"
fetch() {
  archive="$1"; url="$2"
  if [ ! -f "$SRC_DIR/$archive" ]; then
    curl --fail --show-error --location --retry 2 -o "$SRC_DIR/$archive.part" "$url"
    mv "$SRC_DIR/$archive.part" "$SRC_DIR/$archive"
  fi
  expected="$(awk -v name="$archive" '$2 == name {print $1}' "$ROOT_DIR/scripts/audio-sources.sha256")"
  [ -n "$expected" ] || { printf 'Missing source checksum: %s\n' "$archive" >&2; exit 1; }
  (cd "$SRC_DIR" && printf '%s  %s\n' "$expected" "$archive" | sha256sum --check --strict)
  source_name="${archive%.tar.*}"
  if [ ! -f "$SRC_DIR/$source_name/.extract-ok" ]; then
    tar --no-same-owner -xf "$SRC_DIR/$archive" -C "$SRC_DIR"
    touch "$SRC_DIR/$source_name/.extract-ok"
  fi
}
build_dependency() (
  source_name="$1"; shift
  cd "$SRC_DIR/$source_name"
  if [ "$source_name" = libvorbis-1.3.7 ]; then
    # Vorbis adds this obsolete Darwin flag itself, even with custom CFLAGS.
    # Modern Apple linkers reject it. Keep the generated script and its
    # Autoconf source consistent so regeneration preserves the fix.
    for config_file in configure configure.ac; do
      sed 's/ -force_cpusubtype_ALL//g' "$config_file" > "$config_file.tmp"
      cat "$config_file.tmp" > "$config_file"
      rm "$config_file.tmp"
    done
  fi
  if [ "$source_name" = opus-1.5.2 ]; then
    ./configure --prefix="$PREFIX" --enable-static --disable-shared --with-pic ${DEPENDENCY_CONFIGURE_FLAGS:-} ${OPUS_CONFIGURE_FLAGS:-} "$@"
  else
    ./configure --prefix="$PREFIX" --enable-static --disable-shared --with-pic ${DEPENDENCY_CONFIGURE_FLAGS:-} "$@"
  fi
  make -j"$JOBS"
  make install
)
fetch opus-1.5.2.tar.gz https://downloads.xiph.org/releases/opus/opus-1.5.2.tar.gz
build_dependency opus-1.5.2 --disable-extra-programs --disable-doc
fetch libogg-1.3.5.tar.xz https://downloads.xiph.org/releases/ogg/libogg-1.3.5.tar.xz
build_dependency libogg-1.3.5
fetch libvorbis-1.3.7.tar.xz https://downloads.xiph.org/releases/vorbis/libvorbis-1.3.7.tar.xz
build_dependency libvorbis-1.3.7 --disable-oggtest
fetch lame-3.100.tar.gz https://downloads.sourceforge.net/project/lame/lame/3.100/lame-3.100.tar.gz
build_dependency lame-3.100 --disable-frontend --disable-decoder
fetch opencore-amr-0.1.6.tar.gz https://downloads.sourceforge.net/project/opencore-amr/opencore-amr/opencore-amr-0.1.6.tar.gz
build_dependency opencore-amr-0.1.6
fetch speex-1.2.1.tar.gz https://downloads.xiph.org/releases/speex/speex-1.2.1.tar.gz
build_dependency speex-1.2.1 --disable-binaries --disable-examples
fetch vo-amrwbenc-0.1.3.tar.gz https://downloads.sourceforge.net/project/opencore-amr/vo-amrwbenc/vo-amrwbenc-0.1.3.tar.gz
build_dependency vo-amrwbenc-0.1.3
fetch ffmpeg-8.1.2.tar.gz https://ffmpeg.org/releases/ffmpeg-8.1.2.tar.gz
export FFMPEG_DIR="$SRC_DIR/ffmpeg-8.1.2"
"$ROOT_DIR/scripts/build-ffmpeg-audio-static.sh"
printf 'Static audio dependencies installed at: %s\n' "$PREFIX"
