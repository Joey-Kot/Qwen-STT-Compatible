# Qwen STT Compatible

Qwen STT Compatible 是一个 Go 实现的 OpenAI 风格语音转写服务。HTTP 层负责鉴权、上传、分片并发调用 DashScope ASR；音频预处理由 Go 依赖库 [Joey-Kot/ASR-Audio-Preprocess](https://github.com/Joey-Kot/ASR-Audio-Preprocess) 完成。

## 特性

- 兼容 `POST /v1/audio/transcriptions`
- 支持 Bearer Token 鉴权，`API_TOKEN` 可用逗号配置多个 token
- 预处理链路：转 WAV、固定分片并发静音裁剪、合并、按静音区间并发导出和编码 ASR 分片
- 复刻 DashScope Python SDK 的请求流程：获取 OSS 上传策略、上传音频分片，再按模型前缀调用对应 REST endpoint
- 支持 Qwen3-ASR-Flash、Fun-ASR-Flash、Fun-ASR、Paraformer 系列非实时模型，模型名原样透传给 DashScope
- 支持非流式 JSON 返回，也支持 `stream=true` 的伪 SSE 流式返回
- 产物是单个可运行二进制文件

## API

### `POST /v1/audio/transcriptions`

`multipart/form-data` 字段：

- `file`：音频文件
- `model`：模型名，原样透传给 DashScope；支持清单见下方“支持模型”
- `language`：可选，两字母语言码，如 `zh`、`en`
- `prompt`：可选，作为 system prompt
- `enable_lid`：可选，默认读取服务配置 `ENABLE_LID` / `--enable-lid`
- `enable_itn`：可选，默认读取服务配置 `ENABLE_ITN` / `--enable-itn`
- `stream`：可选，默认 `false`；设为 `true` 时返回 SSE

示例：

```bash
curl -X POST "http://localhost:8080/v1/audio/transcriptions" \
  -H "Authorization: Bearer sk-aaa" \
  -F "file=@demo.wav" \
  -F "model=qwen3-asr-flash" \
  -F "language=zh" \
  -F "enable_lid=true" \
  -F "enable_itn=false"
```

非流式响应：

```json
{"status":"success","text":"..."}
```

伪流式响应：

```text
data: {"type":"transcript.text.delta","delta":"..."}

data: {"type":"transcript.text.done","text":"..."}

data: [DONE]
```

## 支持模型

服务不做模型别名转换，`model` 字段会原样透传给 DashScope；内部只按模型名前缀选择对应 endpoint 和请求结构。

| 模型前缀 | 示例模型名 | 调用方式 |
|---|---|---|
| `qwen3-asr-flash*` | `qwen3-asr-flash`、`qwen3-asr-flash-2025-09-08` | `POST /services/aigc/multimodal-generation/generation`，Qwen3 ASR multimodal 请求结构 |
| `fun-asr-flash*` | `fun-asr-flash-2026-06-15` | `POST /services/aigc/multimodal-generation/generation`，`input_audio` 请求结构 |
| `fun-asr*` | `fun-asr` | `POST /services/audio/asr/transcription` 异步任务，轮询 `/tasks/<task_id>` |
| `paraformer*` | `paraformer-v1` 等 Paraformer 全量模型名 | `POST /services/audio/asr/transcription` 异步任务，轮询 `/tasks/<task_id>` |

需要使用带日期或版本后缀的模型时，直接传完整模型名即可，例如 `qwen3-asr-flash-2025-09-08` 或 `fun-asr-flash-2026-06-15`。

## 环境变量

```bash
API_TOKEN="sk-aaa,sk-bbb"
DASHSCOPE_API_KEY="sk-xxx"
DASHSCOPE_HTTP_BASE_URL="https://dashscope.aliyuncs.com/api/v1"

LISTEN=":8080"
MAX_UPLOAD_MB="500"
UPSTREAM_TIMEOUT_SECONDS="30"

API_CONCURRENCY="10"
API_SEGMENT_LENGTH="175"
FFMPEG_WORKS="16"
FFMPEG_SEGMENT_LENGTH="5"
SEGMENT_WORKERS="0"
LIBAV_CODEC_THREADS="0"
SILENT_INTERVAL="700"
PADDING_LENGTH="100"
OUTPUT_BITRATE="128k"
ENABLE_LID="true"
ENABLE_ITN="false"

ASR_RETRY_MAX_ATTEMPTS="4"
ASR_RETRY_INITIAL_DELAY="0.5"
ASR_RETRY_FACTOR="2.0"
ASR_RETRY_MAX_DELAY="8.0"
```

如果使用百炼业务空间域名，将 `DASHSCOPE_HTTP_BASE_URL` 设置为 `https://<WorkspaceId>.cn-beijing.maas.aliyuncs.com/api/v1`。
`MAX_UPLOAD_MB` 控制单个上传音频文件大小上限，默认 `500`，也可用启动参数 `--max-upload-mb` 覆盖。
ASR 分片会显式输出为 `ogg` 容器、`libopus` 编码、`16000Hz`、`s16` 采样格式；`OUTPUT_BITRATE` / `--output-bitrate` 控制 Opus 码率，默认 `128k`。
`SEGMENT_WORKERS` / `--segment-workers` 控制 ASR 分片导出和编码并发数，`0` 表示由预处理库按 CPU 自动选择；`LIBAV_CODEC_THREADS` / `--libav-codec-threads` 控制每条 libav pipeline 的 decoder/encoder 线程数，`0` 表示使用 libav 默认策略。显式调大时需要同时考虑 `FFMPEG_WORKS`，避免 Go worker 和 libav codec 线程叠加后过量并发。
`ENABLE_LID` 和 `ENABLE_ITN` 分别控制请求未显式传入 `enable_lid`、`enable_itn` 时的默认值；请求字段一旦传入，会覆盖服务配置默认值。
生产部署建议用环境变量传入 `API_TOKEN` 和 `DASHSCOPE_API_KEY`，避免密钥出现在进程命令行里；本地测试也可以使用 `--api-token` 和 `--dashscope-api-key`。

## 本地构建

预处理库需要 `libav` build tag，并且需要 FFmpeg/libav 静态依赖。项目脚本会委托当前 `go.mod` 中 `github.com/Joey-Kot/ASR-Audio-Preprocess` 依赖包提供的构建脚本：

```bash
./scripts/bootstrap-static-audio-deps.sh

CGO_ENABLED=1 \
PKG_CONFIG_PATH="$PWD/third_party/ffmpeg-audio/lib/pkgconfig" \
PKG_CONFIG="pkg-config --static" \
go build -tags libav -trimpath -ldflags="-s -w -linkmode external -extldflags '-static'" -o qwen-stt-compatible ./cmd/server
```

完整启动参数示例：

```bash
./qwen-stt-compatible \
  --api-token "sk-aaa,sk-bbb" \
  --dashscope-api-key "sk-xxx" \
  --listen ":8080" \
  --dashscope-base-url "https://dashscope.aliyuncs.com/api/v1" \
  --max-upload-mb 500 \
  --upstream-timeout 30s \
  --api-concurrency 10 \
  --api-segment-length 175s \
  --fixed-slice-length 5s \
  --fixed-slice-workers 16 \
  --segment-workers 0 \
  --libav-codec-threads 0 \
  --silent-interval 700ms \
  --padding 100ms \
  --output-bitrate "128k" \
  --enable-lid 1 \
  --enable-itn 0 \
  --asr-retry-max-attempts 3 \
  --asr-retry-initial-delay 500ms \
  --asr-retry-factor 2.0 \
  --asr-retry-max-delay 8s
```

生产部署建议把 token 放到环境变量，避免密钥出现在进程命令行：

```bash
API_TOKEN="sk-aaa,sk-bbb" \
DASHSCOPE_API_KEY="sk-xxx" \
OUTPUT_BITRATE="128k" \
ENABLE_LID="true" \
ENABLE_ITN="false" \
./qwen-stt-compatible \
  --listen ":8080" \
  --dashscope-base-url "https://dashscope.aliyuncs.com/api/v1" \
  --max-upload-mb 500 \
  --upstream-timeout 30s \
  --api-concurrency 10 \
  --api-segment-length 175s \
  --fixed-slice-length 5s \
  --fixed-slice-workers 16 \
  --segment-workers 0 \
  --libav-codec-threads 0 \
  --silent-interval 700ms \
  --padding 100ms \
  --output-bitrate "128k" \
  --enable-lid 1 \
  --enable-itn 0 \
  --asr-retry-max-attempts 3 \
  --asr-retry-initial-delay 500ms \
  --asr-retry-factor 2.0 \
  --asr-retry-max-delay 8s
```

启动参数参考：

| 参数 | 默认值 | 对应环境变量 | 说明 |
|---|---:|---|---|
| `--listen` | `:8080` | `LISTEN` | HTTP 监听地址 |
| `--api-token` | 空 | `API_TOKEN` | 兼容接口鉴权 token，多个 token 用逗号分隔 |
| `--dashscope-api-key` | 空 | `DASHSCOPE_API_KEY` | DashScope API Key |
| `--dashscope-base-url` | `https://dashscope.aliyuncs.com/api/v1` | `DASHSCOPE_HTTP_BASE_URL` | DashScope HTTP API base URL |
| `--max-upload-mb` | `500` | `MAX_UPLOAD_MB` | 单个上传音频文件大小上限，单位 MiB |
| `--upstream-timeout` | `30s` | `UPSTREAM_TIMEOUT_SECONDS` | DashScope 请求超时时间 |
| `--api-concurrency` | `10` | `API_CONCURRENCY` | 全局 ASR 上游并发请求数，超出后排队 |
| `--api-segment-length` | `175s` | `API_SEGMENT_LENGTH` | 单个 ASR 分片最大时长 |
| `--fixed-slice-length` | `5s` | `FFMPEG_SEGMENT_LENGTH` | 固定分片静音裁剪的切片长度 |
| `--fixed-slice-workers` | `16` | `FFMPEG_WORKS` | 固定分片静音裁剪并发数 |
| `--segment-workers` | `0` | `SEGMENT_WORKERS` | ASR 分片导出和编码并发数，`0` 表示按 CPU 自动选择 |
| `--libav-codec-threads` | `0` | `LIBAV_CODEC_THREADS` | 单个 libav pipeline 的 decoder/encoder 线程数，`0` 表示 libav 默认策略 |
| `--silent-interval` | `700ms` | `SILENT_INTERVAL` | 最短静音判定时长 |
| `--padding` | `100ms` | `PADDING_LENGTH` | 非静音片段前后保留时长 |
| `--output-bitrate` | `128k` | `OUTPUT_BITRATE` | ASR 分片输出音频码率 |
| `--enable-lid` | `true` | `ENABLE_LID` | 请求未传 `enable_lid` 时的默认值，支持 `0/1` 或 `true/false` |
| `--enable-itn` | `false` | `ENABLE_ITN` | 请求未传 `enable_itn` 时的默认值，支持 `0/1` 或 `true/false` |
| `--asr-retry-max-attempts` | `4` | `ASR_RETRY_MAX_ATTEMPTS` | ASR 调用最大尝试次数 |
| `--asr-retry-initial-delay` | `500ms` | `ASR_RETRY_INITIAL_DELAY` | ASR 重试初始等待时间 |
| `--asr-retry-factor` | `2.0` | `ASR_RETRY_FACTOR` | ASR 重试指数退避倍数 |
| `--asr-retry-max-delay` | `8s` | `ASR_RETRY_MAX_DELAY` | ASR 重试最大等待时间 |

## 运行日志与临时文件

服务启动后会自动清理系统临时目录下的历史请求目录：

```text
<系统临时目录>/qwen-stt-compatible/<request_id>
```

正常请求结束时，也会删除本次请求的临时目录。

每次转写请求会输出请求基础信息，不包含 API token、DashScope API Key 或音频内容：

```text
request=<request_id> endpoint=/v1/audio/transcriptions file=<filename> model=<model> language=<language> enable_lid=<bool> enable_itn=<bool>
```

固定切片静音裁剪成功时会输出：

```text
fixed trim input_duration=<音频文件原始长度> fixed_slice_length=<固定切片长度> slices=<成功切片数量> trimmed_slices=<检测到静音并进行了裁剪的切片数量>
```

生成 ASR 分片后会输出：

```text
segments merged_duration=<切片合并后音频长度> asr_segments=<并发 ASR 分片数量>
```

## Docker

```bash
docker build -t qwen-stt-compatible:latest .
docker run -d \
  -p 8888:8080 \
  --name qwen-stt-compatible \
  --restart always \
  -e API_TOKEN="sk-aaa,sk-bbb" \
  -e DASHSCOPE_API_KEY="sk-xxx" \
  -e DASHSCOPE_HTTP_BASE_URL="https://dashscope.aliyuncs.com/api/v1" \
  -e LISTEN=":8080" \
  -e MAX_UPLOAD_MB="500" \
  -e UPSTREAM_TIMEOUT_SECONDS="30" \
  -e API_CONCURRENCY="10" \
  -e API_SEGMENT_LENGTH="175" \
  -e FFMPEG_WORKS="16" \
  -e FFMPEG_SEGMENT_LENGTH="5" \
  -e SEGMENT_WORKERS="0" \
  -e LIBAV_CODEC_THREADS="0" \
  -e SILENT_INTERVAL="700" \
  -e PADDING_LENGTH="100" \
  -e OUTPUT_BITRATE="128k" \
  -e ENABLE_LID="true" \
  -e ENABLE_ITN="false" \
  -e ASR_RETRY_MAX_ATTEMPTS="3" \
  -e ASR_RETRY_INITIAL_DELAY="0.5" \
  -e ASR_RETRY_FACTOR="2.0" \
  -e ASR_RETRY_MAX_DELAY="8.0" \
  qwen-stt-compatible:latest
```

## DashScope 请求说明

所有本地音频分片都会先按 DashScope SDK 的临时 OSS 流程上传：

- 上传策略：`GET https://dashscope.aliyuncs.com/api/v1/uploads?action=getPolicy&model=<model>`
- OSS 上传：按策略字段 multipart 上传音频文件，返回 `oss://...`
- 后续 DashScope 请求带 `X-DashScope-OssResourceResolve: enable`

### `qwen3-asr-flash*`

DashScope Python SDK 的 `MultiModalConversation.call` 实际请求：

- ASR 调用：`POST <DASHSCOPE_HTTP_BASE_URL>/services/aigc/multimodal-generation/generation`

请求体核心结构：

```json
{
  "model": "qwen3-asr-flash",
  "input": {
    "messages": [
      {"role": "system", "content": [{"text": ""}]},
      {"role": "user", "content": [{"audio": "oss://..."}]}
    ]
  },
  "parameters": {
    "result_format": "message",
    "asr_options": {
      "enable_lid": true,
      "enable_itn": false,
      "language": "zh"
    }
  }
}
```

### `fun-asr-flash*`

使用 multimodal generation endpoint：

```json
{
  "model": "fun-asr-flash-2026-06-15",
  "input": {
    "messages": [
      {
        "role": "user",
        "content": [
          {
            "type": "input_audio",
            "input_audio": {
              "data": "oss://..."
            }
          }
        ]
      }
    ]
  },
  "parameters": {
    "format": "ogg",
    "sample_rate": "16000"
  }
}
```

### `fun-asr*` / `paraformer*`

使用 `dashscope.audio.asr.Transcription.async_call` 同款异步任务：

- 提交任务：`POST <DASHSCOPE_HTTP_BASE_URL>/services/audio/asr/transcription`
- 轮询任务：`GET <DASHSCOPE_HTTP_BASE_URL>/tasks/<task_id>`
- 子任务成功后下载 `transcription_url` 并提取文本

提交任务请求体：

```json
{
  "model": "fun-asr",
  "input": {
    "file_urls": ["oss://..."]
  },
  "parameters": {
    "language_hints": ["zh"]
  }
}
```
