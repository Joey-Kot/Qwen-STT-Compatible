# Qwen STT Compatible

Qwen STT Compatible 是一个 Go 实现的 OpenAI 风格语音转写服务。HTTP 层负责鉴权、上传、分片并发调用 DashScope ASR；音频预处理由 Go 依赖库 [Joey-Kot/ASR-Audio-Preprocess](https://github.com/Joey-Kot/ASR-Audio-Preprocess) 完成。

## 性能测试

测试运行于 AMD Ryzen 9 5950X 虚拟化环境，完整分配 32 个 vCPU，启用 WebDAV 与内存盘模式。测试期间 CPU 峰值尖刺不超过 30%，通常在 6%–15% 之间波动。内存盘与 SSD 的实测差异几乎可以忽略，当前瓶颈不在本地临时文件读写；测试在内网环境完成，文件传输速度略快于公网。

三个样本均截取自电影音频的 `00:10:00`–`00:30:00` 片段，原始时长均为 20 分钟，内容包含人物交替或重叠说话、说话距离和音量变化、环境声与配乐。测试模型为 `qwen3-asr-flash`：英语样本为《钢铁侠 1》（6 声道、48.0 kHz、37.3 MiB、Opus），日语样本为《你的名字》（6 声道、48.0 kHz、39.8 MiB、Opus），中文样本为《让子弹飞》（2 声道、48.0 kHz、11.8 MiB、Opus）。

测试使用以下参数：

```bash
MAX_UPLOAD_MB="500"
UPSTREAM_TIMEOUT_SECONDS="10"
API_CONCURRENCY="15"
API_SEGMENT_LENGTH="175"

FFMPEG_SEGMENT_LENGTH="5"
FFMPEG_WORKS="16"
SEGMENT_WORKERS="0"
LIBAV_CODEC_THREADS="0"
SILENT_INTERVAL="700"
PADDING_LENGTH="100"
OUTPUT_BITRATE="" # 未显式设置，采用默认值 128k

ENABLE_LID="true"
ENABLE_ITN="false"

ASR_RETRY_MAX_ATTEMPTS="3"
ASR_RETRY_INITIAL_DELAY="0.5"
ASR_RETRY_FACTOR="2.0"
ASR_RETRY_MAX_DELAY="8.0"
```

端到端耗时取 `curl` 输出的总耗时；预处理耗时从服务收到请求到输出 `segments merged_duration` 日志计算，按秒取整。裁剪率为裁剪掉的时长占原始时长的比例；端到端倍速为原始时长除以端到端耗时，裁剪后倍速为裁剪后音频时长除以端到端耗时。

| 指标 | 《钢铁侠 1》英语 | 《你的名字》日语 | 《让子弹飞》中文 |
|---|---:|---:|---:|
| 原始时长 | 20m 0.007s | 20m 0.006s | 20m 0.007s |
| 裁剪后时长 | 17m 8.862s | 15m 35.705s | 15m 34.507s |
| 裁剪率 | 14.26% | 22.03% | 22.12% |
| 预处理耗时 | 约 10s | 约 9s | 约 6s |
| 端到端耗时 | 19s | 16s | 11s |
| 端到端倍速 | 63.2× | 75.0× | 109.1× |
| 裁剪后倍速 | 54.2× | 58.5× | 85.0× |
| 准确率 | 95%–96% | 96%–97% | 97%–98% |
| 转写结果 | [查看](testdata/performance/transcripts/ironman1.txt) | [查看](<testdata/performance/transcripts/yourname..txt>) | [查看](<testdata/performance/transcripts/Let the Bullets Fly.txt>) |

准确率以官方原语言字幕为参考，由大模型结合转写结果、人工抽查辅助校对获得。由于官方字幕并非严格逐字稿，校对时会根据实际对白补充或修正字幕内容；统计忽略标点符号、断句和字幕分段差异。该指标用于衡量主要语义内容及文字识别的正确程度，不等同于标准 CER/WER。

## 特性

- 兼容 `POST /v1/audio/transcriptions`
- 支持 Bearer Token 鉴权，`API_TOKEN` 可用逗号配置多个 token
- 预处理链路：转 WAV、固定分片并发静音裁剪、合并、按静音区间并发导出和编码 ASR 分片
- 默认复刻 DashScope Python SDK 的临时 OSS 流程，也支持自建 WebDAV 作为分片存储
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
WEBDAV_URL=""
WEBDAV_CREDENTIALS=""

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
服务向 DashScope、OSS 和 WebDAV 发起的请求会优先协商 HTTP/2，但不复用 keep-alive 连接：每个请求都会新建并在完成后关闭其 TCP/TLS 连接。
`WEBDAV_URL` 和 `WEBDAV_CREDENTIALS` 必须同时设置才会启用 WebDAV；未同时设置时使用 DashScope SDK 的内置临时 OSS。`WEBDAV_CREDENTIALS` 格式为 `user@password`，密码可以包含额外的 `@`。存储链路的工作方式、部署要求和取舍见下方“音频分片存储”。

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
  --webdav-url "https://files.example.com/dav/asr" \
  --webdav-credentials "user@password" \
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
WEBDAV_URL="https://files.example.com/dav/asr" \
WEBDAV_CREDENTIALS="user@password" \
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
| `--webdav-url` | 空 | `WEBDAV_URL` | 公网 HTTPS WebDAV 基础地址；与用户名密码同时设置时启用 |
| `--webdav-credentials` | 空 | `WEBDAV_CREDENTIALS` | WebDAV 用户名密码，格式 `user@password`；建议仅通过环境变量传入 |
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
  -e WEBDAV_URL="https://files.example.com/dav/asr" \
  -e WEBDAV_CREDENTIALS="user@password" \
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

## 音频分片存储与上传

转写前，服务会将处理后的音频切成 ASR 分片。分片可通过 DashScope 的临时 OSS 上传，也可通过自建 WebDAV 提供给百炼。

### 推荐：内存盘模式

推荐将 `/tmp` 挂载为内存盘（tmpfs），让音频预处理和分片产生的临时文件直接写入内存。容量按并发数、音频时长和文件大小上限分配，例如分配 `8G`：

```bash
sudo mount -t tmpfs -o size=8G,mode=1777 tmpfs /tmp
```

内存盘避免了临时分片反复写入 SSD 造成的写放大和损耗；本机上的临时数据只在内存、用户态和内核态之间流转，能显著缩短分片读写时间。

若同时使用自建 WebDAV，建议在转写服务所在主机的 `/etc/hosts` 中将 WebDAV 域名解析到本机，使分片上传走本机回环网络：

```text
127.0.0.1 files.example.com
```

此时 `WEBDAV_URL` 仍使用公网域名（如 `https://files.example.com`）：本机服务会通过回环地址访问 Nginx 和 Dufs，而百炼仍通过该域名的公网解析拉取分片。

### 默认：DashScope 临时 OSS

默认情况下，服务使用内置复刻 DashScope Python SDK 的临时 OSS 流程：

- 上传策略：`GET https://dashscope.aliyuncs.com/api/v1/uploads?action=getPolicy&model=<model>`
- OSS 上传：按策略字段 multipart 上传音频文件，返回 `oss://...`
- 后续 DashScope 请求带 `X-DashScope-OssResourceResolve: enable`

依赖内置 OSS 的策略申请和上传请求。请求频率或限流等因素可能造成偶发阻塞，进而让并发分片等待，拖慢甚至卡住整条转写管线。

### 推荐：自建 WebDAV

同时配置 `WEBDAV_URL` 和 `WEBDAV_CREDENTIALS` 后，服务不再走临时 OSS，而是按以下方式处理每个分片：

1. 服务以 Basic Auth 将分片 `PUT` 到自建 WebDAV。
2. 服务将不含认证信息的 HTTPS 文件 URL 传给百炼。
3. 百炼从该 URL 拉取分片并完成转写。
4. 转写结束后，服务以 Basic Auth 删除对应的临时文件。

WebDAV 应部署为百炼可访问的公网 HTTPS 服务。服务账号需要上传、下载和删除权限；由于百炼接收的是不带认证信息的 URL，分片 URL 在无鉴权时必须可读。

#### 使用 Dufs 搭建 WebDAV

推荐使用 [Dufs](https://github.com/sigoden/dufs) 启动 WebDAV 服务。下面的示例中，`username` 账号拥有根目录的读写权限，匿名用户仅用于读取；Dufs 仅监听本机 `127.0.0.1:6001`，分片保存在 `/tmp`：

```bash
dufs \
  --auth 'username:passwd@/:rw' \
  --auth '@/' \
  -b 127.0.0.1 \
  -p 6001 \
  --allow-upload \
  --allow-delete \
  --allow-search \
  --allow-symlink \
  --allow-archive \
  --enable-cors \
  --render-index \
  --render-try-index \
  --render-spa \
  /tmp
```

再使用 Nginx 将 Dufs 服务反代至公网：

```nginx
server {
    listen 443 ssl;
    server_name files.example.com;

    # 按常规方式配置 ssl_certificate 和 ssl_certificate_key。
    client_max_body_size 500m;

    location / {
        proxy_pass http://127.0.0.1:6001/;
        proxy_set_header Host $host;
        proxy_set_header X-Real-IP $remote_addr;
        proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
        proxy_set_header X-Forwarded-Proto $scheme;
    }
}
```

对应的服务配置如下：

```bash
WEBDAV_URL="https://files.example.com"
WEBDAV_CREDENTIALS="username@passwd"
```

使用 WebDAV 即可绕过内置 OSS 的策略申请和上传环节，避免偶发限流使管线阻塞。将 WebDAV 部署在转写服务的同一设备上，分片写入通常更快；百炼直接从 WebDAV 拉取文件，可将原本两阶段请求的额外传输耗时压缩到接近一阶段请求的耗时。实际效果取决于 WebDAV 与转写服务、百炼之间的网络质量和带宽。

## DashScope 请求说明

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
