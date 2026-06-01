# Bailian Audio API（百炼语音识别后端服务）

> **一个适合个人/生产的语音转写后端，面向长音频的“静音大幅裁剪 + 低资源占用”预处理，并对纯净音频流进行“尽量不硬切”的并发切片转写，显著提升吞吐与文本连贯性。**

* **兼容 OpenAI 风格接口**：`/v1/audio/transcriptions`
* **兼容自定义上传接口**：`/upload_audio`
* **核心能力**：

  1. **静音裁剪**：对长音频进行“无效静音大幅裁剪”，输出高质量、高密度的有效音频流，并且资源占用低
  2. **切片并发加速转写**：对“纯净音频流”切片后并发识别，加速整体耗时，同时尽量不破坏语义连续性（大多数情况下不会硬切）

## 功能特性

* ✅ 支持多种音频输入（服务端统一转 WAV + 单声道 + 目标采样率）
* ✅ Bearer Token 鉴权（支持多个 token）
* ✅ 自动清理临时文件目录，避免磁盘堆积
* ✅ 支持 Qwen3 ASR 识别路径（静音增强后切片 + 并发识别 + 拼接输出）

## Docker 部署

### 1) Dockerfile 构建镜像

```bash
docker build -t bailian-audio:latest .
```

### 2) 运行容器

#### 方式 1：直接 docker run（推荐）

```bash
docker rm -f bailian-audio >/dev/null 2>&1 || true

docker run -d \
  -p 8888:8080 \
  --name bailian-audio \
  --restart always \
  -e API_TOKEN="sk-aaa,sk-bbb" \
  -e DASHSCOPE_API_KEY="sk-xxx" \
  -e API_SEGMENT_LENGTH="175" \
  -e FFMPEG_WORKS="32" \
  -e FFMPEG_SEGMENT_LENGTH="5" \
  -e SILENT_INTERVAL="700" \
  -e PADDING_LENGTH="100" \
  -e ASR_RETRY_MAX_ATTEMPTS="4" \
  -e ASR_RETRY_INITIAL_DELAY="0.5" \
  -e ASR_RETRY_FACTOR="2.0" \
  -e ASR_RETRY_MAX_DELAY="8.0" \
  -e UVICORN_WORKERS="16" \
  bailian-audio:latest
```

> 优先读取 `DASHSCOPE_API_KEY`，否则读取 `BAILIAN_TOKEN` 作为Token。
> `API_TOKEN` 支持逗号分隔多个 token。

#### 方式 2：使用 `.env` 文件

在宿主机创建 `.env`：

```bash
API_TOKEN="sk-aaa,sk-bbb"
DASHSCOPE_API_KEY="sk-xxx"
API_SEGMENT_LENGTH="175"
FFMPEG_WORKS="32"
FFMPEG_SEGMENT_LENGTH="5"
SILENT_INTERVAL="700"
PADDING_LENGTH="100"
ASR_RETRY_MAX_ATTEMPTS="4"
ASR_RETRY_INITIAL_DELAY="0.5"
ASR_RETRY_FACTOR="2.0"
ASR_RETRY_MAX_DELAY="8.0"
UVICORN_WORKERS="16"
```

运行：

```bash
docker run -d \
  -p 8888:8080 \
  --name bailian-audio \
  --restart always \
  --env-file .env \
  bailian-audio:latest
```

### 3) 验证服务

```bash
curl -s http://localhost:8888/docs | head
```

或直接请求转写接口（示例）：

```bash
curl -X POST "http://localhost:8888/v1/audio/transcriptions" \
  -H "Authorization: Bearer sk-aaa" \
  -F "file=@demo.wav" \
  -F "model=asr" \
  -F "language=zh" \
  -F "enable_lid=true" \
  -F "enable_itn=false"
```

## 快速开始

### 1) 环境依赖

**系统依赖：**

* `ffmpeg`
* `mediainfo`（用于更稳健地获取时长；代码里有 ffprobe/ffmpeg/wave 回退）

**Python 依赖：**

* `fastapi`, `uvicorn`, `python-multipart`, `python-dotenv`
* `dashscope`
* `ffmpeg-python`

### 2) 环境变量配置

创建 `.env`（或用容器环境变量注入）：

```bash
# 鉴权：支持逗号分隔多个 Token
API_TOKEN="sk-aaa,sk-bbb"

# DashScope/百炼 Token（二选一均可，代码优先 DASHSCOPE_API_KEY，否则取 BAILIAN_TOKEN）
DASHSCOPE_API_KEY="sk-xxx"
# 或：
BAILIAN_TOKEN="sk-xxx"

# 静音裁剪/切片相关（可选）
API_CONCURRENCY="10"     # 百炼 API 并发请求数，默认 10
API_SEGMENT_LENGTH="175" # 百炼 API 请求音频最大切片长度(s)，默认 175
FFMPEG_WORKS="16"        # 固定切片并发裁剪线程数，默认 16
FFMPEG_SEGMENT_LENGTH="5" # 固定切片长度(s)，默认 5
SILENT_INTERVAL="700"    # 静音判定的最短持续时间(ms)，默认 700
PADDING_LENGTH="100"     # 非静音片段前后保留填充(ms)，默认 100

# 百炼 API 重试相关（可选）
ASR_RETRY_MAX_ATTEMPTS="4"   # 最大尝试次数，默认 4
ASR_RETRY_INITIAL_DELAY="0.5" # 初始重试等待秒数，默认 0.5
ASR_RETRY_FACTOR="2.0"       # 重试退避倍数，默认 2.0
ASR_RETRY_MAX_DELAY="8.0"    # 最大重试等待秒数，默认 8.0
```

