[English](README.md) | [简体中文](README_ZH.md)

# Qwen STT Compatible

Qwen STT Compatible is an OpenAI-style speech transcription service written in Go. Non-realtime models call DashScope ASR over HTTP, with audio preprocessing handled by the Go dependency [Joey-Kot/ASR-Audio-Preprocess](https://github.com/Joey-Kot/ASR-Audio-Preprocess). Realtime models continuously send audio and receive recognition results over WebSocket, supporting file-based SSE and OpenAI Realtime transcription sessions.

## Downloads

| Platform | Download | SHA-256 |
|---|---|---|
| Linux x86_64 | [linux-x86_64](https://github.com/Joey-Kot/Qwen-STT-Compatible/releases/download/Latest/qwen-stt-compatible-linux-amd64.tar.gz) | [sha256](https://github.com/Joey-Kot/Qwen-STT-Compatible/releases/download/Latest/qwen-stt-compatible-linux-amd64.tar.gz.sha256) |
| Linux arm64 | [linux-arm64](https://github.com/Joey-Kot/Qwen-STT-Compatible/releases/download/Latest/qwen-stt-compatible-linux-arm64.tar.gz) | [sha256](https://github.com/Joey-Kot/Qwen-STT-Compatible/releases/download/Latest/qwen-stt-compatible-linux-arm64.tar.gz.sha256) |
| Windows x86_64 | [windows-x86_64](https://github.com/Joey-Kot/Qwen-STT-Compatible/releases/download/Latest/qwen-stt-compatible-windows-amd64.zip) | [sha256](https://github.com/Joey-Kot/Qwen-STT-Compatible/releases/download/Latest/qwen-stt-compatible-windows-amd64.zip.sha256) |
| Windows arm64 | [windows-arm64](https://github.com/Joey-Kot/Qwen-STT-Compatible/releases/download/Latest/qwen-stt-compatible-windows-arm64.zip) | [sha256](https://github.com/Joey-Kot/Qwen-STT-Compatible/releases/download/Latest/qwen-stt-compatible-windows-arm64.zip.sha256) |
| macOS x86_64 | [macos-x86_64](https://github.com/Joey-Kot/Qwen-STT-Compatible/releases/download/Latest/qwen-stt-compatible-darwin-amd64.tar.gz) | [sha256](https://github.com/Joey-Kot/Qwen-STT-Compatible/releases/download/Latest/qwen-stt-compatible-darwin-amd64.tar.gz.sha256) |
| macOS arm64 | [macos-arm64](https://github.com/Joey-Kot/Qwen-STT-Compatible/releases/download/Latest/qwen-stt-compatible-darwin-arm64.tar.gz) | [sha256](https://github.com/Joey-Kot/Qwen-STT-Compatible/releases/download/Latest/qwen-stt-compatible-darwin-arm64.tar.gz.sha256) |

## Silence Trimming Benchmarks

Tests ran in a virtualized AMD Ryzen 9 5950X environment with all 32 vCPUs allocated, using WebDAV and a RAM disk. CPU spikes stayed below 30%, with typical usage between 6% and 15%. The measured difference between the RAM disk and SSD was negligible; local temporary-file I/O was not the bottleneck. Tests ran on a local network, where file transfers were slightly faster than over the public internet.

All three samples cover the `00:10:00`–`00:30:00` portion of movie audio, with an original duration of 20 minutes each. They include alternating and overlapping dialogue, varying speaker distances and volumes, ambient sound, and music. The model was `qwen3-asr-flash`: the English sample was from *Iron Man* (6 channels, 48.0 kHz, 37.3 MiB, Opus), the Japanese sample from *Your Name* (6 channels, 48.0 kHz, 39.8 MiB, Opus), and the Chinese sample from *Let the Bullets Fly* (2 channels, 48.0 kHz, 11.8 MiB, Opus).

Test configuration:

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
OUTPUT_BITRATE="" # Not explicitly set; uses the default 128k

ENABLE_LID="true"
ENABLE_ITN="false"

ASR_RETRY_MAX_ATTEMPTS="3"
ASR_RETRY_INITIAL_DELAY="0.5"
ASR_RETRY_FACTOR="2.0"
ASR_RETRY_MAX_DELAY="8.0"
```

All benchmarks used `SKIP_TRIM=false`. End-to-end time is the total time reported by `curl`. Preprocessing time is measured from receipt of the request to the `segments merged_duration` log entry, rounded to whole seconds. The trimming ratio is the removed duration divided by the original duration. End-to-end speed is the original audio duration divided by end-to-end time; post-trim speed uses the trimmed audio duration instead.

| Metric | Iron Man (English) | Your Name (Japanese) | Let the Bullets Fly (Chinese) |
|---|---:|---:|---:|
| Original duration | 20m 0.007s | 20m 0.006s | 20m 0.007s |
| Duration after trimming | 17m 8.862s | 15m 35.705s | 15m 34.507s |
| Trimming ratio | 14.26% | 22.03% | 22.12% |
| Preprocessing time | Approx. 10s | Approx. 9s | Approx. 6s |
| End-to-end time | 19s | 16s | 11s |
| End-to-end speed | 63.2× | 75.0× | 109.1× |
| Post-trim speed | 54.2× | 58.5× | 85.0× |
| Accuracy | 95%–96% | 96%–97% | 97%–98% |
| Transcripts | [View](testdata/performance/transcripts/ironman1.txt) | [View](<testdata/performance/transcripts/yourname..txt>) | [View](<testdata/performance/transcripts/Let the Bullets Fly.txt>) |

Accuracy was assessed against official subtitles in the original language, using an LLM to compare transcripts and manual spot checks to assist verification. Because official subtitles are not strictly verbatim, subtitle content was supplemented or corrected against the actual dialogue. Punctuation, sentence boundaries, and subtitle segmentation were ignored. This metric measures the correctness of the main meaning and recognized text; it is not standard CER/WER.

## Features

### Interfaces and Authentication

- OpenAI-style file transcription via `POST /v1/audio/transcriptions`, with JSON and SSE responses
- WebSocket transcription sessions via `GET /v1/realtime`, with continuous audio input, manual commit, clear, and upstream VAD-based automatic segmentation
- Bearer token authentication, with multiple comma-separated tokens supported in `API_TOKEN`

### Models and Streaming

- Supports non-realtime and realtime Qwen, Fun-ASR, and Paraformer speech recognition models, passing model names to DashScope unchanged; see [Supported Models](#supported-models)
- Non-realtime models: recognize audio segments concurrently; `stream=true` uses pseudo-streaming, emitting SSE only after all recognition has finished
- Realtime models: continuously send audio and receive results over upstream WebSocket connections; file requests with `stream=true` emit confirmed sentences or text prefixes as SSE

### Audio Processing and Uploads

- Non-realtime audio is transcoded, trimmed for silence in parallel fixed-length slices, merged, and then split at silence intervals for concurrent ASR segment export and encoding; `SKIP_TRIM=true` or a request with `stream=true` skips trimming and merging
- Non-realtime segments use Ogg + Opus and are converted to the model's supported sample rate; Base64 and URL uploads enforce their respective size limits, as described in [Audio Segment Storage and Uploads](#audio-segment-storage-and-uploads)
- Realtime audio uses continuous PCM transmission without silence trimming, separate recognition tasks, or public audio URLs

### Builds and Deployment

- Linux, Windows, and macOS builds for x86_64 / ARM64, deployable as a single service binary
- Realtime file decoding additionally requires the `ffmpeg` executable; the continuous-PCM WebSocket endpoint does not
- Reverse proxies must disable response buffering for file SSE, forward WebSocket Upgrade headers for `/v1/realtime`, and use timeouts suitable for long-lived connections

## API

### `POST /v1/audio/transcriptions`

`multipart/form-data` fields:

- `file`: audio file
- `model`: model name, passed unchanged to DashScope; see Supported Models below
- `language`: optional 2–3-letter language code, such as `zh`, `en`, or `yue`; realtime models also validate language support for the specific model
- `prompt`: optional; non-realtime Qwen3-ASR-Flash uses system context, synchronous Qwen-Audio-3.x-ASR-Flash / Fun-ASR-Flash use an `input_text` message, and Qwen-Audio-3.x-ASR-Flash-Filetrans / Fun-ASR use `input.context`. Realtime models validate context support as described below; Qwen3-ASR-Flash-Realtime and Paraformer-Realtime reject nonempty `prompt` values
- `enable_lid`: compatibility field; currently supported models do not forward it upstream
- `enable_itn`: optional; non-realtime models default to the service's `ENABLE_ITN` / `--enable-itn` setting; realtime models ignore this field
- `stream`: optional, defaults to `false`; `true` returns SSE and also forces non-realtime models to skip fixed-slice silence trimming and merging
- `response_format`: realtime models only support omission or `json`

Example:

```bash
curl -X POST "http://localhost:8080/v1/audio/transcriptions" \
  -H "Authorization: Bearer sk-aaa" \
  -F "file=@demo.wav" \
  -F "model=qwen3-asr-flash" \
  -F "language=zh" \
  -F "enable_itn=false"
```

Non-streaming response:

```json
{"status":"success","text":"..."}
```

For non-realtime models, `stream=true` forces this request to behave as if `SKIP_TRIM=true`, skipping fixed-slice silence trimming and merging even when the service setting is `false`. After conversion to WAV, audio is still segmented according to `API_SEGMENT_LENGTH`, with `SEGMENT_WORKERS` controlling export and encoding concurrency and `API_CONCURRENCY` controlling upstream recognition concurrency. `FFMPEG_SEGMENT_LENGTH` and `FFMPEG_WORKS` are not used for trimming. This override does not change global configuration or affect other requests.

Pseudo-streaming responses are still emitted only after all segments have been recognized:

```text
data: {"type":"transcript.text.delta","delta":"..."}

data: {"type":"transcript.text.done","text":"..."}

data: [DONE]
```

Realtime models use the same file endpoint:

```bash
curl -N -X POST "http://localhost:8080/v1/audio/transcriptions" \
  -H "Authorization: Bearer sk-aaa" \
  -F "file=@demo.wav" \
  -F "model=qwen-audio-3.0-asr-flash-streaming" \
  -F "language=zh" \
  -F "stream=true"
```

For Qwen-ASR-Realtime, replace the example model with `qwen3-asr-flash-realtime` or a dated version. For Paraformer realtime models, use `paraformer-realtime-v2`, `paraformer-realtime-v1`, `paraformer-realtime-8k-v2`, or `paraformer-realtime-8k-v1`. No realtime model requires a public audio URL.

After upload, the service continuously decodes audio into mono PCM16 through an FFmpeg pipe, without trimming silence or splitting it into independent recognition tasks. Qwen3-ASR-Flash-Realtime and `paraformer-realtime-v1` use 16000 Hz; Fun-ASR / Paraformer 8k realtime models use 8000 Hz; other supported realtime models use 24000 Hz. File audio is sent in approximately 100 ms chunks at roughly real-time speed. This conservative sending policy means long files do not gain the processing speedup of concurrent non-realtime segment recognition.

Realtime SSE emits confirmed text continuously and returns the full text at the end, without an additional `[DONE]`:

```text
data: {"type":"transcript.text.delta","delta":"First sentence. "}

data: {"type":"transcript.text.delta","delta":"Second sentence."}

data: {"type":"transcript.text.done","text":"First sentence. Second sentence."}
```

The Fun-ASR and Paraformer protocols emit only finalized sentences, not intermediate full-text hypotheses that may be revised. The Qwen-ASR protocol uses upstream VAD and emits additions to the confirmed `text` prefix while the file is being sent, then fills in the remaining text from the final `transcript`. The revisable `stash` draft is not emitted. Even if Qwen item results arrive out of order, the full file transcript is assembled in speech order, buffering later results when necessary.

An SSE comment is sent every 15 seconds as a keepalive. Errors before output starts return HTTP JSON; errors after output starts emit a `type=error` event and end the stream without a successful `transcript.text.done`. `stream=false` uses the same realtime upstream path and returns the full JSON result at completion.

### `GET /v1/realtime`

WebSocket URL: `ws://localhost:8080/v1/realtime?intent=transcription`. Use `wss://` through a TLS reverse proxy in production. Authentication uses `Authorization: Bearer <API_TOKEN>` or `x-api-key`; keys are not accepted in URL query parameters. The `realtime` subprotocol can be negotiated, and same-origin Origin checks are enabled by default.

Uses the OpenAI Realtime `session.update` transcription-session structure. After connecting, receive `session.created`, then send the configuration:

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
          "prompt": "Terminology: Qwen"
        },
        "turn_detection": null,
        "noise_reduction": null
      }
    }
  }
}
```

After receiving `session.updated`, send audio continuously:

```json
{"type":"input_audio_buffer.append","audio":"<Base64-encoded PCM16 audio chunk>"}
```

Audio must be **24000 Hz, mono, 16-bit little-endian PCM**, without a WAV header. Each chunk must align to complete 16-bit samples. Standard Fun-ASR realtime models and `paraformer-realtime-v2` receive the Base64-decoded audio directly. The 8k models use continuous low-pass-filtered resampling to 8000 Hz, and `paraformer-realtime-v1` to 16000 Hz. Qwen3-ASR-Flash-Realtime uses continuous resampling to 16000 Hz, then Base64-encodes each chunk for transmission. Resampler state is retained across chunks.

For Qwen-ASR-Realtime, replace `transcription` in the session example with the following, leaving the input format unchanged. Do not include the original example's nonempty `prompt`:

```json
{"model":"qwen3-asr-flash-realtime","language":"zh"}
```

Paraformer realtime models also require an empty or omitted `prompt`, for example:

```json
{"model":"paraformer-realtime-v2","language":"zh"}
```

By default, `turn_detection=null` and the client commits manually, with at least 100 ms of audio per turn:

```json
{"type":"input_audio_buffer.commit"}
```

The service returns `input_audio_buffer.committed` and `conversation.item.added`, followed by `conversation.item.input_audio_transcription.delta` for that turn, and finally `conversation.item.input_audio_transcription.completed`. The Fun-ASR and Paraformer protocols buffer confirmed sentences received before commit, emit them after commit, and merge multiple sentences in the same turn into a final transcript. The Qwen-ASR protocol disables upstream VAD and receives confirmed-prefix deltas and final text after manual commit, without emitting drafts.

Committing does not close the client connection; audio for the next turn can follow. Use `item_id` to correlate results and `previous_item_id` to determine turn order; turns may finish out of order. If the previous Qwen VAD turn is still flushing its tail, subsequent turns can continue sending audio, but their commit acknowledgments and recognition results wait until the previous turn's item boundaries are known.

```json
{"type":"input_audio_buffer.clear"}
```

Clearing returns `input_audio_buffer.cleared`, cancels the current uncommitted audio, and ignores late results; committed turns continue processing. A Qwen VAD upstream connection may contain both committed items and an uncommitted tail. In that case, the service retains committed items until completion, discards results for the uncommitted tail, and uses a new upstream connection for the next turn.

To receive results automatically as you speak, set `audio.input.turn_detection` to:

```json
{"type":"server_vad","silence_duration_ms":1300}
```

`silence_duration_ms` supports 200–6000. Fun-ASR and Paraformer v2 map it to upstream `max_sentence_silence`: the first sentence result triggers `input_audio_buffer.speech_started`, and final confirmation triggers `speech_stopped`, automatic commit, and transcription results. Qwen-ASR maps it to the parameter of the same name and translates upstream speech-start, speech-stop, commit, and recognition events. The VAD threshold is fixed at the upstream-recommended `0.0`; the client `threshold` parameter is not exposed.

Neither Paraformer v1 model supports a custom silence threshold. To enable automatic segmentation, send only `{"type":"server_vad"}` to use upstream defaults. Explicit `silence_duration_ms` values return an error, and the returned v1 session configuration omits this field. When switching from another model to v1 after setting a silence threshold, also update `turn_detection` to `null` or an object containing only `type`.

You can still manually `commit` to flush unconfirmed trailing audio. In Qwen VAD mode, this sends upstream `session.finish` and waits for the final sentence and `session.finished`; it does not send the upstream-prohibited `input_audio_buffer.commit`. Downstream commit acknowledgments reflect the items actually returned, so no new item may appear if no speech was detected. This mode uses Alibaba Cloud VAD and does not guarantee the same segmentation timing as OpenAI.

After VAD segmentation, the 100 ms minimum for manual commit counts only audio after the confirmed speech boundary: Fun-ASR / Paraformer use the final `end_time`, while Qwen-ASR uses `speech_stopped.audio_end_ms`, both converted to client-side 24000 Hz sample counts. If a Paraformer end time is missing or invalid, valid word-level end times are used when available. If the boundary still cannot be determined, VAD mode returns an error and closes the session rather than fabricating a commit time. When boundary events arrive late, audio already sent upstream but located after that boundary is retained. Empty buffers or commits below 100 ms return an error without ending the current upstream task.

#### Compatibility and Limitations

##### Interface Support

- Implements transcription sessions only, supporting `session.update` and audio `append` / `commit` / `clear`.
- Does not support the legacy `transcription_session.update`, WebRTC, ephemeral client keys, speech generation, or tool calling.
- Does not support `semantic_vad`, VAD `threshold` / `prefix_padding_ms`, noise reduction, or logprobs. Unsupported fields and events return errors.

##### Audio Input

- WebSocket input supports only the 24000 Hz mono PCM16 format described above, not G.711.
- Realtime audio totals are not subject to the non-realtime Base64 10 MiB or URL 2 GiB limits; the file endpoint remains subject to `MAX_UPLOAD_MB`.
- The Qwen upstream non-VAD limit for a single Base64 `append.audio` value is 15 MiB. The service actually splits audio into PCM chunks of at most 3200 bytes and checks their encoded size; client messages remain subject to the 1 MiB limit below.

##### Session Configuration

- Partial updates are supported. During audio input, only `prompt` can be updated, and only on models supporting context. Commit or clear before changing the model, language, or turn detection settings.
- `prompt` is limited to 400 characters and supported only by the `qwen-audio-3.x-asr-flash-streaming` series, `fun-asr-realtime`, and `fun-asr-realtime-2025-11-07`. Other models reject nonempty prompts.

##### Capacity and Concurrency Limits

| Object | Limit |
|---|---|
| Single client JSON message (including Base64 text and JSON fields) | 1 MiB |
| Unfinished upstream tasks per session (including the current input task) | 4 |
| Cumulative text per manually committed Fun-ASR / Paraformer turn | 1 MiB |
| Text per Qwen-ASR item | 1 MiB |
| Full realtime file transcript | 8 MiB |
| Total items processed per Qwen upstream connection | 4096 |
| Concurrent unfinished items per Qwen upstream connection | 64, with at most 8 MiB of confirmed text in total |
| Results buffered per subsequent task while waiting for the previous Qwen VAD tail | 64 events, 8 MiB of text |

When the upstream task concurrency limit is reached, wait for tasks to finish and resend the rejected audio. For long Qwen sessions, use `commit` to end the current upstream connection and continue with the next turn. While waiting for the previous turn's tail, adjacent confirmed-prefix updates for the same item are coalesced. Exceeding buffer limits returns an error rather than allowing unbounded buffering.

##### Errors and Disconnections

- Upstream connection or protocol failure: sends an error event and closes the session without automatic reconnection or audio replay. Committed but unfinished items also receive `conversation.item.input_audio_transcription.failed`.
- Qwen single-item recognition failure: returns `failed` only for that item, without reporting success. Other items and the client session can continue.
- Client disconnection: cancels all unfinished tasks.

Interface structures follow [OpenAI file transcription](https://developers.openai.com/api/docs/guides/speech-to-text) and [OpenAI Realtime transcription](https://developers.openai.com/api/docs/guides/realtime-transcription). Compatibility is limited to the implementation described above.

## Supported Models

The service does not translate model aliases. It passes `model` unchanged to DashScope and uses model-name patterns only to select the endpoint and request structure.

| Model prefix | Example model names | Invocation |
|---|---|---|
| `qwen3-asr-flash-realtime*` | `qwen3-asr-flash-realtime`, `qwen3-asr-flash-realtime-2026-02-10`, `qwen3-asr-flash-realtime-2025-10-27` | Realtime WebSocket recognition, Base64 PCM audio events, 16000 Hz upstream |
| `qwen-audio-3.x-asr-flash-streaming*` | `qwen-audio-3.0-asr-flash-streaming`, `qwen-audio-3.1-asr-flash-streaming` | Realtime WebSocket recognition, binary PCM audio |
| `fun-asr-realtime*` | `fun-asr-realtime`, `fun-asr-realtime-2025-11-07`, `fun-asr-realtime-2026-02-28`, `fun-asr-realtime-2025-09-15` | Realtime WebSocket recognition, binary PCM audio |
| `fun-asr-flash-8k-realtime*` | `fun-asr-flash-8k-realtime`, `fun-asr-flash-8k-realtime-2026-01-28` | Realtime WebSocket recognition, fixed 8000 Hz upstream |
| `paraformer-realtime-v2*` | `paraformer-realtime-v2` | Realtime WebSocket recognition, binary PCM, 24000 Hz upstream |
| `paraformer-realtime-v1*` | `paraformer-realtime-v1` | Realtime WebSocket recognition, fixed 16000 Hz upstream |
| `paraformer-realtime-8k-v2*` / `paraformer-realtime-8k-v1*` | `paraformer-realtime-8k-v2`, `paraformer-realtime-8k-v1` | Realtime WebSocket recognition, fixed 8000 Hz upstream |
| `qwen-audio-3.x-asr-flash-filetrans*` | `qwen-audio-3.0-asr-flash-filetrans`, `qwen-audio-3.1-asr-flash-filetrans` | `POST /services/audio/asr/transcription`, asynchronous URL-based task, polling `/tasks/<task_id>` |
| `qwen-audio-3.x-asr-flash*` | `qwen-audio-3.0-asr-flash`, `qwen-audio-3.1-asr-flash`, `qwen-audio-3.1-asr-flash-message` | `POST /services/aigc/multimodal-generation/generation`, `input_audio` request structure |
| `qwen3-asr-flash*` | `qwen3-asr-flash`, `qwen3-asr-flash-2025-09-08` | `POST /services/aigc/multimodal-generation/generation`, Qwen3 ASR multimodal request structure |
| `fun-asr-flash*` | `fun-asr-flash-2026-06-15` | `POST /services/aigc/multimodal-generation/generation`, `input_audio` request structure |
| `fun-asr*` | `fun-asr`, `fun-asr-2025-11-07`, `fun-asr-mtl` | `POST /services/audio/asr/transcription`, asynchronous URL-based task, polling `/tasks/<task_id>` |
| `paraformer*` | Full Paraformer model names such as `paraformer-v2` and `paraformer-v1` | `POST /services/audio/asr/transcription`, asynchronous task, polling `/tasks/<task_id>` |

To use a dated or versioned model, pass its full name, such as `qwen3-asr-flash-2025-09-08` or `fun-asr-flash-2026-06-15`.

Realtime patterns are matched before non-realtime Flash / Fun-ASR / Paraformer patterns. `GET /v1/models` returns declared models. Model-name routing does not imply that any arbitrarily constructed version exists upstream; availability depends on the selected region and Alibaba Cloud Model Studio account.

The Fun-ASR realtime protocol maps `language` to a single-element `language_hints` array. Standard models support `zh en ja ko vi th id ms tl hi ar fr de es pt ru it nl sv da fi no el pl cs hu ro bg hr sk`; `fun-asr-realtime-2026-02-28` supports only `zh en ja`, `fun-asr-realtime-2025-09-15` only `zh en`, and 8k realtime models only `zh`.

The Qwen-ASR protocol maps language to `session.input_audio_transcription.language` and supports `zh yue en ja de ko ru fr pt ar it es hi id th tr uk vi cs da fil fi is ms no pl sv`. It does not reuse the Fun-ASR language list. If omitted, the upstream service detects the language automatically.

The Paraformer realtime protocol maps `language` to a single-element `language_hints` array and validates the documented list: `zh en ja yue ko de fr ru`. If omitted, the upstream service detects the language automatically. This family is available only in China (Beijing). Although upstream `paraformer-realtime-v2` supports arbitrary sample rates, this service uses 24000 Hz; v1 and 8k models are converted to the fixed sample rates listed above.

## Environment Variables

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

### Upstream Endpoints and Connections

The HTTP endpoint and the two WebSocket endpoints are configured independently; none is derived from another. For Model Studio workspace domains, replace `<WorkspaceId>` with the actual workspace ID:

| Setting | Purpose and workspace endpoint |
|---|---|
| `DASHSCOPE_HTTP_BASE_URL` | Non-realtime HTTP; Beijing: `https://<WorkspaceId>.cn-beijing.maas.aliyuncs.com/api/v1` |
| `DASHSCOPE_WS_URL` | Fun-ASR / Paraformer realtime protocols; Beijing: `wss://<WorkspaceId>.cn-beijing.maas.aliyuncs.com/api-ws/v1/inference`; Singapore: `wss://<WorkspaceId>.ap-southeast-1.maas.aliyuncs.com/api-ws/v1/inference` (Fun-ASR only) |
| `DASHSCOPE_QWEN_WS_URL` | Qwen-ASR realtime protocol; use the corresponding regional domain with `/api-ws/v1/realtime`. The service adds or overrides the `model` query parameter |

- Paraformer realtime is available only in Beijing and reuses `DASHSCOPE_WS_URL`. The default `wss://dashscope.aliyuncs.com/api-ws/v1/inference` remains usable.
- The key, workspace, and region must match. `DASHSCOPE_WORKSPACE` supplies the `X-DashScope-WorkSpace` header for realtime upstream requests.
- Non-realtime requests to DashScope, OSS, and WebDAV prefer HTTP/2 but do not reuse keep-alive connections: each request opens a new TCP/TLS connection and closes it on completion. Realtime connections remain open for the duration of an upstream task.

### Uploads and Storage

- `MAX_UPLOAD_MB` limits the size of each uploaded audio file. It defaults to `500` MiB and can be overridden with `--max-upload-mb`.
- WebDAV is enabled only when both `WEBDAV_URL` and `WEBDAV_CREDENTIALS` are set. Otherwise, URL-input models use the DashScope SDK's built-in temporary OSS flow.
- `WEBDAV_CREDENTIALS` uses the format `user@password`; the password may contain additional `@` characters.
- WebDAV settings do not affect Qwen3-ASR-Flash, Qwen-Audio-3.x-ASR-Flash (excluding Filetrans), or Fun-ASR-Flash, which use Base64 Data URIs directly.

See [Audio Segment Storage and Uploads](#audio-segment-storage-and-uploads) for storage behavior and deployment requirements.

### Non-Realtime Audio Processing

ASR segments use the `ogg` container and `libopus` codec. The service does not validate model format allowlists against the original file extension. `paraformer-8k*` uses 8000 Hz; other non-realtime models use 16000 Hz.

| Setting | Purpose and default behavior |
|---|---|
| `OUTPUT_BITRATE` / `--output-bitrate` | Opus bitrate; defaults to `128k` |
| `SEGMENT_WORKERS` / `--segment-workers` | ASR segment export and encoding concurrency; `0` lets the preprocessing library choose based on CPU count |
| `LIBAV_CODEC_THREADS` / `--libav-codec-threads` | Decoder/encoder threads per libav pipeline; `0` uses libav defaults |
| `SKIP_TRIM` / `--skip-trim` | Defaults to `false`; `true` or `1` skips fixed-slice silence trimming and merging, splitting at silence intervals directly after conversion to WAV |

When increasing concurrency or thread counts, also account for `FFMPEG_WORKS` to avoid excessive combined Go worker and libav codec parallelism.

Non-realtime requests with `stream=true` always skip trimming and merging. With `stream=false` or an omitted value, `SKIP_TRIM` applies. Realtime processing is unaffected by this setting.

### Recognition Options and Authentication

- `ENABLE_LID` / `enable_lid` is retained only for compatibility; currently supported models do not forward it upstream.
- `ENABLE_ITN` provides the default for non-realtime requests that omit `enable_itn`. An explicit request field overrides the service default.
- In production, pass `API_TOKEN` and `DASHSCOPE_API_KEY` through environment variables to keep keys out of command lines. For local testing, `--api-token` and `--dashscope-api-key` are also supported.

### Configuration Scope

| Scope | Settings |
|---|---|
| Common | `LISTEN`, `API_TOKEN`, `DASHSCOPE_API_KEY`; `MAX_UPLOAD_MB` applies to file uploads |
| Non-realtime | `DASHSCOPE_HTTP_BASE_URL`, `WEBDAV_URL`, `WEBDAV_CREDENTIALS`, `UPSTREAM_TIMEOUT_SECONDS`, `API_CONCURRENCY` |
| Non-realtime audio processing | `API_SEGMENT_LENGTH`, `FFMPEG_SEGMENT_LENGTH`, `FFMPEG_WORKS`, `SKIP_TRIM`, `SEGMENT_WORKERS`, `LIBAV_CODEC_THREADS`, `SILENT_INTERVAL`, `PADDING_LENGTH`, `OUTPUT_BITRATE` |
| Non-realtime recognition options | `ENABLE_LID`, `ENABLE_ITN`, all `ASR_RETRY_*`; realtime mode does not read these options |
| Realtime | `DASHSCOPE_WS_URL` (Fun-ASR / Paraformer protocols), `DASHSCOPE_QWEN_WS_URL` (Qwen-ASR protocol), `DASHSCOPE_WORKSPACE`, all `REALTIME_*` |

`REALTIME_CONCURRENCY` jointly limits realtime file requests and client WebSocket sessions. Excess requests receive HTTP 429 rather than being queued. A WebSocket session can have at most 4 unfinished upstream tasks. Realtime timeouts cover the handshake, startup wait, finish wait, and individual writes, not the total recording duration. `REALTIME_IDLE_TIMEOUT_SECONDS` limits how long a client WebSocket can remain idle without sending a message.

Range validation for non-realtime retry and preprocessing concurrency settings occurs when non-realtime requests run, so it does not prevent a realtime-only service from starting. Invalid command-line types or syntax still fail at startup.

## Building Locally

The realtime file endpoint requires `ffmpeg` on the system `PATH`. The continuous-PCM WebSocket endpoint does not require the FFmpeg executable; 8k and 16k resampling is implemented in Go.

The preprocessing library requires the `libav` build tag and static FFmpeg/libav dependencies. The project script delegates to the build script provided by the `github.com/Joey-Kot/ASR-Audio-Preprocess` version in `go.mod`:

```bash
./scripts/bootstrap-static-audio-deps.sh

CGO_ENABLED=1 \
PKG_CONFIG_PATH="$PWD/third_party/ffmpeg-audio/lib/pkgconfig" \
PKG_CONFIG="pkg-config --static" \
go build -tags libav -trimpath -ldflags="-s -w -linkmode external -extldflags '-static'" -o qwen-stt-compatible ./cmd/server
```

Complete startup example:

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

In production, keep tokens in environment variables to avoid exposing keys in command lines:

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

Command-line options:

| Option | Default | Environment variable | Description |
|---|---:|---|---|
| `--listen` | `:8080` | `LISTEN` | HTTP listen address |
| `--api-token` | Empty | `API_TOKEN` | Authentication tokens for the compatible API, separated by commas |
| `--dashscope-api-key` | Empty | `DASHSCOPE_API_KEY` | DashScope API Key |
| `--dashscope-base-url` | `https://dashscope.aliyuncs.com/api/v1` | `DASHSCOPE_HTTP_BASE_URL` | DashScope HTTP API base URL |
| `--dashscope-ws-url` | `wss://dashscope.aliyuncs.com/api-ws/v1/inference` | `DASHSCOPE_WS_URL` | Fun-ASR / Paraformer upstream WebSocket URL, independent of the HTTP URL |
| `--dashscope-qwen-ws-url` | `wss://dashscope.aliyuncs.com/api-ws/v1/realtime` | `DASHSCOPE_QWEN_WS_URL` | Qwen-ASR upstream WebSocket URL; the model query parameter is added automatically |
| `--dashscope-workspace` | Empty | `DASHSCOPE_WORKSPACE` | Workspace header for realtime upstream requests |
| `--realtime-concurrency` | `10` | `REALTIME_CONCURRENCY` | Concurrent realtime file requests and WebSocket sessions; excess requests receive 429 |
| `--realtime-connect-timeout` | `10s` | `REALTIME_CONNECT_TIMEOUT_SECONDS` | Realtime upstream handshake timeout |
| `--realtime-start-timeout` | `10s` | `REALTIME_START_TIMEOUT_SECONDS` | Timeout waiting for `task-started` or Qwen session creation and update acknowledgment |
| `--realtime-finish-timeout` | `30s` | `REALTIME_FINISH_TIMEOUT_SECONDS` | Timeout waiting for `task-finished` or `session.finished` |
| `--realtime-write-timeout` | `10s` | `REALTIME_WRITE_TIMEOUT_SECONDS` | Timeout for each upstream or client write in realtime mode |
| `--realtime-idle-timeout` | `120s` | `REALTIME_IDLE_TIMEOUT_SECONDS` | Client WebSocket inactivity timeout |
| `--webdav-url` | Empty | `WEBDAV_URL` | Public HTTPS WebDAV base URL; enabled when credentials are also set |
| `--webdav-credentials` | Empty | `WEBDAV_CREDENTIALS` | WebDAV credentials in `user@password` format; preferably passed only through environment variables |
| `--max-upload-mb` | `500` | `MAX_UPLOAD_MB` | Maximum size per uploaded audio file, in MiB |
| `--upstream-timeout` | `30s` | `UPSTREAM_TIMEOUT_SECONDS` | Non-realtime DashScope HTTP request timeout |
| `--api-concurrency` | `10` | `API_CONCURRENCY` | Concurrent non-realtime upstream ASR requests; excess requests are queued |
| `--api-segment-length` | `175s` | `API_SEGMENT_LENGTH` | Maximum ASR segment duration |
| `--fixed-slice-length` | `5s` | `FFMPEG_SEGMENT_LENGTH` | Fixed slice length for silence trimming |
| `--fixed-slice-workers` | `16` | `FFMPEG_WORKS` | Fixed-slice silence trimming concurrency |
| `--skip-trim` | `false` | `SKIP_TRIM` | Skip fixed-slice silence trimming and merging, splitting at silence intervals directly after transcoding; accepts `0/1` or `true/false`; forced on for non-realtime `stream=true` requests |
| `--segment-workers` | `0` | `SEGMENT_WORKERS` | ASR segment export and encoding concurrency; `0` selects based on CPU count |
| `--libav-codec-threads` | `0` | `LIBAV_CODEC_THREADS` | Decoder/encoder threads per libav pipeline; `0` uses libav defaults |
| `--silent-interval` | `700ms` | `SILENT_INTERVAL` | Minimum silence duration |
| `--padding` | `100ms` | `PADDING_LENGTH` | Padding retained before and after non-silent intervals |
| `--output-bitrate` | `128k` | `OUTPUT_BITRATE` | Output bitrate for ASR audio segments |
| `--enable-lid` | `true` | `ENABLE_LID` | Compatibility setting; currently supported models do not forward it upstream |
| `--enable-itn` | `false` | `ENABLE_ITN` | Default when the request omits `enable_itn`; accepts `0/1` or `true/false` |
| `--asr-retry-max-attempts` | `4` | `ASR_RETRY_MAX_ATTEMPTS` | Maximum ASR call attempts |
| `--asr-retry-initial-delay` | `500ms` | `ASR_RETRY_INITIAL_DELAY` | Initial delay before an ASR retry |
| `--asr-retry-factor` | `2.0` | `ASR_RETRY_FACTOR` | Exponential backoff factor for ASR retries |
| `--asr-retry-max-delay` | `8s` | `ASR_RETRY_MAX_DELAY` | Maximum delay before an ASR retry |

Release packages include the executable, `README.md`, `LICENSE`, `NOTICE`,
`THIRD_PARTY_NOTICES.md`, and the complete third-party license texts in
`THIRD_PARTY_LICENSES/`.

## Logs and Temporary Files

On startup, the service removes historical request directories under the system temporary directory:

```text
<system-temp-dir>/qwen-stt-compatible/<request_id>
```

Each request's temporary directory is also removed when the request ends normally.

Each transcription request logs basic request information, without API tokens, DashScope API keys, or audio content:

```text
request=<request_id> endpoint=/v1/audio/transcriptions file=<filename> model=<model> language=<language> enable_lid=<bool> enable_itn=<bool>
```

Realtime file requests instead log `mode=realtime` and the upstream `sample_rate`, without segment or trimming logs. WebSocket sessions log connection and close events with `session=<session_id>`. Realtime PCM is passed through pipes or memory, without creating Ogg segments or uploading to OSS / WebDAV.

For non-realtime, non-streaming requests with `SKIP_TRIM=false`, successful fixed-slice silence trimming logs:

```text
fixed trim input_duration=<original-audio-duration> fixed_slice_length=<fixed-slice-length> slices=<successful-slice-count> trimmed_slices=<slices-with-silence-detected-and-trimmed>
```

After ASR segments are generated in the default mode:

```text
segments merged_duration=<merged-audio-duration> asr_segments=<concurrent-asr-segment-count>
```

For non-realtime requests with `SKIP_TRIM=true` or `stream=true`, fixed-trimming logs are omitted. Direct segmentation logs:

```text
segments skip_trim=true input_duration=<transcoded-audio-duration> asr_segments=<concurrent-asr-segment-count>
```

## Audio Segment Storage and Uploads

This section applies only to non-realtime models. Before transcription, the service splits processed audio into `ogg + Opus` ASR segments. Segments for Qwen3-ASR-Flash, Qwen-Audio-3.x-ASR-Flash, and Fun-ASR-Flash are encoded as Base64 Data URIs and must not exceed 10 MiB after encoding. Segments for Qwen-Audio-3.x-ASR-Flash-Filetrans, Fun-ASR, and Paraformer are provided to Model Studio through DashScope temporary OSS or self-hosted WebDAV URLs, with a 2 GiB file limit. Oversized segments return an error directly, without recognition retries.

### Recommended: RAM Disk

Mounting `/tmp` as a RAM disk (tmpfs) is recommended so that temporary preprocessing and segment files are written directly to memory. Size it according to concurrency, audio duration, and the upload limit, for example `8G`:

```bash
sudo mount -t tmpfs -o size=8G,mode=1777 tmpfs /tmp
```

A RAM disk avoids write amplification and SSD wear from repeatedly writing temporary segments. Local temporary data stays in memory, moving between user space and kernel space, which can substantially reduce segment I/O time.

When also using self-hosted WebDAV, map the WebDAV domain to the local machine in `/etc/hosts` on the transcription host so segment uploads use the loopback network:

```text
127.0.0.1 files.example.com
```

Keep the public domain in `WEBDAV_URL`, such as `https://files.example.com`. The local service accesses Nginx and Dufs through loopback, while Model Studio fetches segments using the domain's public DNS resolution.

### Default: DashScope Temporary OSS

By default, the service uses a built-in implementation of the DashScope Python SDK's temporary OSS flow:

- Upload policy: `GET https://dashscope.aliyuncs.com/api/v1/uploads?action=getPolicy&model=<model>`
- OSS upload: multipart audio upload using the policy fields, returning `oss://...`
- Subsequent DashScope requests include `X-DashScope-OssResourceResolve: enable`

This depends on OSS policy and upload requests. Request frequency, rate limiting, or other factors can occasionally block progress, forcing concurrent segments to wait and slowing or stalling the transcription pipeline.

### Recommended: Self-Hosted WebDAV

When both `WEBDAV_URL` and `WEBDAV_CREDENTIALS` are configured, the service replaces temporary OSS with the following flow for each segment:

1. The service uploads the segment to self-hosted WebDAV with `PUT` and Basic Auth.
2. The service sends Model Studio an HTTPS file URL without authentication information.
3. Model Studio fetches the segment from that URL and transcribes it.
4. After transcription, the service deletes the temporary file using Basic Auth.

WebDAV must be publicly accessible over HTTPS by Model Studio. The service account needs upload, download, and delete permissions. Because Model Studio receives a URL without credentials, segment URLs must allow unauthenticated reads.

#### Setting Up WebDAV with Dufs

[Dufs](https://github.com/sigoden/dufs) is recommended for running WebDAV. In this example, `username` has read/write access to the root directory, while anonymous users have read-only access. Dufs listens only on `127.0.0.1:6001`, and segments are stored in `/tmp`:

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

Use Nginx to expose Dufs through a public reverse proxy:

```nginx
server {
    listen 443 ssl;
    server_name files.example.com;

    # Configure ssl_certificate and ssl_certificate_key as usual.
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

Corresponding service configuration:

```bash
WEBDAV_URL="https://files.example.com"
WEBDAV_CREDENTIALS="username@passwd"
```

WebDAV bypasses the built-in OSS policy and upload steps, avoiding pipeline stalls caused by occasional rate limiting. Hosting WebDAV on the same machine as the transcription service usually makes segment writes faster. Since Model Studio fetches directly from WebDAV, the extra transfer overhead of the two-stage request can approach that of a single stage. Actual performance depends on network quality and bandwidth between WebDAV, the transcription service, and Model Studio.

## DashScope Request Details

### `qwen-audio-3.x-asr-flash-streaming*` / `fun-asr-realtime*` / `fun-asr-flash-8k-realtime*`

Connect to `DASHSCOPE_WS_URL`, sending `Authorization: Bearer <DASHSCOPE_API_KEY>` during the handshake and optionally `X-DashScope-WorkSpace`.

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

Send binary PCM only after receiving `task-started`, while concurrently reading `result-generated`. Results with `heartbeat=true` do not emit text. `sentence_end=true` marks a finalized sentence, and each sentence's final result is processed only once.

After sending all audio, send `finish-task` and continue receiving trailing sentences until `task-finished`. The realtime path does not use `ASR_RETRY_*` and never automatically replays audio after failure. Models supporting context receive `prompt` as a `user/input_text` message in `input.context`, updated during the task through `continue-task`.

See the upstream [Realtime WebSocket API](https://docs.bailian.console.aliyun.com/zh/model-studio/fun-asr-realtime-websocket-api), [client events](https://docs.bailian.console.aliyun.com/zh/model-studio/fun-asr-client-events), and [server events](https://docs.bailian.console.aliyun.com/zh/model-studio/fun-asr-server-events).

### `paraformer-realtime-v2*` / `paraformer-realtime-v1*` / `paraformer-realtime-8k-v2*` / `paraformer-realtime-8k-v1*`

Reuses `DASHSCOPE_WS_URL` and the `run-task` / `finish-task` flow above, with the same authentication and workspace headers. Example v2 request:

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

`input` is always `{}`; context and `continue-task` are not sent. v2 enables heartbeats and sends `max_sentence_silence` according to client settings; neither v1 model sends these two parameters. Semantic segmentation, filler-word filtering, punctuation, and ITN retain upstream defaults and do not use the non-realtime `ENABLE_ITN` setting. Model-specific parameters such as `vocabulary_id` are not currently exposed, and emotion labels and word-level results are not forwarded.

Paraformer results have no documented `sentence_id` or `sentence_begin`. The service generates internal IDs from `begin_time` and sentence state, filters heartbeats and already-confirmed duplicates, and emits only final text with `sentence_end=true`. A change in start time before the active sentence is finalized, or `task-finished` with an unconfirmed sentence remaining, returns a protocol error rather than reporting success with missing text. Word-level timestamps are used only to recover VAD boundaries when the sentence end time is invalid; file output and manual commits do not depend on these timestamps to assemble text.

See the upstream [Paraformer Realtime WebSocket API](https://docs.bailian.console.aliyun.com/zh/model-studio/websocket-for-paraformer-real-time-service), [client events](https://docs.bailian.console.aliyun.com/zh/model-studio/paraformer-client-events), and [server events](https://docs.bailian.console.aliyun.com/zh/model-studio/paraformer-server-events).

### `qwen3-asr-flash-realtime*`

Connect to `DASHSCOPE_QWEN_WS_URL` with the model specified in the `model` URL query parameter. Authentication and workspace headers are the same as for the other realtime protocol. Receive `session.created`, send the configuration, and wait for `session.updated`:

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

The file endpoint uses the VAD configuration above. The WebSocket endpoint uses VAD or `null` according to client settings, defaulting to manual mode. Audio is sent as a Base64 string without a Data URI prefix:

```json
{"event_id":"event_<ID>","type":"input_audio_buffer.append","audio":"<Base64 PCM16>"}
```

In manual mode, send `input_audio_buffer.commit` followed by `session.finish` when input ends. In VAD mode, send only `session.finish`. Continue receiving results and close the upstream connection only after `session.finished`. Items are correlated and deduplicated by `item_id`; confirmed `text` prefixes become deltas, and `completed.transcript` supplies the remaining final text. `stash` is not converted into downstream deltas. If upstream revises an already-confirmed prefix, the service returns a protocol error to avoid emitting incorrect text that cannot be retracted.

Upstream supports PCM or Opus at 16000 / 8000 Hz; this adapter consistently uses 16000 Hz PCM16. Client input is not converted to Ogg / Opus and does not use the non-realtime segment upload path. Qwen-ASR realtime models do not send `prompt`, `enable_lid`, or `enable_itn`.

See the upstream [Qwen-ASR Realtime WebSocket API](https://docs.bailian.console.aliyun.com/zh/model-studio/qwen-asr-realtime-interaction-process), [client events](https://docs.bailian.console.aliyun.com/zh/model-studio/qwen-asr-realtime-client-events), and [server events](https://docs.bailian.console.aliyun.com/zh/model-studio/qwen-asr-realtime-server-events). Model names and dated versions are listed in the [official model documentation](https://help.aliyun.com/zh/model-studio/qwen3-asr-flash-realtime).

### `qwen3-asr-flash*`

This section covers only non-realtime Flash models, excluding Realtime. It uses the DashScope multimodal generation endpoint. Each segment is encoded as a Base64 Data URI; the Base64-encoded data must be at most 10 MiB, otherwise the request returns an error. `prompt` is used only as recognition context; no system message is sent when it is empty.

- ASR call: `POST <DASHSCOPE_HTTP_BASE_URL>/services/aigc/multimodal-generation/generation`

Core request structure:

```json
{
  "model": "qwen3-asr-flash",
  "input": {
    "messages": [
      {"role": "system", "content": [{"text": "Terminology: Qwen"}]},
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

This section covers only non-realtime Flash models, excluding Streaming, Realtime, and Filetrans. It uses the multimodal generation endpoint. Each segment is encoded as a Base64 Data URI; the Base64-encoded data must be at most 10 MiB, otherwise the request returns an error.

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

This section covers only non-realtime asynchronous models. It uses the same asynchronous task flow as `dashscope.audio.asr.Transcription.async_call`:

- Submit task: `POST <DASHSCOPE_HTTP_BASE_URL>/services/audio/asr/transcription`
- Poll task: `GET <DASHSCOPE_HTTP_BASE_URL>/tasks/<task_id>`
- After a subtask succeeds, download `transcription_url` and extract the text
- These large-file asynchronous models do not use Base64; audio is provided through public HTTP/HTTPS URLs or temporary `oss://` URLs supported by the REST API
- `X-DashScope-OssResourceResolve: enable` is sent only for temporary `oss://` URLs, not HTTP/HTTPS URLs
- `prompt` is sent as `input_text` inside `input.context` for Qwen-Audio-3.x-ASR-Flash-Filetrans / Fun-ASR; no context is sent for Paraformer
- `language_hints` is sent for Qwen-Audio-3.x-ASR-Flash-Filetrans / Fun-ASR
- For Paraformer, `language_hints` is sent only for `paraformer-v2`; v1, 8k, MTL, and other models do not receive it

Task submission request body:

```json
{
  "model": "qwen-audio-3.0-asr-flash-filetrans",
  "input": {
    "file_urls": ["https://files.example.com/audio.ogg"],
    "context": [
      {"role": "user", "content": [{"type": "input_text", "text": "Terminology: Qwen"}]}
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
