# syntax=docker/dockerfile:1
FROM rust:1.98.1-bookworm AS build
WORKDIR /src
RUN apt-get update \
    && apt-get install -y --no-install-recommends build-essential autoconf automake libtool pkg-config curl xz-utils tar ca-certificates \
    && rm -rf /var/lib/apt/lists/*
COPY scripts/ /src/scripts/
RUN JOBS=2 ./scripts/bootstrap-static-audio-deps.sh
COPY Cargo.toml Cargo.lock rust-toolchain.toml /src/
COPY src/ /src/src/
ENV PKG_CONFIG_PATH=/src/third_party/ffmpeg-audio/lib/pkgconfig
RUN --mount=type=cache,target=/usr/local/cargo/registry \
    --mount=type=cache,target=/usr/local/cargo/git \
    --mount=type=cache,target=/src/target \
    cargo build --locked --release && cp target/release/qwen-stt-compatible /usr/local/bin/qwen-stt-compatible

FROM debian:bookworm-slim
RUN apt-get update \
    && apt-get install -y --no-install-recommends ca-certificates ffmpeg \
    && rm -rf /var/lib/apt/lists/*
COPY --from=build /usr/local/bin/qwen-stt-compatible /usr/local/bin/qwen-stt-compatible
USER 65532:65532
EXPOSE 8080
ENTRYPOINT ["/usr/local/bin/qwen-stt-compatible"]