### 3) 启动服务

本地启动（示例）：

```bash
uvicorn bailian-audio-api:app --host 0.0.0.0 --port 8080 --workers 4
```

> `--workers` 按机器核数与负载调整。部署文档提供了 `uvicorn --workers 16` 等参考配置。

## API 使用

### 鉴权

所有接口需要 `Authorization: Bearer <token>`，token 必须在 `API_TOKEN` 环境变量中。

### 1) OpenAI 风格接口：`POST /v1/audio/transcriptions`

**表单字段：**

* `file`：音频文件（multipart/form-data）
* `model`：模型名（必填）
* `language`：可选，两字母语言码，如 `zh` / `en`（会被校验格式）
* `prompt`：可选，主要用于 qwen3-asr 的 system prompt
* `enable_lid`：可选，是否启用语言识别，默认 `true`
* `enable_itn`：可选，是否启用逆文本归一化，默认 `false`

**示例：**

```bash
curl -X POST "http://localhost:8080/v1/audio/transcriptions" \
  -H "Authorization: Bearer sk-aaa" \
  -F "file=@demo.wav" \
  -F "model=asr" \
  -F "language=zh" \
  -F "prompt=请尽量保留口语表达" \
  -F "enable_lid=true" \
  -F "enable_itn=false"
```

**响应：**

```json
{ "status": "success", "text": "..." }
```

### 2) 兼容上传接口：`POST /upload_audio`

字段与返回基本一致：`audio` 作为文件字段名；同样支持 `enable_lid` / `enable_itn` 表单字段。

## 支持的模型与采样率策略

服务支持 Qwen3 ASR 模型，并为每个模型固定采样率（用于转 WAV、编码 Opus 等）。

**常用别名：**

* `asr` → `qwen3-asr-flash`
* `asr-0908` → `qwen3-asr-flash-2025-09-08`

Qwen3 ASR 统一走 `16000` 采样率。

# 核心设计与实现细节

## 1) 静音裁剪：输出高质量高密度音频流（低资源占用）

### 目标

现实输入里常见问题：

* 很长的会议录音/监控录音/屏录里**大量静音、等待、无效段**
* 直接送入识别模型会：耗时长、费用高、吞吐差、结果还容易被噪声/空白影响

因此服务会把输入音频预处理为：

> **“高密度有效语音流”**：尽可能删除长静音，保留必要上下文边界（padding），并保持语音连续、自然。

### 总体流程（高层）

对于任意上传音频：

1. 统一转码为 **单声道 WAV**，并按模型要求重采样
2. 进入“固定切片 + 局部静音裁剪 + 合并”的主策略：`remove_silence_by_fixed_slices_and_merge`
3. 得到一份“静音大幅被删除”的合并 WAV（更短、更密、更干净）

### 为什么采用“固定切片 + 局部裁剪 + 合并”？

不是直接对整段音频做一次 `silencedetect` 然后一次性拼接，而是：

* **先用 ffmpeg segment muxer 一次性切成固定秒数的小片**（例如 5s）
* **对每个小片独立跑静音检测与裁剪**（局部决策）
* **并发处理这些小片**（线程池，数量由 `FFMPEG_WORKS` 控制）
* **把裁剪后的有效小片按顺序合并回去**（优先 concat demuxer，失败再 filter_complex fallback）

这么做的收益很明确：

1. **资源占用更可控**

* 单次 ffmpeg 处理对象变小，内存峰值低
* 并发度可配置（不把机器打爆）

2. **静音阈值自适应更可靠**
   静音检测依赖音量阈值。整段音频如果音量跨度很大，单阈值容易失效。你实现里会先用 `volumedetect` 得到 `max_volume/mean_volume`，再生成一组候选阈值（例如基于 max 减不同 offset），逐个尝试直到检测到静音区间。
   对短片做这套逻辑，往往更稳定。

3. **失败隔离**
   某个切片裁剪失败可以跳过或回退，不影响整段处理结果（最终会过滤掉过短片段）。

### 静音裁剪的关键点

`trim_long_silences_from_wav` 逻辑可以概括为：

1. 用 `ffmpeg -af volumedetect` 估计音量范围（mean/max）
2. 构造一系列 `silencedetect=noise=XdB:d=Y` 阈值尝试序列，直到检测到静音区间
3. 得到静音区间后，做“反转”得到非静音区间
4. 对非静音区间做 **padding 扩展**：前后各保留 `PADDING_LENGTH`（ms）避免语音边界被切坏
5. 用 `atrim + concat` 的 filter_complex 把多个非静音片段无缝拼接成新音频

