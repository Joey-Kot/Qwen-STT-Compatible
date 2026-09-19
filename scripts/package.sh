#!/usr/bin/env sh
set -eu
platform="$1"
arch="$2"
binary="$3"
stage="dist/package-$platform-$arch"
mkdir -p "$stage" dist/upload dist/vendor
cp "$binary" "$stage/"
cp README.md README_ZH.md LICENSE NOTICE THIRD_PARTY_NOTICES.md "$stage/"
cp -R THIRD_PARTY_LICENSES "$stage/"
# Retain the original dependency license/notice directory structure.
cargo fetch --locked
cargo vendor --locked dist/vendor > dist/vendor-config.toml
mkdir -p "$stage/THIRD_PARTY_LICENSES/rust"
find dist/vendor -type f \( -iname '*license*' -o -iname '*copying*' -o -iname '*notice*' \) -print | while IFS= read -r file; do
  relative="${file#dist/vendor/}"
  mkdir -p "$stage/THIRD_PARTY_LICENSES/rust/$(dirname "$relative")"
  cp "$file" "$stage/THIRD_PARTY_LICENSES/rust/$relative"
done
name="qwen-stt-compatible-$platform-$arch"
if [ "$platform" = windows ]; then
  (cd "$stage" && zip -qr "../upload/$name.zip" .)
  (cd dist/upload && sha256sum "$name.zip" > "$name.zip.sha256")
else
  tar -C "$stage" -czf "dist/upload/$name.tar.gz" .
  (cd dist/upload && sha256sum "$name.tar.gz" > "$name.tar.gz.sha256")
fi
