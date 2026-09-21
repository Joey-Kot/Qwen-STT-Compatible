[English](README.md) | [简体中文](README_ZH.md)

# Qwen STT Compatible

Qwen STT Compatible 是一个 Go 实现的 OpenAI 风格语音转写服务。非实时模型通过 HTTP 调用 DashScope ASR，音频预处理由 Go 依赖库 [Joey-Kot/ASR-Audio-Preprocess](https://github.com/Joey-Kot/ASR-Audio-Preprocess) 完成；实时模型通过 WebSocket 持续发送音频并接收识别结果，支持文件 SSE 和 OpenAI Realtime 转写会话。

## 下载

| Platform | Download | SHA-256 |
|---|---|---|
| Linux x86_64 | [linux-x86_64](https://github.com/Joey-Kot/Qwen-STT-Compatible/releases/download/Latest/qwen-stt-compatible-linux-amd64.tar.gz) | [sha256](https://github.com/Joey-Kot/Qwen-STT-Compatible/releases/download/Latest/qwen-stt-compatible-linux-amd64.tar.gz.sha256) |
| Linux arm64 | [linux-arm64](https://github.com/Joey-Kot/Qwen-STT-Compatible/releases/download/Latest/qwen-stt-compatible-linux-arm64.tar.gz) | [sha256](https://github.com/Joey-Kot/Qwen-STT-Compatible/releases/download/Latest/qwen-stt-compatible-linux-arm64.tar.gz.sha256) |
| Windows x86_64 | [windows-x86_64](https://github.com/Joey-Kot/Qwen-STT-Compatible/releases/download/Latest/qwen-stt-compatible-windows-amd64.zip) | [sha256](https://github.com/Joey-Kot/Qwen-STT-Compatible/releases/download/Latest/qwen-stt-compatible-windows-amd64.zip.sha256) |
| Windows arm64 | [windows-arm64](https://github.com/Joey-Kot/Qwen-STT-Compatible/releases/download/Latest/qwen-stt-compatible-windows-arm64.zip) | [sha256](https://github.com/Joey-Kot/Qwen-STT-Compatible/releases/download/Latest/qwen-stt-compatible-windows-arm64.zip.sha256) |
| macOS x86_64 | [macos-x86_64](https://github.com/Joey-Kot/Qwen-STT-Compatible/releases/download/Latest/qwen-stt-compatible-darwin-amd64.tar.gz) | [sha256](https://github.com/Joey-Kot/Qwen-STT-Compatible/releases/download/Latest/qwen-stt-compatible-darwin-amd64.tar.gz.sha256) |
| macOS arm64 | [macos-arm64](https://github.com/Joey-Kot/Qwen-STT-Compatible/releases/download/Latest/qwen-stt-compatible-darwin-arm64.tar.gz) | [sha256](https://github.com/Joey-Kot/Qwen-STT-Compatible/releases/download/Latest/qwen-stt-compatible-darwin-arm64.tar.gz.sha256) |

## 裁剪性能测试

测试运行于 AMD Ryzen 9 5950X 虚拟化环境，完配 32 个 vCPU，启用 WebDAV 与内存盘模式。测试期间 CPU 峰值尖刺不超过 30%，通常在 6%–15% 之间波动。内存盘与 SSD 的实测差异几乎可以忽略，当前瓶颈不在本地临时文件读写；测试在内网环境完成，文件传输速度略快于公网。

三个样本均截取自电影音频的 `00:10:00`–`00:30:00` 片段，原始时长均为 20 分钟，内容包含人物交替或重叠说话、说话距离和音量变化、环境声与配乐。测试模型为 `qwen3-asr-flash`：英语样本为《钢铁侠 1》（6 声道、48.0 kHz、37.3 MiB、Opus），日语样本为《你的名字》（6 声道、48.0 kHz、39.8 MiB、Opus），中文样本为《让子弹飞》（2 声道、48.0 kHz、11.8 MiB、Opus）。

测试使用以下参数：

```bash
MAX_UPLOAD_MB="500"
UPSTREAM_TIMEOUT_SECONDS="10"
API_CONCURRENCY="15"
API_SEGMENT_LENGTH="175"

FFMPEG_SEGMENT_LENGTH="5"
FFMPEG_WORKS="16"
SKIP_TRIM="false"
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

性能测试均使用 `SKIP_TRIM=false`。端到端耗时取 `curl` 输出的总耗时；预处理耗时从服务收到请求到输出 `segments merged_duration` 日志计算，按秒取整。裁剪率为裁剪掉的时长占原始时长的比例；端到端倍速为原始时长除以端到端耗时，裁剪后倍速为裁剪后音频时长除以端到端耗时。

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

### 接口与鉴权

- 提供 OpenAI 风格的文件转写接口 `POST /v1/audio/transcriptions`，支持 JSON 和 SSE 响应
- 提供 `GET /v1/realtime` WebSocket 转写会话，支持持续音频输入、手动提交、清空和上游 VAD 自动断句
- 支持 Bearer Token 鉴权，`API_TOKEN` 可用逗号配置多个 token

### 模型与流式响应

- 支持 Qwen、Fun-ASR 和 Paraformer 系列的非实时与实时语音识别模型，模型名原样透传给 DashScope；具体型号见[支持模型](#支持模型)
- 非实时模型：并发识别音频分片；`stream=true` 使用伪流式，在全部识别完成后输出 SSE
- 实时模型：通过上游 WebSocket 持续发送音频并接收结果；文件接口启用 `stream=true` 时，按确认的句子或文本前缀持续输出 SSE

### 音频处理与上传

- 非实时音频默认经过转码、固定分片并发静音裁剪、合并，再按静音区间并发导出和编码 ASR 分片；`SKIP_TRIM=true` 或请求 `stream=true` 时跳过裁剪和合并
- 非实时分片统一使用 Ogg + Opus，按模型支持的采样率转换；支持 Base64 和 URL 上传，并校验对应的音频大小限制，详见[音频分片存储与上传](#音频分片存储与上传)
- 实时音频使用连续 PCM 传输，不裁剪静音、不拆成独立识别任务，也不需要音频公网 URL

### 构建与部署

- 提供 Linux、Windows 和 macOS 的 x86_64 / ARM64 构建，支持单个服务二进制文件部署
- 实时文件解码另需 `ffmpeg` 可执行文件；持续 PCM 的 WebSocket 入口不依赖该可执行文件
- 使用反向代理时，文件 SSE 需关闭响应缓冲；`/v1/realtime` 需转发 WebSocket Upgrade 请求头，并设置适合长连接的代理超时

## API

### `POST /v1/audio/transcriptions`

`multipart/form-data` 字段：

- `file`：音频文件
- `model`：模型名，原样透传给 DashScope；支持清单见下方“支持模型”
- `language`：可选，2–3 字母语言码，如 `zh`、`en`、`yue`；实时模型还会按具体型号检查语种支持
- `prompt`：可选；非实时 Qwen3-ASR-Flash 使用 system 上下文，同步 Qwen-Audio-3.x-ASR-Flash / Fun-ASR-Flash 使用 `input_text` 消息，Qwen-Audio-3.x-ASR-Flash-Filetrans / Fun-ASR 使用 `input.context`；实时型号按下方上下文能力限制校验，Qwen3-ASR-Flash-Realtime 和 Paraformer-Realtime 不支持非空 `prompt`
- `enable_lid`：兼容字段，当前支持的模型不会将其传给上游
- `enable_itn`：可选，非实时模型默认读取服务配置 `ENABLE_ITN` / `--enable-itn`；实时模型忽略此字段
- `stream`：可选，默认 `false`；设为 `true` 时返回 SSE，非实时模型同时强制跳过固定切片静音裁剪和合并
- `response_format`：实时模型仅支持省略或 `json`

示例：

```bash
curl -X POST "http://localhost:8080/v1/audio/transcriptions" \
  -H "Authorization: Bearer sk-aaa" \
  -F "file=@demo.wav" \
  -F "model=qwen3-asr-flash" \
  -F "language=zh" \
  -F "enable_itn=false"
```

非流式响应：

```json
{"status":"success","text":"..."}
```

非实时模型使用 `stream=true` 时，本次请求强制按 `SKIP_TRIM=true` 处理，即使服务配置为 `false` 也跳过固定切片静音裁剪和合并。统一转 WAV 后，仍按 `API_SEGMENT_LENGTH` 分片，由 `SEGMENT_WORKERS` 控制分片导出和编码并发，`API_CONCURRENCY` 控制上游识别并发；`FFMPEG_SEGMENT_LENGTH` 和 `FFMPEG_WORKS` 不参与裁剪。此覆盖不修改全局配置，也不影响其他请求。

伪流式响应仍在全部分片识别结束后输出：

```text
data: {"type":"transcript.text.delta","delta":"..."}

data: {"type":"transcript.text.done","text":"..."}

data: [DONE]
```

实时模型使用同一文件接口：

```bash
curl -N -X POST "http://localhost:8080/v1/audio/transcriptions" \
  -H "Authorization: Bearer sk-aaa" \
  -F "file=@demo.wav" \
  -F "model=qwen-audio-3.0-asr-flash-streaming" \
  -F "language=zh" \
  -F "stream=true"
```

使用 Qwen-ASR-Realtime 系列时，将示例中的模型替换为 `qwen3-asr-flash-realtime` 或其日期版本；Paraformer 实时系列可替换为 `paraformer-realtime-v2`、`paraformer-realtime-v1`、`paraformer-realtime-8k-v2` 或 `paraformer-realtime-8k-v1`。实时模型均不需要提供音频公网 URL。

上传完成后，服务通过 FFmpeg 管道连续解码为单声道 PCM16，不裁剪静音、不拆成独立识别任务。Qwen3-ASR-Flash-Realtime 和 `paraformer-realtime-v1` 使用 16000 Hz，Fun-ASR / Paraformer 的 8k 实时型号使用 8000 Hz，其余已支持的实时型号使用 24000 Hz。文件音频按约 100 ms 的块、以约 1 倍音频速度发送；这是一项保守的发送策略，长文件不会获得非实时分片并发识别的处理倍速。

实时 SSE 持续输出确认文本，最后返回全文，不额外发送 `[DONE]`：

```text
data: {"type":"transcript.text.delta","delta":"第一句。"}

data: {"type":"transcript.text.delta","delta":"第二句。"}

data: {"type":"transcript.text.done","text":"第一句。第二句。"}
```

Fun-ASR 和 Paraformer 协议只输出最终确认的句子，不将可能改写的中间全文拼接为增量。Qwen-ASR 协议使用上游 VAD，在文件发送过程中按 `text` 已确认前缀输出新增部分，收到最终 `transcript` 后补齐余下文本；可能修订的 `stash` 草稿不输出。Qwen 多个项目的结果即使乱序到达，文件全文仍按语音顺序拼接，必要时暂存后续项目的结果。

流中每 15 秒发送一次 SSE 注释保活。开始输出前的错误返回 HTTP JSON；开始输出后的错误通过 `type=error` 事件通知并结束，不再发送成功的 `transcript.text.done`。`stream=false` 使用相同的实时上游链路，结束后返回完整 JSON。

### `GET /v1/realtime`

WebSocket 地址：`ws://localhost:8080/v1/realtime?intent=transcription`。生产部署通过 TLS 反向代理使用 `wss://`。鉴权沿用 `Authorization: Bearer <API_TOKEN>`，也支持 `x-api-key`；不通过 URL 查询参数接收密钥。可协商 `realtime` 子协议，默认执行同源 Origin 检查。

采用 OpenAI Realtime 的 `session.update` 转写会话结构。连接后先接收 `session.created`，再发送配置：

```json
{
  "type": "session.update",
  "session": {
    "type": "transcription",
    "audio": {
      "input": {
        "format": {"type": "audio/pcm", "rate": 24000},
        "transcription": {
          "model": "qwen-audio-3.0-asr-flash-streaming",
          "language": "zh",
          "prompt": "专有词：通义千问"
        },
        "turn_detection": null,
        "noise_reduction": null
      }
    }
  }
}
```

收到 `session.updated` 后，可持续发送音频：

```json
{"type":"input_audio_buffer.append","audio":"<Base64 编码的 PCM16 音频块>"}
```

音频必须为 **24000 Hz、单声道、16 位小端 PCM**，不包含 WAV 文件头，每块按完整的 16 位样本对齐。Fun-ASR 普通实时型号和 `paraformer-realtime-v2` 解码 Base64 后直接发送上游；8k 型号使用带低通滤波的连续重采样转换为 8000 Hz，`paraformer-realtime-v1` 转换为 16000 Hz。Qwen3-ASR-Flash-Realtime 则连续重采样为 16000 Hz，再按块编码为 Base64 发送，重采样状态均跨音频块保留。

选择 Qwen-ASR-Realtime 时，可将会话示例中的 `transcription` 替换为以下内容，其余输入格式不变；不要携带原示例的非空 `prompt`：

```json
{"model":"qwen3-asr-flash-realtime","language":"zh"}
```

Paraformer 实时系列同样不携带非空 `prompt`，例如：

```json
{"model":"paraformer-realtime-v2","language":"zh"}
```

默认 `turn_detection=null`，由客户端手动提交，每轮至少 100 ms：

```json
{"type":"input_audio_buffer.commit"}
```

服务返回 `input_audio_buffer.committed` 和 `conversation.item.added`，随后返回该轮的 `conversation.item.input_audio_transcription.delta`，最后发送 `conversation.item.input_audio_transcription.completed`。Fun-ASR 和 Paraformer 协议将提交前收到的确认句子暂存在服务端，提交后输出，并将同一轮的多个句子合并为最终文本；Qwen-ASR 协议关闭上游 VAD，手动提交后接收确认前缀增量和最终文本，不输出草稿。

提交不关闭客户端连接，可以继续发送下一轮音频。使用 `item_id` 关联结果，使用 `previous_item_id` 确认轮次顺序；不同轮次可能按不同顺序完成。若前一轮 Qwen VAD 仍在刷新尾部，后续轮次可以继续发送音频，但提交确认和识别结果会等待前一轮的项目边界确定后再输出，避免轮次顺序错乱。

```json
{"type":"input_audio_buffer.clear"}
```

清空返回 `input_audio_buffer.cleared`，取消当前尚未提交的音频并忽略迟到结果；已经提交的轮次继续处理。Qwen VAD 的同一上游连接可能同时包含已提交项目和未提交尾部：此时服务保留已提交项目并等待其完成，丢弃未提交尾部的结果，下一轮使用新上游连接。

如需边说边自动返回结果，将 `audio.input.turn_detection` 设置为：

```json
{"type":"server_vad","silence_duration_ms":1300}
```

`silence_duration_ms` 支持 200–6000。Fun-ASR 和 Paraformer v2 协议映射到上游 `max_sentence_silence`：首次收到句子结果时返回 `input_audio_buffer.speech_started`，最终确认时返回 `speech_stopped`、自动提交和转写结果。Qwen-ASR 协议映射到同名参数，分别转换上游语音开始、语音结束、提交和识别事件；VAD 阈值固定使用上游推荐的 `0.0`，不开放客户端 `threshold` 参数。

Paraformer v1 两个型号不支持自定义静音阈值；开启自动断句时只传 `{"type":"server_vad"}`，使用上游默认断句行为。显式传入 `silence_duration_ms` 会返回错误，服务返回的 v1 会话配置也不包含该字段。从其他型号切换到 v1 时，如先前设置过静音阈值，需要同时将 `turn_detection` 更新为 `null` 或只含 `type` 的对象。

仍可手动 `commit` 刷出尚未确认的尾部音频。Qwen VAD 模式下，这会发送上游 `session.finish`，等待尾句及 `session.finished`，不会发送上游禁止的 `input_audio_buffer.commit`；下游提交确认以实际返回的项目为准，没有检测到语音时可能没有新项目。此模式使用阿里云的 VAD，不保证与 OpenAI 的断句时机完全一致。

VAD 断句后，手动提交的 100 ms 下限按已确认语音边界之后的音频计算：Fun-ASR / Paraformer 使用最终结果 `end_time`，Qwen-ASR 使用 `speech_stopped.audio_end_ms`，均换算到客户端 24000 Hz 样本计数。Paraformer 的结束时间缺失或无效时，尝试使用有效的字级结束时间；仍无法确定边界时，VAD 模式返回错误并关闭会话，不伪造提交时间。边界事件迟到时，已发送到上游但位于该边界之后的音频仍保留；空缓冲或不足 100 ms 的提交返回错误，不结束当前上游任务。

#### 兼容范围与限制

##### 接口能力

- 仅实现转写会话，支持 `session.update` 和音频 `append` / `commit` / `clear`。
- 不支持旧版 `transcription_session.update`、WebRTC、临时客户端密钥、语音生成和工具调用。
- 不支持 `semantic_vad`、VAD `threshold` / `prefix_padding_ms`、降噪或 logprobs；不支持的字段和事件返回错误。

##### 音频输入

- WebSocket 仅支持上述 24000 Hz、单声道 PCM16 输入，不支持 G.711。
- 实时音频总大小不套用非实时 Base64 10 MiB 或 URL 2 GiB 限制；文件入口仍受 `MAX_UPLOAD_MB` 限制。
- Qwen 上游非 VAD 单次 `append.audio` 的 Base64 上限为 15 MiB。服务实际拆为最多 3200 字节 PCM 的小块并校验编码后大小；客户端消息仍受下表的 1 MiB 限制。

##### 会话配置

- 支持局部更新。音频输入期间只能更新支持上下文的型号的 `prompt`；更换模型、语言或断句配置需先 `commit` 或 `clear`。
- `prompt` 最多 400 个字符，仅 `qwen-audio-3.x-asr-flash-streaming` 系列、`fun-asr-realtime`、`fun-asr-realtime-2025-11-07` 支持；其他型号传非空 `prompt` 返回错误。

##### 容量与并发限制

| 对象 | 上限 |
|---|---|
| 单条客户端 JSON 消息（包含 Base64 文本和 JSON 字段） | 1 MiB |
| 每个会话尚未结束的上游任务（包含当前输入任务） | 4 个 |
| Fun-ASR / Paraformer 手动提交的单轮累计文本 | 1 MiB |
| Qwen-ASR 每个项目的文本 | 1 MiB |
| 实时文件转写全文 | 8 MiB |
| Qwen 单个上游连接累计处理的项目 | 4096 个 |
| Qwen 单个上游连接同时未完成的项目 | 64 个，确认文本合计不超过 8 MiB |
| 等待前一轮 Qwen VAD 尾部时，每个后续任务缓存的结果 | 64 个事件、8 MiB 文本数据 |

达到上游任务并发上限后，需等待任务结束并重新发送被拒绝的音频。Qwen 长会话可通过 `commit` 结束当前上游连接后继续下一轮。等待前轮尾部时，相邻的同项目确认前缀更新会合并；超出缓存保护限制返回错误，不无限缓存。

##### 异常与断开

- 上游连接或协议失败：发送错误事件并关闭会话，不自动重连或重放音频；已提交的未完成条目还会返回 `conversation.item.input_audio_transcription.failed`。
- Qwen 单项识别失败：仅返回该项 `failed`，不报告成功完成；其他项目和客户端会话可以继续。
- 客户端断开：取消所有未结束任务。

接口结构参考 [OpenAI 文件转写](https://developers.openai.com/api/docs/guides/speech-to-text) 和 [OpenAI Realtime 转写](https://developers.openai.com/api/docs/guides/realtime-transcription)，兼容范围以上述实现为准。

## 支持模型

服务不做模型别名转换，`model` 字段会原样透传给 DashScope；内部只按模型名模式选择对应 endpoint 和请求结构。

| 模型前缀 | 示例模型名 | 调用方式 |
|---|---|---|
| `qwen3-asr-flash-realtime*` | `qwen3-asr-flash-realtime`、`qwen3-asr-flash-realtime-2026-02-10`、`qwen3-asr-flash-realtime-2025-10-27` | WebSocket 实时识别，Base64 PCM 音频事件，上游使用 16000 Hz |
| `qwen-audio-3.x-asr-flash-streaming*` | `qwen-audio-3.0-asr-flash-streaming`、`qwen-audio-3.1-asr-flash-streaming` | WebSocket 实时识别，二进制 PCM 音频 |
| `fun-asr-realtime*` | `fun-asr-realtime`、`fun-asr-realtime-2025-11-07`、`fun-asr-realtime-2026-02-28`、`fun-asr-realtime-2025-09-15` | WebSocket 实时识别，二进制 PCM 音频 |
| `fun-asr-flash-8k-realtime*` | `fun-asr-flash-8k-realtime`、`fun-asr-flash-8k-realtime-2026-01-28` | WebSocket 实时识别，上游固定 8000 Hz |
| `paraformer-realtime-v2*` | `paraformer-realtime-v2` | WebSocket 实时识别，二进制 PCM，上游使用 24000 Hz |
| `paraformer-realtime-v1*` | `paraformer-realtime-v1` | WebSocket 实时识别，上游固定 16000 Hz |
| `paraformer-realtime-8k-v2*` / `paraformer-realtime-8k-v1*` | `paraformer-realtime-8k-v2`、`paraformer-realtime-8k-v1` | WebSocket 实时识别，上游固定 8000 Hz |
| `qwen-audio-3.x-asr-flash-filetrans*` | `qwen-audio-3.0-asr-flash-filetrans`、`qwen-audio-3.1-asr-flash-filetrans` | `POST /services/audio/asr/transcription` 异步任务，使用 URL，轮询 `/tasks/<task_id>` |
| `qwen-audio-3.x-asr-flash*` | `qwen-audio-3.0-asr-flash`、`qwen-audio-3.1-asr-flash`、`qwen-audio-3.1-asr-flash-message` | `POST /services/aigc/multimodal-generation/generation`，`input_audio` 请求结构 |
| `qwen3-asr-flash*` | `qwen3-asr-flash`、`qwen3-asr-flash-2025-09-08` | `POST /services/aigc/multimodal-generation/generation`，Qwen3 ASR multimodal 请求结构 |
| `fun-asr-flash*` | `fun-asr-flash-2026-06-15` | `POST /services/aigc/multimodal-generation/generation`，`input_audio` 请求结构 |
| `fun-asr*` | `fun-asr`、`fun-asr-2025-11-07`、`fun-asr-mtl` | `POST /services/audio/asr/transcription` 异步任务，使用 URL，轮询 `/tasks/<task_id>` |
| `paraformer*` | `paraformer-v2`、`paraformer-v1` 等 Paraformer 全量模型名 | `POST /services/audio/asr/transcription` 异步任务，轮询 `/tasks/<task_id>` |

需要使用带日期或版本后缀的模型时，直接传完整模型名即可，例如 `qwen3-asr-flash-2025-09-08` 或 `fun-asr-flash-2026-06-15`。

实时模型名模式优先于非实时 Flash / Fun-ASR / Paraformer 模式匹配。`GET /v1/models` 返回已声明的型号；模型名路由不代表上游一定提供某个任意拼接的版本，具体可用性仍由所选地域和百炼账号决定。

Fun-ASR 协议的实时 `language` 映射为单元素 `language_hints`。普通型号支持 `zh en ja ko vi th id ms tl hi ar fr de es pt ru it nl sv da fi no el pl cs hu ro bg hr sk`；`fun-asr-realtime-2026-02-28` 仅支持 `zh en ja`，`fun-asr-realtime-2025-09-15` 仅支持 `zh en`，8k 实时型号仅支持 `zh`。

Qwen-ASR 协议映射为 `session.input_audio_transcription.language`，支持 `zh yue en ja de ko ru fr pt ar it es hi id th tr uk vi cs da fil fi is ms no pl sv`，不复用 Fun-ASR 的语种列表。省略语言时由上游自动识别。

Paraformer 实时协议的 `language` 映射为单元素 `language_hints`，按文档校验 `zh en ja yue ko de fr ru`，省略时由上游自动识别。该系列仅支持华北 2（北京）地域。`paraformer-realtime-v2` 上游支持任意采样率，当前服务统一使用 24000 Hz；v1 和 8k 型号按表中固定采样率转换。

## 环境变量

```bash
API_TOKEN="sk-aaa,sk-bbb"
DASHSCOPE_API_KEY="sk-xxx"
DASHSCOPE_HTTP_BASE_URL="https://dashscope.aliyuncs.com/api/v1"
DASHSCOPE_WS_URL="wss://dashscope.aliyuncs.com/api-ws/v1/inference"
DASHSCOPE_QWEN_WS_URL="wss://dashscope.aliyuncs.com/api-ws/v1/realtime"
DASHSCOPE_WORKSPACE=""
WEBDAV_URL=""
WEBDAV_CREDENTIALS=""

LISTEN=":8080"
MAX_UPLOAD_MB="500"
UPSTREAM_TIMEOUT_SECONDS="30"

REALTIME_CONCURRENCY="10"
REALTIME_CONNECT_TIMEOUT_SECONDS="10"
REALTIME_START_TIMEOUT_SECONDS="10"
REALTIME_FINISH_TIMEOUT_SECONDS="30"
REALTIME_WRITE_TIMEOUT_SECONDS="10"
REALTIME_IDLE_TIMEOUT_SECONDS="120"

API_CONCURRENCY="10"
API_SEGMENT_LENGTH="175"
FFMPEG_WORKS="16"
FFMPEG_SEGMENT_LENGTH="5"
SKIP_TRIM="false"
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

### 上游地址与连接

HTTP 和两个 WebSocket 地址独立配置，不会相互推导。使用百炼业务空间域名时，将地址中的 `<WorkspaceId>` 替换为实际业务空间 ID：

| 配置 | 用途与业务空间地址 |
|---|---|
| `DASHSCOPE_HTTP_BASE_URL` | 非实时 HTTP；北京：`https://<WorkspaceId>.cn-beijing.maas.aliyuncs.com/api/v1` |
| `DASHSCOPE_WS_URL` | Fun-ASR / Paraformer 实时协议；北京：`wss://<WorkspaceId>.cn-beijing.maas.aliyuncs.com/api-ws/v1/inference`；新加坡：`wss://<WorkspaceId>.ap-southeast-1.maas.aliyuncs.com/api-ws/v1/inference`（仅 Fun-ASR） |
| `DASHSCOPE_QWEN_WS_URL` | Qwen-ASR 实时协议；使用对应地域域名，路径为 `/api-ws/v1/realtime`，服务自动添加或覆盖 `model` 查询参数 |

- Paraformer 实时协议仅支持北京地域，复用 `DASHSCOPE_WS_URL`；默认地址 `wss://dashscope.aliyuncs.com/api-ws/v1/inference` 仍可使用。
- 密钥、业务空间与地域需匹配。`DASHSCOPE_WORKSPACE` 用于发送实时上游的 `X-DashScope-WorkSpace` 请求头。
- 非实时链路向 DashScope、OSS 和 WebDAV 发起的请求优先协商 HTTP/2，但不复用 keep-alive 连接：每个请求新建 TCP/TLS 连接，完成后关闭。实时链路在一个上游任务期间保持 WebSocket 连接。

### 上传与存储

- `MAX_UPLOAD_MB` 控制单个上传音频文件的大小上限，默认 `500` MiB，可用 `--max-upload-mb` 覆盖。
- `WEBDAV_URL` 和 `WEBDAV_CREDENTIALS` 同时设置时启用 WebDAV；否则，URL 输入模型使用 DashScope SDK 的内置临时 OSS。
- `WEBDAV_CREDENTIALS` 格式为 `user@password`，密码可以包含额外的 `@`。
- WebDAV 配置不影响直接使用 Base64 Data URI 的 Qwen3-ASR-Flash、Qwen-Audio-3.x-ASR-Flash（非 Filetrans）和 Fun-ASR-Flash。

存储链路的工作方式与部署要求见[音频分片存储与上传](#音频分片存储与上传)。

### 非实时音频处理

ASR 分片统一使用 `ogg` 容器和 `libopus` 编码，不按原始文件扩展名做模型格式白名单校验。`paraformer-8k*` 使用 8000 Hz，其他非实时模型使用 16000 Hz。

| 配置 | 作用与默认行为 |
|---|---|
| `OUTPUT_BITRATE` / `--output-bitrate` | Opus 码率，默认 `128k` |
| `SEGMENT_WORKERS` / `--segment-workers` | ASR 分片导出和编码并发数；`0` 表示由预处理库按 CPU 自动选择 |
| `LIBAV_CODEC_THREADS` / `--libav-codec-threads` | 每条 libav pipeline 的 decoder/encoder 线程数；`0` 表示使用 libav 默认策略 |
| `SKIP_TRIM` / `--skip-trim` | 默认 `false`；设为 `true` 或 `1` 时跳过固定切片静音裁剪和合并，统一转 WAV 后直接按静音区间分片 |

调大并发或线程数时，需要同时考虑 `FFMPEG_WORKS`，避免 Go worker 和 libav codec 线程叠加后过量并发。

非实时模型的 `stream=true` 请求强制跳过裁剪和合并；`stream=false` 或省略时遵循 `SKIP_TRIM` 配置。实时模型的处理链路不受此配置影响。

### 识别选项与鉴权

- `ENABLE_LID` / `enable_lid` 仅作为兼容配置保留，当前支持的模型不会将其传给上游。
- `ENABLE_ITN` 控制非实时请求未传 `enable_itn` 时的默认值；请求字段一旦传入，会覆盖服务配置默认值。
- 生产部署建议通过环境变量传入 `API_TOKEN` 和 `DASHSCOPE_API_KEY`，避免密钥出现在进程命令行里；本地测试也可使用 `--api-token` 和 `--dashscope-api-key`。

### 配置作用域

| 作用域 | 配置 |
|---|---|
| 通用 | `LISTEN`、`API_TOKEN`、`DASHSCOPE_API_KEY`；`MAX_UPLOAD_MB` 作用于文件上传入口 |
| 非实时 | `DASHSCOPE_HTTP_BASE_URL`、`WEBDAV_URL`、`WEBDAV_CREDENTIALS`、`UPSTREAM_TIMEOUT_SECONDS`、`API_CONCURRENCY` |
| 非实时音频处理 | `API_SEGMENT_LENGTH`、`FFMPEG_SEGMENT_LENGTH`、`FFMPEG_WORKS`、`SKIP_TRIM`、`SEGMENT_WORKERS`、`LIBAV_CODEC_THREADS`、`SILENT_INTERVAL`、`PADDING_LENGTH`、`OUTPUT_BITRATE` |
| 非实时识别选项 | `ENABLE_LID`、`ENABLE_ITN`、全部 `ASR_RETRY_*`；实时模式不读取这些选项 |
| 实时 | `DASHSCOPE_WS_URL`（Fun-ASR / Paraformer 协议）、`DASHSCOPE_QWEN_WS_URL`（Qwen-ASR 协议）、`DASHSCOPE_WORKSPACE`、全部 `REALTIME_*` |

`REALTIME_CONCURRENCY` 同时限制实时文件请求和客户端 WebSocket 会话数，超出返回 HTTP 429，不排队。一个 WebSocket 会话最多对应 4 个尚未结束的上游任务。实时超时分别限制握手、启动等待、结束等待和单次写入，不作为整场录音的总时限；`REALTIME_IDLE_TIMEOUT_SECONDS` 限制客户端 WebSocket 连续未发送消息的时间。

非实时重试参数及预处理并发参数的范围检查在非实时请求执行时进行，不阻止仅使用实时识别的服务启动。命令行参数本身的类型和语法错误仍在启动时返回。

## 本地构建

实时文件接口需要系统 `PATH` 中提供 `ffmpeg`。持续 PCM 的 WebSocket 入口不依赖 FFmpeg 可执行文件；8k 和 16k 重采样在 Go 中完成。

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
  --skip-trim 0 \
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
SKIP_TRIM="false" \
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
| `--dashscope-ws-url` | `wss://dashscope.aliyuncs.com/api-ws/v1/inference` | `DASHSCOPE_WS_URL` | Fun-ASR / Paraformer 协议上游 WebSocket 地址，与 HTTP 地址独立 |
| `--dashscope-qwen-ws-url` | `wss://dashscope.aliyuncs.com/api-ws/v1/realtime` | `DASHSCOPE_QWEN_WS_URL` | Qwen-ASR 协议上游 WebSocket 地址，自动附加模型查询参数 |
| `--dashscope-workspace` | 空 | `DASHSCOPE_WORKSPACE` | 实时上游业务空间请求头 |
| `--realtime-concurrency` | `10` | `REALTIME_CONCURRENCY` | 实时文件请求和 WebSocket 会话并发数，超出返回 429 |
| `--realtime-connect-timeout` | `10s` | `REALTIME_CONNECT_TIMEOUT_SECONDS` | 实时上游握手超时 |
| `--realtime-start-timeout` | `10s` | `REALTIME_START_TIMEOUT_SECONDS` | 等待 `task-started` 或 Qwen 会话创建及更新确认的超时 |
| `--realtime-finish-timeout` | `30s` | `REALTIME_FINISH_TIMEOUT_SECONDS` | 等待 `task-finished` 或 `session.finished` 的超时 |
| `--realtime-write-timeout` | `10s` | `REALTIME_WRITE_TIMEOUT_SECONDS` | 实时上游和客户端单次写入超时 |
| `--realtime-idle-timeout` | `120s` | `REALTIME_IDLE_TIMEOUT_SECONDS` | 客户端 WebSocket 无消息输入超时 |
| `--webdav-url` | 空 | `WEBDAV_URL` | 公网 HTTPS WebDAV 基础地址；与用户名密码同时设置时启用 |
| `--webdav-credentials` | 空 | `WEBDAV_CREDENTIALS` | WebDAV 用户名密码，格式 `user@password`；建议仅通过环境变量传入 |
| `--max-upload-mb` | `500` | `MAX_UPLOAD_MB` | 单个上传音频文件大小上限，单位 MiB |
| `--upstream-timeout` | `30s` | `UPSTREAM_TIMEOUT_SECONDS` | 非实时 DashScope HTTP 请求超时时间 |
| `--api-concurrency` | `10` | `API_CONCURRENCY` | 非实时 ASR 上游并发请求数，超出后排队 |
| `--api-segment-length` | `175s` | `API_SEGMENT_LENGTH` | 单个 ASR 分片最大时长 |
| `--fixed-slice-length` | `5s` | `FFMPEG_SEGMENT_LENGTH` | 固定分片静音裁剪的切片长度 |
| `--fixed-slice-workers` | `16` | `FFMPEG_WORKS` | 固定分片静音裁剪并发数 |
| `--skip-trim` | `false` | `SKIP_TRIM` | 跳过固定分片静音裁剪和合并，统一转码后直接按静音区间分片；支持 `0/1` 或 `true/false`；非实时 `stream=true` 请求强制启用 |
| `--segment-workers` | `0` | `SEGMENT_WORKERS` | ASR 分片导出和编码并发数，`0` 表示按 CPU 自动选择 |
| `--libav-codec-threads` | `0` | `LIBAV_CODEC_THREADS` | 单个 libav pipeline 的 decoder/encoder 线程数，`0` 表示 libav 默认策略 |
| `--silent-interval` | `700ms` | `SILENT_INTERVAL` | 最短静音判定时长 |
| `--padding` | `100ms` | `PADDING_LENGTH` | 非静音片段前后保留时长 |
| `--output-bitrate` | `128k` | `OUTPUT_BITRATE` | ASR 分片输出音频码率 |
| `--enable-lid` | `true` | `ENABLE_LID` | 兼容配置，当前支持的模型不会将其传给上游 |
| `--enable-itn` | `false` | `ENABLE_ITN` | 请求未传 `enable_itn` 时的默认值，支持 `0/1` 或 `true/false` |
| `--asr-retry-max-attempts` | `4` | `ASR_RETRY_MAX_ATTEMPTS` | ASR 调用最大尝试次数 |
| `--asr-retry-initial-delay` | `500ms` | `ASR_RETRY_INITIAL_DELAY` | ASR 重试初始等待时间 |
| `--asr-retry-factor` | `2.0` | `ASR_RETRY_FACTOR` | ASR 重试指数退避倍数 |
| `--asr-retry-max-delay` | `8s` | `ASR_RETRY_MAX_DELAY` | ASR 重试最大等待时间 |

Release packages include the executable, `README.md`, `LICENSE`, `NOTICE`,
`THIRD_PARTY_NOTICES.md`, and the complete third-party license texts in
`THIRD_PARTY_LICENSES/`.

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

实时文件请求改为记录 `mode=realtime` 和上游 `sample_rate`，不输出分片、裁剪日志；WebSocket 会话记录 `session=<session_id>` 的连接和关闭事件。实时 PCM 数据通过管道或内存传输，不生成 Ogg 分片，也不上传 OSS / WebDAV。

非实时非流式请求在 `SKIP_TRIM=false` 时，固定切片静音裁剪成功会输出：

```text
fixed trim input_duration=<音频文件原始长度> fixed_slice_length=<固定切片长度> slices=<成功切片数量> trimmed_slices=<检测到静音并进行了裁剪的切片数量>
```

默认模式生成 ASR 分片后会输出：

```text
segments merged_duration=<切片合并后音频长度> asr_segments=<并发 ASR 分片数量>
```

非实时请求配置 `SKIP_TRIM=true` 或请求 `stream=true` 时，不会输出固定裁剪日志，直接分片后会输出：

```text
segments skip_trim=true input_duration=<统一转码后音频长度> asr_segments=<并发 ASR 分片数量>
```

## 音频分片存储与上传

本节仅适用于非实时模型。转写前，服务会将处理后的音频切成 `ogg + Opus` ASR 分片。Qwen3-ASR-Flash、Qwen-Audio-3.x-ASR-Flash、Fun-ASR-Flash 的分片会编码为 Base64 Data URI，编码后不得超过 10 MiB；Qwen-Audio-3.x-ASR-Flash-Filetrans、Fun-ASR、Paraformer 的分片通过 DashScope 临时 OSS 或自建 WebDAV URL 提供给百炼，文件不得超过 2 GiB。大小超限直接返回错误，不进入识别重试。

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

### `qwen-audio-3.x-asr-flash-streaming*` / `fun-asr-realtime*` / `fun-asr-flash-8k-realtime*`

连接 `DASHSCOPE_WS_URL`，握手时发送 `Authorization: Bearer <DASHSCOPE_API_KEY>`，可选发送 `X-DashScope-WorkSpace`。

```json
{
  "header": {
    "action": "run-task",
    "task_id": "<UUID>",
    "streaming": "duplex"
  },
  "payload": {
    "task_group": "audio",
    "task": "asr",
    "function": "recognition",
    "model": "qwen-audio-3.0-asr-flash-streaming",
    "parameters": {
      "format": "pcm",
      "sample_rate": 24000,
      "heartbeat": true,
      "language_hints": ["zh"]
    },
    "input": {}
  }
}
```

收到 `task-started` 后才发送二进制 PCM，同时读取 `result-generated`。带 `heartbeat=true` 的结果不输出文本；`sentence_end=true` 表示该句最终结果，同一句最终结果只处理一次。

音频发送结束后发送 `finish-task`，继续接收尾句，直到 `task-finished`。实时链路不使用 `ASR_RETRY_*`，失败后不会自动重放音频。支持上下文的型号将 `prompt` 放入 `input.context` 的 `user/input_text` 消息，运行期间通过 `continue-task` 更新。

上游格式参考 [实时 WebSocket API](https://docs.bailian.console.aliyun.com/zh/model-studio/fun-asr-realtime-websocket-api)、[客户端事件](https://docs.bailian.console.aliyun.com/zh/model-studio/fun-asr-client-events)和[服务端事件](https://docs.bailian.console.aliyun.com/zh/model-studio/fun-asr-server-events)。

### `paraformer-realtime-v2*` / `paraformer-realtime-v1*` / `paraformer-realtime-8k-v2*` / `paraformer-realtime-8k-v1*`

复用 `DASHSCOPE_WS_URL` 和上述 `run-task` / `finish-task` 流程，鉴权和业务空间请求头相同。以下为 v2 的请求示例：

```json
{
  "header": {
    "action": "run-task",
    "task_id": "<UUID>",
    "streaming": "duplex"
  },
  "payload": {
    "task_group": "audio",
    "task": "asr",
    "function": "recognition",
    "model": "paraformer-realtime-v2",
    "parameters": {
      "format": "pcm",
      "sample_rate": 24000,
      "heartbeat": true,
      "language_hints": ["zh"]
    },
    "input": {}
  }
}
```

`input` 固定为 `{}`，不发送上下文或 `continue-task`。v2 开启心跳，按客户端配置发送 `max_sentence_silence`；v1 两个型号不发送这两个参数。语义断句、语气词过滤、标点和 ITN 保持上游默认，不使用非实时 `ENABLE_ITN` 配置；暂不开放 `vocabulary_id` 等专属参数，也不透传情感标签和字级结果。

Paraformer 结果没有文档定义的 `sentence_id`、`sentence_begin`。服务根据 `begin_time` 和句子状态生成内部编号，过滤心跳及已经确认的重复结果，只输出 `sentence_end=true` 的最终文本。句子尚未确认就切换开始时间，或 `task-finished` 到达时仍有未确认句子，会返回协议错误，避免遗漏文本后报告成功。字级时间戳仅在句子结束时间无效时用于恢复 VAD 边界；文件输出和手动提交不依赖该时间戳拼接文本。

上游格式参考 [Paraformer 实时 WebSocket API](https://docs.bailian.console.aliyun.com/zh/model-studio/websocket-for-paraformer-real-time-service)、[客户端事件](https://docs.bailian.console.aliyun.com/zh/model-studio/paraformer-client-events)和[服务端事件](https://docs.bailian.console.aliyun.com/zh/model-studio/paraformer-server-events)。

### `qwen3-asr-flash-realtime*`

连接 `DASHSCOPE_QWEN_WS_URL`，服务通过 URL 查询参数 `model` 指定模型，鉴权和业务空间请求头与另一套实时协议相同。先接收 `session.created`，再发送配置并等待 `session.updated`：

```json
{
  "event_id": "event_<ID>",
  "type": "session.update",
  "session": {
    "input_audio_format": "pcm",
    "sample_rate": 16000,
    "input_audio_transcription": {"language": "zh"},
    "turn_detection": {
      "type": "server_vad",
      "threshold": 0.0,
      "silence_duration_ms": 800
    }
  }
}
```

文件入口使用上述 VAD 配置；WebSocket 入口按客户端选择配置 VAD 或 `null`，默认手动模式。音频以不带 Data URI 前缀的 Base64 字符串发送：

```json
{"event_id":"event_<ID>","type":"input_audio_buffer.append","audio":"<Base64 PCM16>"}
```

手动模式结束输入时先发送 `input_audio_buffer.commit`，再发送 `session.finish`；VAD 模式仅发送 `session.finish`。持续接收结果，直到 `session.finished` 才关闭上游连接。项目按 `item_id` 关联并去重，`text` 的确认前缀转换为增量，`completed.transcript` 补齐最终文本；`stash` 不转换为下游增量。如果上游修改已确认的文本前缀，返回协议错误，避免输出无法撤回的错误拼接。

上游支持 16000 / 8000 Hz 的 PCM 或 Opus，当前适配统一使用 16000 Hz PCM16；不将客户端输入改为 Ogg / Opus，也不使用非实时分片上传链路。Qwen-ASR 实时模型不发送 `prompt`、`enable_lid` 或 `enable_itn`。

上游格式参考 [Qwen-ASR 实时 WebSocket API](https://docs.bailian.console.aliyun.com/zh/model-studio/qwen-asr-realtime-interaction-process)、[客户端事件](https://docs.bailian.console.aliyun.com/zh/model-studio/qwen-asr-realtime-client-events)和[服务端事件](https://docs.bailian.console.aliyun.com/zh/model-studio/qwen-asr-realtime-server-events)。型号与日期版本参见[官方模型说明](https://help.aliyun.com/zh/model-studio/qwen3-asr-flash-realtime)。

### `qwen3-asr-flash*`

本节仅指非实时 Flash 型号，不包括 Realtime。使用 DashScope multimodal generation endpoint。每个分片会编码为 Base64 Data URI；Base64 编码结果必须小于或等于 10 MiB，超过时请求返回错误。`prompt` 仅作为识别上下文使用；为空时不发送 system 消息。

- ASR 调用：`POST <DASHSCOPE_HTTP_BASE_URL>/services/aigc/multimodal-generation/generation`

请求体核心结构：

```json
{
  "model": "qwen3-asr-flash",
  "input": {
    "messages": [
      {"role": "system", "content": [{"text": "专有词：通义千问"}]},
      {"role": "user", "content": [{"audio": "data:audio/ogg;base64,..."}]}
    ]
  },
  "parameters": {
    "result_format": "message",
    "asr_options": {
      "enable_itn": false,
      "language": "zh"
    }
  }
}
```

### `qwen-audio-3.x-asr-flash*` / `fun-asr-flash*`

本节仅指非实时 Flash 型号，不包括 Streaming、Realtime 或 Filetrans。使用 multimodal generation endpoint。每个分片会编码为 Base64 Data URI；Base64 编码结果必须小于或等于 10 MiB，超过时请求返回错误。

```json
{
  "model": "qwen-audio-3.0-asr-flash",
  "input": {
    "messages": [
      {
        "role": "user",
        "content": [
          {
            "type": "input_audio",
            "input_audio": {
              "data": "data:audio/ogg;base64,..."
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

### `qwen-audio-3.x-asr-flash-filetrans*` / `fun-asr*` / `paraformer*`

本节仅指非实时异步型号。使用 `dashscope.audio.asr.Transcription.async_call` 同款异步任务：

- 提交任务：`POST <DASHSCOPE_HTTP_BASE_URL>/services/audio/asr/transcription`
- 轮询任务：`GET <DASHSCOPE_HTTP_BASE_URL>/tasks/<task_id>`
- 子任务成功后下载 `transcription_url` 并提取文本
- 这些大文件异步模型不使用 Base64，音频通过 HTTP/HTTPS 公网 URL 或 REST API 支持的临时 `oss://` URL 提供
- `X-DashScope-OssResourceResolve: enable` 仅在使用临时 `oss://` URL 时发送，HTTP/HTTPS URL 不携带该请求头
- `prompt` 会作为 `input.context` 中的 `input_text` 发送给 Qwen-Audio-3.x-ASR-Flash-Filetrans / Fun-ASR；Paraformer 不发送上下文
- `language_hints` 会发送给 Qwen-Audio-3.x-ASR-Flash-Filetrans / Fun-ASR
- `language_hints` 仅对 `paraformer-v2` 发送；Paraformer v1、8k、MTL 等模型不会携带该参数

提交任务请求体：

```json
{
  "model": "qwen-audio-3.0-asr-flash-filetrans",
  "input": {
    "file_urls": ["https://files.example.com/audio.ogg"],
    "context": [
      {"role": "user", "content": [{"type": "input_text", "text": "专有词：通义千问"}]}
    ]
  },
  "parameters": {
    "language_hints": ["zh"]
  }
}
```

## License

This project is licensed under the GNU General Public License v3.0 or later.
See [LICENSE](LICENSE) for details. Third-party dependency notices and complete
license texts are available in [THIRD_PARTY_NOTICES.md](THIRD_PARTY_NOTICES.md)
and [`THIRD_PARTY_LICENSES/`](THIRD_PARTY_LICENSES/).