> 这一步是“高密度音频流”的本质：**在不破坏内容的前提下最大化删除无效静音**。

### 关键参数建议

* `SILENT_INTERVAL`（默认 700ms）：越大越“保守”，更不容易把停顿当静音切掉；越小裁剪更激进。
* `PADDING_LENGTH`（默认 100ms）：建议不要为 0，避免咬字/爆破音被切掉。
* `FFMPEG_SEGMENT_LENGTH`（默认 5s）：切片越短越利于低峰值与局部阈值，但文件数变多；一般 3~8s 是比较稳的区间。
* `API_SEGMENT_LENGTH`（默认 175s）：发起百炼 API 请求前的音频分组最长秒数；越小请求更细碎，越大单次请求音频更长。
* `FFMPEG_WORKS`：按 CPU 核数与负载调，建议从 8/16 起步。

## 2) 纯净音频流切片并发加速转写（尽量不断句、不硬切）

当模型是 Qwen3 ASR（`qwen3-asr-flash*`）时，服务走“并发切片识别”路径：

### 整体思路

1. 先得到 **静音裁剪后的合并 WAV（纯净高密度）**
2. 再基于 `silencedetect` 的结果做“切片分组”，导出每段音频
3. 将每段音频编码为 Opus（ogg/opus）
4. 对每段音频并发调用 ASR
5. **按 index 顺序拼接文本**，输出一份完整转写结果

### “不影响完整性 / 大多数情况下不会硬切”的实现方式

切片逻辑非常关键：`split_wav_by_silence_groups(preserve_internal_silence=True)`

#### A. 切片依据：静音检测驱动的分组

* 先用 silencedetect 找到静音区间
* 再反转得到非静音区间（连续说话的片段）
* 然后把这些非静音区间按时间轴累积成“组”，每组最长由 `API_SEGMENT_LENGTH` 控制（默认 175 秒）

#### B. preserve_internal_silence=True：只按组导出“连续区间”，保留组内静音

* `preserve_internal_silence=True` 表示：**分组仍然参考 silencedetect，但导出音频时不做组内静音删除**
* 导出方式是 `-ss start -to end`，直接导出整段连续区间

这能带来两个核心收益：

1. **组内停顿、语气词等仍被保留**，语义与节奏更自然
2. 避免“过度裁剪”造成的上下文断裂，让 ASR 更稳定

#### C. 尽量不硬切：只有一种情况会切

* 如果某个“非静音 interval”自身长度 **超过 API_SEGMENT_LENGTH**，才会在 interval 内部做硬切（否则不拆 interval，而是把它作为下一组开头）。

### 并发识别与保序拼接

* `recognize_segments_concurrently` 用 `asyncio.Semaphore(max_concurrency)` 控制并发上限
* 实际调用通过 `retry_blocking_call` 包装：失败会指数退避重试（默认最多 4 次）
* 并发返回后按 `index` 排序，最终按顺序拼接文本输出，保证输出文本与音频时间顺序一致

> “并发 + 保序”：吞吐提升明显，同时不牺牲最终文本连贯性。

## 常见问题（FAQ）

### 1) 为什么需要 mediainfo？

用于更稳健地获取外部输入音频的时长；原始上传文件默认优先 mediainfo，失败再回退 ffprobe/ffmpeg/wave。
后续流程里已经转成标准 WAV 的内部文件会优先用 Python `wave` 读取时长，减少子进程调用。

### 2) 为什么要统一转 WAV？

为了后续静音检测与切片处理一致、稳定；并能按模型要求固定采样率。

### 3) Qwen3 ASR 为什么用 Opus？

切片导出后会编码为 `ogg/opus`，再通过 MultiModalConversation 走 audio 输入，整体更适合网络/调用链路。

### 4）额外的错误处理与占位符策略

为了让调用方**显式感知“某段音频转写失败/无输出”**，服务在以下场景会在返回文本中插入占位符：

* 当底层 ASR 返回结果中 `text` 字段存在，但内容为空字符串或仅包含空白字符时
* 服务会将该段文本替换为：`【该段音频转录出错】`

  * **不破坏响应结构**：仍然返回 `{"status":"success","text":"..."}`（当底层调用成功但内容为空时）
  * **利于调用方自动化处理**：调用方可以在最终文本里搜索 `【...】` 快速定位异常段
  * **方便重试/降级**：比如命中占位符后仅对相关切片重试，或切换模型/参数重跑

## 安全与运维建议

* 将 `API_TOKEN` 作为密钥对待，至少 32 位以上随机字符串
* 生产环境建议限制 CORS 源（目前为 `*`）
* 为 FastAPI / Uvicorn 配置反向代理（Nginx）并设置上传大小、超时
* 日志中已带 RequestID（ContextVar），便于排障追踪
