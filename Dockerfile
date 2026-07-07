# syntax=docker/dockerfile:1

FROM golang:1.22-bookworm AS build

ARG TARGETOS
ARG TARGETARCH

WORKDIR /src
COPY go.mod go.sum* /src/
RUN --mount=type=cache,target=/go/pkg/mod go mod download

COPY . /src

RUN apt-get update \
    && apt-get install -y --no-install-recommends build-essential autoconf automake libtool pkg-config curl xz-utils tar ca-certificates \
    && rm -rf /var/lib/apt/lists/*

RUN --mount=type=cache,target=/root/.cache/go-build \
    set -eux; \
    export JOBS="${JOBS:-2}"; \
    ./scripts/bootstrap-static-audio-deps.sh; \
    grep -q '#define CONFIG_ASPLIT_FILTER 1' third_party/src/ffmpeg-*/config_components.h; \
    CGO_ENABLED=1 \
    GOOS="${TARGETOS}" \
    GOARCH="${TARGETARCH}" \
    PKG_CONFIG_PATH="/src/third_party/ffmpeg-audio/lib/pkgconfig" \
    PKG_CONFIG="pkg-config --static" \
    go build -tags libav -trimpath -ldflags="-s -w -linkmode external -extldflags '-static'" -o /out/qwen-stt-compatible ./cmd/server

FROM debian:bookworm-slim

RUN apt-get update \
    && apt-get install -y --no-install-recommends ca-certificates \
    && rm -rf /var/lib/apt/lists/*

COPY --from=build /out/qwen-stt-compatible /usr/local/bin/qwen-stt-compatible

USER 65532:65532
EXPOSE 8080

ENTRYPOINT ["/usr/local/bin/qwen-stt-compatible"]
