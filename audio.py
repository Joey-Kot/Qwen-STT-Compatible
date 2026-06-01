import os
import re
import uuid
import shutil
import secrets
import logging
import asyncio
import math
from pathlib import Path
from typing import Optional, Tuple, List, Any, Dict
from contextvars import ContextVar, copy_context
from concurrent.futures import ThreadPoolExecutor

import ffmpeg
import dashscope
import subprocess
import json
import wave
import contextlib

from fastapi import FastAPI, UploadFile, File, HTTPException, Depends, status, Form
from fastapi.responses import JSONResponse
from fastapi.security import HTTPBearer, HTTPAuthorizationCredentials
from fastapi.middleware.cors import CORSMiddleware
from dotenv import load_dotenv

# 请确保安装 dashscope python SDK、ffmpeg-python、fastapi、dotenv、uvicorn、python-multipart
# pip install dashscope ffmpeg-python fastapi dotenv uvicorn python-multipart --no-cache-dir --break-system-packages
# 请确保安装ffmpeg、mediainfo
# apt install ffmpeg mediainfo

# 环境变量：
# BAILIAN_TOKEN：阿里云百炼 Token
# API_CONCURRENCY：百炼 API 并发请求数（整数，默认10）
# API_SEGMENT_LENGTH：百炼 API 请求音频最大切片长度（整数秒，默认175）
# FFMPEG_WORKS：静音裁剪时使用的 ffmpeg 并发线程数（整数，默认16）
# FFMPEG_SEGMENT_LENGTH：静音裁剪时使用的单个音频切片长度（整数秒，默认5）
# SILENT_INTERVAL：静音裁剪时使用的音频静音区间长度（整数毫秒，默认700）
# PADDING_LENGTH：静音裁剪时使用音频分片合并填充区间长度（整数毫秒，默认100）
# ASR_RETRY_MAX_ATTEMPTS：百炼 API 调用最大重试次数（整数，默认4）
# ASR_RETRY_INITIAL_DELAY：百炼 API 调用初始重试等待秒数（数字，默认0.5）
# ASR_RETRY_FACTOR：百炼 API 调用重试退避倍数（数字，默认2.0）
# ASR_RETRY_MAX_DELAY：百炼 API 调用最大重试等待秒数（数字，默认8.0）

load_dotenv()

request_id_var = ContextVar("request_id", default="N/A")


class RequestIdFilter(logging.Filter):
    def filter(self, record):
        record.request_id = request_id_var.get()
        return True


logging.basicConfig(
    level=logging.INFO,
    format="%(asctime)s - %(levelname)s - RequestID:%(request_id)s - %(message)s",
    handlers=[logging.StreamHandler()],
)
logger = logging.getLogger(__name__)
logger.addFilter(RequestIdFilter())

app = FastAPI()
app.add_middleware(
    CORSMiddleware,
    allow_origins=["*"],
    allow_credentials=True,
    allow_methods=["*"],
    allow_headers=["*"],
)


def get_positive_int_env(name: str, default: int) -> int:
    value = os.getenv(name)
    if not value:
        return default
    try:
        parsed = int(value)
        if parsed > 0:
            return parsed
    except ValueError:
        pass
    logger.warning("环境变量 %s=%r 非正整数，使用默认值 %d", name, value, default)
    return default


def get_positive_float_env(name: str, default: float) -> float:
    value = os.getenv(name)
    if not value:
        return default
    try:
        parsed = float(value)
        if parsed > 0:
            return parsed
    except ValueError:
        pass
    logger.warning("环境变量 %s=%r 非正数，使用默认值 %s", name, value, default)
    return default


API_TOKENS = [t.strip() for t in os.getenv("API_TOKEN", "").split(",") if t.strip()]
API_CONCURRENCY = get_positive_int_env("API_CONCURRENCY", 10)
API_SEGMENT_LENGTH = get_positive_int_env("API_SEGMENT_LENGTH", 175)
ASR_RETRY_MAX_ATTEMPTS = get_positive_int_env("ASR_RETRY_MAX_ATTEMPTS", 4)
ASR_RETRY_INITIAL_DELAY = get_positive_float_env("ASR_RETRY_INITIAL_DELAY", 0.5)
ASR_RETRY_FACTOR = get_positive_float_env("ASR_RETRY_FACTOR", 2.0)
ASR_RETRY_MAX_DELAY = get_positive_float_env("ASR_RETRY_MAX_DELAY", 8.0)

dashscope_api_key = os.getenv("DASHSCOPE_API_KEY") or os.getenv("BAILIAN_TOKEN")
dashscope.api_key = dashscope_api_key

bearer_scheme = HTTPBearer()
executor = ThreadPoolExecutor(max_workers=max(API_CONCURRENCY, 4, (os.cpu_count() or 1) * 2))

DEFAULT_MODEL = "qwen3-asr-flash"

MODEL_ALIASES = {
    "asr": "qwen3-asr-flash",
    "asr-0908": "qwen3-asr-flash-2025-09-08",
}
MODEL_ALIASES = {k.lower(): v.lower() for k, v in MODEL_ALIASES.items()}

MODEL_RATES = {
    "qwen3-asr-flash": 16000,
    "qwen3-asr-flash-2025-09-08": 16000,
}
MODEL_RATES = {k.lower(): v for k, v in MODEL_RATES.items()}


def get_safe_filename(filename: str) -> str:
    """清理文件名，防止目录穿越/非法字符。"""
    return re.sub(r"[^a-zA-Z0-9._-]", "", os.path.basename(filename or "unknown"))


def normalize_language_code(lang: Optional[str]) -> Optional[str]:
    """格式化两字母语言码，如 'zh'/'en'。"""
    if not lang:
        return None
    lang_norm = str(lang).strip().lower()
    if re.match(r'^[a-z]{2}$', lang_norm):
        return lang_norm
    return None


async def verify_token(
    credentials: HTTPAuthorizationCredentials = Depends(bearer_scheme),
):
    """基于环境变量 API_TOKEN 的 Bearer 验证。"""
    if not API_TOKENS:
        logger.error("环境变量 API_TOKEN 未配置")
        raise HTTPException(
            status_code=status.HTTP_500_INTERNAL_SERVER_ERROR,
            detail="服务器内部错误：API_TOKEN 未配置",
        )
    if not any(secrets.compare_digest(credentials.credentials, token) for token in API_TOKENS):
        raise HTTPException(
            status_code=status.HTTP_401_UNAUTHORIZED,
            detail="无效的认证令牌",
            headers={"WWW-Authenticate": "Bearer"},
        )
    return credentials


def create_temp_dir_and_save(upload_file: UploadFile, request_id: str) -> Tuple[str, str]:
    """保存上传文件到 temp/<request_id>，返回 (file_path, temp_dir)。"""
    temp_dir = os.path.join("temp", request_id)
    os.makedirs(temp_dir, exist_ok=True)
    safe_filename = get_safe_filename(upload_file.filename or "unknown_file")
    file_path = os.path.join(temp_dir, "input_" + safe_filename)
    with open(file_path, "wb") as f:
        f.write(upload_file.file.read())
    return file_path, temp_dir


def model_name_map(model_name: Optional[str]) -> str:
    """别名映射到标准模型名。"""
    if not model_name:
        return DEFAULT_MODEL.lower()
    key = model_name.strip().lower()
    if key in MODEL_ALIASES:
        return MODEL_ALIASES[key]
    if key in MODEL_RATES:
        return key
    raise ValueError(f"不支持的模型名: '{model_name}'.")


def get_sample_rate_by_model(model_name: Optional[str]) -> int:
    """返回模型对应采样率。"""
    if not model_name:
        return MODEL_RATES[DEFAULT_MODEL.lower()]
    key = model_name.strip().lower()
    if key in MODEL_ALIASES:
        key = MODEL_ALIASES[key]
    if key in MODEL_RATES:
        return MODEL_RATES[key]
    raise ValueError(f"Unknown model_name: {model_name}.")


def preconvert_to_wav(input_path: str, wav_path: str, sample_rate: int) -> None:
    """ffmpeg 转为单声道 WAV 并重采样到 sample_rate。"""
    (
        ffmpeg.input(input_path)
        .output(wav_path, ar=sample_rate, ac=1)
        .overwrite_output()
        .run(capture_stdout=True, capture_stderr=True)
    )


def _ffmpeg_mean_volume_db(path: str) -> Tuple[Optional[float], Optional[float]]:
    try:
        cmd = ["ffmpeg", "-i", path, "-af", "volumedetect", "-f", "null", "-"]
        p = subprocess.run(cmd, stdout=subprocess.PIPE, stderr=subprocess.PIPE, text=True)
        stderr = p.stderr or ""
        mean = None
        maxv = None
        m_mean = re.search(r"mean_volume:\s*([-\d.]+)\s*dB", stderr)
        if m_mean:
            try:
                mean = float(m_mean.group(1))
            except Exception:
                mean = None
        m_max = re.search(r"max_volume:\s*([-\d.]+)\s*dB", stderr)
        if m_max:
            try:
                maxv = float(m_max.group(1))
            except Exception:
                maxv = None
        return mean, maxv
    except Exception:
        return None, None


def _ffmpeg_silencedetect(path: str, noise_db: float, min_s: float) -> List[Tuple[float, float]]:
    intervals: List[Tuple[float, float]] = []
    try:
        af = f"silencedetect=noise={noise_db}dB:d={min_s}"
        cmd = ["ffmpeg", "-i", path, "-af", af, "-f", "null", "-"]
        p = subprocess.run(cmd, stdout=subprocess.PIPE, stderr=subprocess.PIPE, text=True)
        stderr = p.stderr or ""
        starts = []
        ends = []
        for line in stderr.splitlines():
            line = line.strip()
            if "silence_start:" in line:
                m = re.search(r"silence_start:\s*([0-9.]+)", line)
                if m:
                    starts.append(float(m.group(1)))
            if "silence_end:" in line:
                m = re.search(r"silence_end:\s*([0-9.]+)", line)
                if m:
                    ends.append(float(m.group(1)))
        si = 0
        ei = 0
        while si < len(starts) and ei < len(ends):
            if ends[ei] >= starts[si]:
                intervals.append((starts[si], ends[ei]))
                si += 1
                ei += 1
            else:
                ei += 1
        return intervals
    except Exception:
        return []


def _detect_silence_intervals(
    wav_path: str,
    threshold_ms: int = 700,
    silence_thresh: Optional[int] = None,
) -> List[Tuple[float, float]]:
    min_s = max(0.001, threshold_ms / 1000.0)
    if silence_thresh is not None:
        return _ffmpeg_silencedetect(wav_path, float(silence_thresh), min_s)

    mean_db, max_db = _ffmpeg_mean_volume_db(wav_path)
    thresholds: List[float] = []
    try:
        if max_db is not None:
            base_max = float(max_db)
            offsets = [18.0, 16.0, 14.0, 12.0, 10.0]
            raw = [base_max - off for off in offsets]
        elif mean_db is not None:
            base = float(mean_db) - 8.0
            offsets = [0.0, 6.0, 12.0]
            raw = [base + off for off in offsets]
        else:
            raw = [-35.0, -30.0, -25.0, -20.0]
        lower_limit = -60.0
        upper_limit = -8.0
        seen = set()
        for t in raw:
            try:
                tv = max(lower_limit, min(upper_limit, float(t)))
            except Exception:
                continue
            if tv not in seen:
                seen.add(tv)
                thresholds.append(tv)
    except Exception:
        thresholds = [-35.0, -30.0, -25.0, -20.0]

    seen = set()
    normalized: List[float] = []
    for t in thresholds:
        try:
            tv = max(-60.0, min(-10.0, float(t)))
        except Exception:
            continue
        if tv not in seen:
            seen.add(tv)
            normalized.append(tv)

    # 如果需要查看移除静音的日志，请取消注释下面这一行
    # logger.info("构建尝试阈值序列: %s", normalized)
    for threshold_db in normalized:
        # 如果需要查看移除静音的日志，请取消注释下面这一行
        # logger.info("尝试 silencedetect with silence_thresh=%sdB (min_s=%ss)", threshold_db, min_s)
        intervals = _ffmpeg_silencedetect(wav_path, threshold_db, min_s)
        # 如果需要查看移除静音的日志，请取消注释下面这一行
        # logger.info("silencedetect returned %d intervals for thresh=%s", len(intervals), threshold_db)
        if intervals:
            return intervals

    # 如果需要查看移除静音的日志，请取消注释下面这一行
    # logger.info("未检测到静音，视为全段非静音: %s", wav_path)
    return []


def _invert_and_expand(
    silences: List[Tuple[float, float]],
    duration_s: float,
    keep_ms_local: int,
) -> List[Tuple[float, float]]:
    non_silent: List[Tuple[float, float]] = []
    prev = 0.0
    for s, e in sorted(silences, key=lambda x: x[0]):
        if s > prev:
            non_silent.append((prev, s))
        prev = max(prev, e)
    if prev < duration_s:
        non_silent.append((prev, duration_s))
    k = keep_ms_local / 1000.0
    expanded: List[Tuple[float, float]] = []
    for s, e in non_silent:
        s_exp = max(0.0, s - k)
        e_exp = min(duration_s, e + k)
        if not expanded or s_exp > expanded[-1][1]:
            expanded.append((s_exp, e_exp))
        else:
            expanded[-1] = (expanded[-1][0], max(expanded[-1][1], e_exp))
    filtered = [(s, e) for s, e in expanded if (e - s) >= 0.02]
    return filtered


def _group_intervals_by_max_span(
    intervals: List[Tuple[float, float]],
    max_segment_len_s: int,
) -> List[List[Tuple[float, float]]]:
    """按时间轴跨度分组，单个超长区间会被硬切。"""
    groups: List[List[Tuple[float, float]]] = []
    max_seg_s = float(max_segment_len_s)
    eps = 1e-6

    cur_group: List[Tuple[float, float]] = []
    segment_start: Optional[float] = None

    def _flush_group():
        nonlocal cur_group, segment_start
        if cur_group:
            groups.append(cur_group)
            cur_group = []
            segment_start = None

    for s, e in intervals:
        if e <= s:
            continue

        interval_len = e - s
        if interval_len > max_seg_s + eps:
            _flush_group()
            start = s
            while start < e - eps:
                piece_end = min(e, start + max_seg_s)
                groups.append([(start, piece_end)])
                start = piece_end
            continue

        if not cur_group:
            cur_group.append((s, e))
            segment_start = s
            continue

        new_span = e - (segment_start if segment_start is not None else s)
        if new_span <= max_seg_s + eps:
            cur_group.append((s, e))
        else:
            _flush_group()
            cur_group.append((s, e))
            segment_start = s

    _flush_group()
    return groups


def _build_filter_complex(intervals: List[Tuple[float, float]]) -> Optional[str]:
    if not intervals:
        return None
    parts = []
    labels = []
    for idx, (s, e) in enumerate(intervals):
        s_str = "{:.3f}".format(s)
        e_str = "{:.3f}".format(e)
        parts.append(f"[0:a]atrim=start={s_str}:end={e_str},asetpts=PTS-STARTPTS[a{idx}];")
        labels.append(f"[a{idx}]")
    concat_part = "".join(parts) + "".join(labels) + f"concat=n={len(intervals)}:v=0:a=1[out]"
    return concat_part


def trim_long_silences_from_wav(
    wav_path: str,
    out_wav_path: Optional[str] = None,
    threshold_ms: int = 700,
    keep_ms: int = 100,
    silence_thresh: Optional[int] = None,
) -> str:
    """移除长静音并输出单个 WAV。失败时返回原始 wav_path。"""
    try:
        duration = get_audio_duration_seconds(wav_path, probe_order="wave_first")
        if duration is None:
            logger.warning("无法获取音频时长，返回原始文件: %s", wav_path)
            return wav_path

        silence_intervals = _detect_silence_intervals(wav_path, threshold_ms, silence_thresh)
        non_silent_intervals = _invert_and_expand(silence_intervals, duration, keep_ms)

        if not non_silent_intervals:
            # 如果需要查看移除静音的日志，请取消注释下面这一行
            # logger.info("反转后未得到非静音区间，返回原始文件: %s", wav_path)
            return wav_path

        filter_complex = _build_filter_complex(non_silent_intervals)
        if not filter_complex:
            logger.info("构建 filter_complex 失败，返回原始文件: %s", wav_path)
            return wav_path
        if not out_wav_path:
            base = os.path.splitext(wav_path)[0]
            out_wav_path = f"{base}_nosilence.wav"
        ffmpeg_cmd = ["ffmpeg", "-i", wav_path, "-filter_complex", filter_complex, "-map", "[out]", "-ac", "1", out_wav_path, "-y"]
        p = subprocess.run(ffmpeg_cmd, stdout=subprocess.PIPE, stderr=subprocess.PIPE, text=True)
        if p.returncode != 0:
            logger.error("ffmpeg 提取合并失败，返回原始文件: %s", wav_path)
            return wav_path
        # 如果需要查看移除静音的日志，请取消注释下面这一行
        # logger.info("已移除静音: %s -> %s", wav_path, out_wav_path)
        return out_wav_path
    except Exception:
        logger.exception("静音裁剪异常")
        return wav_path


def split_wav_by_silence_groups(
    wav_path: str,
    threshold_ms: int = 700,
    keep_ms: int = 100,
    silence_thresh: Optional[int] = None,
    sample_rate: Optional[int] = None,
    max_segment_len_s: int = API_SEGMENT_LENGTH,
    opus_bitrate: str = "32k",
    out_dir: Optional[str] = None,
    keep_temp_wavs: bool = True,
    preserve_internal_silence: bool = True,
) -> List[dict]:
    """基于长静音分组切片，并为每段生成 OGG/Opus 和元数据。"""
    try:
        duration = get_audio_duration_seconds(wav_path, probe_order="wave_first")
        if duration is None:
            logger.warning("无法获取音频时长，无法切片: %s", wav_path)
            return []

        silence_intervals = _detect_silence_intervals(wav_path, threshold_ms, silence_thresh)
        non_silent_intervals = _invert_and_expand(silence_intervals, duration, keep_ms)

        if not non_silent_intervals:
            # 如果需要查看移除静音的日志，请取消注释下面这一行
            # logger.info("反转后未得到非静音区间，无法切片: %s", wav_path)
            return []

        groups = _group_intervals_by_max_span(non_silent_intervals, max_segment_len_s)
        if not groups:
            logger.info("没有生成任何分段，返回空列表: %s", wav_path)
            return []

        if not out_dir:
            out_dir = os.path.join(os.path.dirname(wav_path), "out_segments")
        os.makedirs(out_dir, exist_ok=True)

        segments: List[dict] = []
        base = os.path.splitext(os.path.basename(wav_path))[0]
        sample_rate_use = int(sample_rate) if sample_rate else 16000
        seg_index = 1

        for group in groups:
            if preserve_internal_silence:
                s0 = group[0][0]
                e0 = group[-1][1]
                s_str = "{:.3f}".format(s0)
                e_str = "{:.3f}".format(e0)
                seg_wav = os.path.join(out_dir, f"{base}_seg{seg_index:03d}.wav")
                ffmpeg_cmd = ["ffmpeg", "-i", wav_path, "-ss", s_str, "-to", e_str, "-ac", "1", seg_wav, "-y"]
                p = subprocess.run(ffmpeg_cmd, stdout=subprocess.PIPE, stderr=subprocess.PIPE, text=True)
                if p.returncode != 0:
                    logger.error("ffmpeg 导出连续区间失败: stderr=%s", p.stderr[:1000])
                    seg_index += 1
                    continue
            else:
                fc = _build_filter_complex(group)
                if not fc:
                    logger.warning("构建单组 filter_complex 失败，跳过 group")
                    seg_index += 1
                    continue
                seg_wav = os.path.join(out_dir, f"{base}_seg{seg_index:03d}.wav")
                ffmpeg_cmd = ["ffmpeg", "-i", wav_path, "-filter_complex", fc, "-map", "[out]", "-ac", "1", seg_wav, "-y"]
                p = subprocess.run(ffmpeg_cmd, stdout=subprocess.PIPE, stderr=subprocess.PIPE, text=True)
                if p.returncode != 0:
                    logger.error("ffmpeg 导出分段 wav 失败: stderr=%s", p.stderr[:1000])
                    seg_index += 1
                    continue

            out_ogg = os.path.join(out_dir, f"{base}_part{seg_index:03d}.ogg")
            try:
                encode_wav_to_opus(seg_wav, out_ogg, sample_rate=sample_rate_use, opus_bitrate=opus_bitrate)
            except Exception:
                logger.exception("编码分段到 opus 失败")
                out_ogg = seg_wav

            start_ms = int(round(group[0][0] * 1000))
            end_ms = int(round(group[-1][1] * 1000))
            duration_ms = end_ms - start_ms

            segments.append(
                {
                    "index": seg_index,
                    "file": out_ogg,
                    "temp_wav": seg_wav if keep_temp_wavs else None,
                    "start_ms": start_ms,
                    "end_ms": end_ms,
                    "cut_ms": end_ms,
                    "duration_ms": duration_ms,
                }
            )
            seg_index += 1
        # 如果需要查看移除静音的日志，请取消注释下面这一行
        # logger.info("已生成 %d 个分段到 %s (preserve_internal_silence=%s)", len(segments), out_dir, preserve_internal_silence)
        return segments
    except Exception:
        logger.exception("静音切片异常")
        return []


def encode_wav_to_opus(wav_input_path: str, ogg_output_path: str, sample_rate: int, opus_bitrate: str = "32k") -> None:
    """将 WAV 编码为 OGG/Opus（单声道）。"""
    (
        ffmpeg.input(wav_input_path)
        .output(
            ogg_output_path,
            acodec="libopus",
            audio_bitrate=opus_bitrate,
            ar=sample_rate,
            ac=1,
            format="ogg",
        )
        .overwrite_output()
        .run(capture_stdout=True, capture_stderr=True)
    )


async def retry_blocking_call(
    blocking_fn,
    *args,
    blocking_kwargs: Optional[dict] = None,
    max_attempts: int = 4,
    initial_delay: float = 0.5,
    factor: float = 2.0,
    max_delay: float = 8.0,
    acceptable_result_checker=None,
    executor_for_run=None,
):
    """异步上下文中执行阻塞调用并重试。"""
    blocking_kwargs = blocking_kwargs or {}
    attempt = 0
    delay = initial_delay
    loop = asyncio.get_running_loop()
    last_exc = None
    ctx = copy_context()

    while attempt < max_attempts:
        attempt += 1
        try:
            result = await loop.run_in_executor(
                executor_for_run or executor,
                lambda: ctx.run(lambda: blocking_fn(*args, **blocking_kwargs))
            )
            if acceptable_result_checker:
                try:
                    ok = acceptable_result_checker(result)
                except Exception:
                    ok = False
                if ok:
                    return result
                last_exc = result
            else:
                if isinstance(result, dict):
                    if result.get("status") == "success":
                        return result
                    last_exc = result
                else:
                    return result
        except Exception as e:
            last_exc = e
            logger.exception("阻塞调用异常")
        if attempt < max_attempts:
            await asyncio.sleep(min(delay, max_delay))
            delay *= factor

    logger.error("重试结束未成功，最后错误: %s", str(last_exc)[:1000])
    if isinstance(last_exc, dict):
        return last_exc
    return {"status": "error", "message": f"调用失败: {str(last_exc)}"}


def call_qwen3_asr_blocking(segment_path: str, model: str = "qwen3-asr-flash", asr_options: Optional[dict] = None, prompt: str = "") -> dict:
    """调用 dashscope MultiModalConversation.call（qwen3-asr）。"""
    try:
        # 如果需要查看移除静音的日志，请取消注释下面这一行
        # duration = get_audio_duration_seconds(segment_path)
        # logger.info("切片音频时长: %s 秒", duration)
        if not dashscope.api_key:
            return {"status": "error", "message": "DASHSCOPE_API_KEY 未配置"}
        from_path = Path(segment_path).resolve().as_uri()
        system_text = prompt or ""
        messages = [
            {"role": "system", "content": [{"text": system_text}]},
            {"role": "user", "content": [{"audio": from_path}]},
        ]
        asr_options_local: Dict[str, Any] = asr_options if asr_options is not None else {"enable_lid": True, "enable_itn": False}
        call_kwargs: Dict[str, Any] = {
            "api_key": dashscope.api_key,
            "model": model,
            "messages": messages,
            "result_format": "message",
            "asr_options": asr_options_local,
        }
        # 如果需要查看移除静音的日志，请取消注释下面这一行
        logger.info("调用 qwen3-asr: %s", segment_path)
        response = dashscope.MultiModalConversation.call(**call_kwargs)

        text_accum = ""
        try:
            output = response.get("output") if isinstance(response, dict) else getattr(response, "output", None)
            if output:
                choices = output.get("choices", [])
                if choices:
                    message = choices[0].get("message", {})
                    content = message.get("content", [])
                    for item in content:
                        if isinstance(item, dict) and item.get("text"):
                            text_accum += item.get("text", "")
            if not text_accum:
                def deep_find_text(obj):
                    if isinstance(obj, dict):
                        if "text" in obj and isinstance(obj["text"], str):
                            return obj["text"]
                        for v in obj.values():
                            t = deep_find_text(v)
                            if t:
                                return t
                    if isinstance(obj, list):
                        for it in obj:
                            t = deep_find_text(it)
                            if t:
                                return t
                    return ""
                text_accum = deep_find_text(response) or ""
        except Exception:
            logger.exception("解析 qwen 返回异常")
            return {"status": "error", "message": "解析识别结果失败"}

        # 如果提取到字段存在但为空（包括只含空白），覆盖为占位字符串以便上层区分出错段
        if isinstance(text_accum, str) and text_accum.strip() == "":
            text_accum = "【该段音频转录出错】"

        return {"status": "success", "text": text_accum}
    except Exception:
        logger.exception("qwen3-asr 调用异常")
        return {"status": "error", "message": "调用 qwen3-asr 失败"}


async def recognize_segments_concurrently(
    segments: List[dict],
    model: str,
    max_concurrency: Optional[int] = None,
    retry_params: dict = None,
    asr_options: Optional[dict] = None,
    prompt: str = "",
):
    """并发识别 segments，按 index 保序返回结果列表。"""
    retry_params = retry_params or {}
    if max_concurrency is None:
        max_concurrency = API_CONCURRENCY
    semaphore = asyncio.Semaphore(max_concurrency)

    async def worker(seg):
        async with semaphore:
            res = await retry_blocking_call(
                call_qwen3_asr_blocking,
                seg["file"],
                model,
                blocking_kwargs={"asr_options": asr_options, "prompt": prompt},
                executor_for_run=executor,
                **retry_params,
            )
            if not isinstance(res, dict):
                return {"index": seg["index"], "file": seg["file"], "status": "error", "message": "未知返回类型"}
            return {"index": seg["index"], "file": seg["file"], **res}

    tasks = [asyncio.create_task(worker(seg)) for seg in segments]
    results = await asyncio.gather(*tasks, return_exceptions=False)
    return sorted(results, key=lambda x: x["index"])


def _probe_duration_with_mediainfo(path: str) -> Optional[float]:
    try:
        p = subprocess.run(
            ["mediainfo", "--Output=JSON", path],
            stdout=subprocess.PIPE,
            stderr=subprocess.PIPE,
            text=True,
            timeout=8,
        )
        if p.returncode == 0 and p.stdout:
            try:
                obj = json.loads(p.stdout)
                media = obj.get("media", {}) if isinstance(obj, dict) else {}
                tracks = media.get("track", []) if isinstance(media, dict) else []
                for t in tracks:
                    if not isinstance(t, dict):
                        continue
                    # Duration / duration 都直接当秒用，不做 /1000
                    if "Duration" in t:
                        try:
                            dur_val = float(t.get("Duration"))
                            if dur_val > 0:
                                return dur_val
                        except Exception:
                            pass
                    if "duration" in t:
                        try:
                            dur_val = float(t.get("duration"))
                            if dur_val > 0:
                                return dur_val
                        except Exception:
                            pass
            except Exception:
                logger.debug("mediainfo JSON 解析失败")
    except FileNotFoundError:
        logger.debug("mediainfo 未安装，回退 ffprobe")
    except subprocess.TimeoutExpired:
        logger.warning("mediainfo 超时")
    except Exception:
        logger.debug("mediainfo 调用异常")
    return None


def _probe_duration_with_ffprobe_simple(path: str) -> Optional[float]:
    try:
        cmd = [
            "ffprobe", "-v", "error",
            "-show_entries", "format=duration",
            "-of", "default=noprint_wrappers=1:nokey=1", path,
        ]
        p = subprocess.run(cmd, stdout=subprocess.PIPE, stderr=subprocess.PIPE, text=True, timeout=8)
        if p.returncode == 0:
            out = (p.stdout or "").strip()
            if out:
                try:
                    return float(out)
                except Exception:
                    logger.debug("ffprobe 简单解析失败")
    except subprocess.TimeoutExpired:
        logger.warning("ffprobe 简单输出超时")
    except Exception:
        logger.debug("ffprobe 简单输出异常")
    return None


def _probe_duration_with_ffprobe_json(path: str) -> Optional[float]:
    try:
        cmd = ["ffprobe", "-v", "error", "-print_format", "json", "-show_format", path]
        p = subprocess.run(cmd, stdout=subprocess.PIPE, stderr=subprocess.PIPE, text=True, timeout=8)
        if p.returncode == 0 and p.stdout:
            try:
                obj = json.loads(p.stdout)
                fmt = obj.get("format", {}) if isinstance(obj, dict) else {}
                dur = fmt.get("duration")
                if dur:
                    return float(dur)
            except Exception:
                logger.debug("ffprobe json 解析失败")
    except subprocess.TimeoutExpired:
        logger.warning("ffprobe json 超时")
    except Exception:
        logger.debug("ffprobe json 异常")
    return None


def _probe_duration_with_ffmpeg(path: str) -> Optional[float]:
    try:
        cmd = ["ffmpeg", "-i", path, "-f", "null", "-"]
        p = subprocess.run(cmd, stdout=subprocess.PIPE, stderr=subprocess.PIPE, text=True, timeout=8)
        stderr = p.stderr or ""
        m = re.search(r"Duration:\s*([0-9]{2}:[0-9]{2}:[0-9]{2}\.?[0-9]*)", stderr)
        if m:
            hhmmss = m.group(1)
            try:
                parts = hhmmss.split(":")
                return float(parts[0]) * 3600.0 + float(parts[1]) * 60.0 + float(parts[2])
            except Exception:
                logger.debug("ffmpeg Duration 解析失败")
    except subprocess.TimeoutExpired:
        logger.warning("ffmpeg duration 超时")
    except Exception:
        logger.debug("ffmpeg duration 解析异常")
    return None


def _probe_duration_with_wave(path: str) -> Optional[float]:
    try:
        if str(path).lower().endswith(".wav"):
            with contextlib.closing(wave.open(path, "rb")) as wf:
                frames = wf.getnframes()
                rate = wf.getframerate()
                if rate and frames is not None:
                    return float(frames) / float(rate)
    except Exception:
        logger.debug("wave 读取失败")
    return None


def get_audio_duration_seconds(path: str, probe_order: str = "media_first") -> Optional[float]:
    """获取音频时长（秒），支持 media_first 或 wave_first 两种探测顺序。"""
    try:
        if probe_order == "media_first":
            probes = (
                _probe_duration_with_mediainfo,
                _probe_duration_with_ffprobe_simple,
                _probe_duration_with_ffprobe_json,
                _probe_duration_with_ffmpeg,
                _probe_duration_with_wave,
            )
        elif probe_order == "wave_first":
            probes = (
                _probe_duration_with_wave,
                _probe_duration_with_mediainfo,
                _probe_duration_with_ffprobe_simple,
                _probe_duration_with_ffprobe_json,
                _probe_duration_with_ffmpeg,
            )
        else:
            logger.warning("未知的音频时长探测顺序: %s，回退 media_first", probe_order)
            probes = (
                _probe_duration_with_mediainfo,
                _probe_duration_with_ffprobe_simple,
                _probe_duration_with_ffprobe_json,
                _probe_duration_with_ffmpeg,
                _probe_duration_with_wave,
            )

        for probe in probes:
            duration = probe(path)
            if duration is not None:
                return duration

        return None
    except Exception:
        logger.exception("获取时长发生异常")
        return None


def get_audio_duration_ms(file_path: str, probe_order: str = "media_first") -> int:
    """返回时长（毫秒）。"""
    secs = get_audio_duration_seconds(file_path, probe_order=probe_order)
    if secs is None:
        raise ValueError(f"无法获取文件时长: {file_path}")
    return int(round(secs * 1000))


# 固定切片 -> 局部静音裁剪 -> 合并
def _split_wav_fixed_intervals(wav_path: str, out_dir: str, slice_s: int = 3, sample_rate: Optional[int] = None) -> List[str]:
    """将 wav 切成固定 slice_s 秒的小片，返回片段路径列表。
    使用 ffmpeg 的 segment muxer 在单次进程调用中输出所有切片。
    """
    os.makedirs(out_dir, exist_ok=True)
    dur = get_audio_duration_seconds(wav_path, probe_order="wave_first")
    if dur is None or dur <= 0.0:
        raise ValueError("无法获取音频时长或时长为0: " + wav_path)

    base = os.path.splitext(os.path.basename(wav_path))[0]
    # 生成目标文件名模式（%04d 用于按索引命名）
    pattern = os.path.join(out_dir, f"{base}_slice%04d.wav")

    cmd = [
        "ffmpeg", "-hide_banner", "-loglevel", "error",
        "-i", wav_path,
        "-ac", "1",
    ]
    if sample_rate:
        cmd += ["-ar", str(sample_rate)]

    # 使用 segment muxer 一次性切分为多个文件
    cmd += [
        "-f", "segment",
        "-segment_time", str(slice_s),
        "-segment_format", "wav",
        "-reset_timestamps", "1",
        pattern,
        "-y",
    ]

    p = subprocess.run(cmd, stdout=subprocess.PIPE, stderr=subprocess.PIPE, text=True)
    if p.returncode != 0:
        logger.warning("segment 切片失败: stderr=%s", (p.stderr or "")[:1000])
        return []

    # 延迟导入以避免污染全局导入列表（保持与现有文件风格一致）
    import glob

    files = glob.glob(os.path.join(out_dir, f"{base}_slice*.wav"))
    # 按文件名中的索引排序（匹配最后的 4 位数字）
    def _sort_key(path_str: str):
        m = re.search(r"_(\d{4})\.wav$", path_str)
        if m:
            try:
                return int(m.group(1))
            except Exception:
                return path_str
        return path_str

    files_sorted = sorted(files, key=_sort_key)
    return files_sorted


def _concat_wavs_with_ffmpeg(file_list: List[str], out_wav: str) -> None:
    """使用 ffmpeg concat demuxer 合并 wav 列表（要求格式一致）。"""
    if not file_list:
        raise ValueError("file_list 为空，无法合并")
    tmp_list_txt = out_wav + ".concat_list.txt"
    with open(tmp_list_txt, "w", encoding="utf-8") as f:
        for p in file_list:
            f.write(f"file '{os.path.abspath(p)}'\n")
    cmd = ["ffmpeg", "-hide_banner", "-loglevel", "error", "-f", "concat", "-safe", "0", "-i", tmp_list_txt, "-c", "copy", out_wav, "-y"]
    p = subprocess.run(cmd, stdout=subprocess.PIPE, stderr=subprocess.PIPE, text=True)
    try:
        os.remove(tmp_list_txt)
    except Exception:
        pass
    if p.returncode != 0:
        logger.error("合并 wav 失败: stderr=%s", (p.stderr or "")[:1000])
        raise RuntimeError("ffmpeg concat 失败")


async def remove_silence_by_fixed_slices_and_merge(
    wav_path: str,
    out_merged_wav: Optional[str] = None,
    slice_s: Optional[int] = None,
    threshold_ms: Optional[int] = None,
    keep_ms: Optional[int] = None,
    sample_rate: Optional[int] = None,
    temp_dir: Optional[str] = None,
    max_workers: Optional[int] = None,
) -> str:
    """
    切成固定 slice_s 秒的小片，对每片运行 trim_long_silences_from_wav 做局部静音裁剪，
    支持并发处理分片，跳过空片并按序合并，返回合并后 wav 路径。

    max_workers: 并发线程数，优先使用传入值；若为 None 则读取环境变量 FFMPEG_WORKS（正整数），
    否则默认使用 16。
    """
    # 解析音频切片长度优先级：传入 > 环境变量 FFMPEG_SEGMENT_LENGTH > 默认 5 秒
    try:
        if slice_s is None:
            env_v = os.getenv("FFMPEG_SEGMENT_LENGTH")
            if env_v:
                try:
                    slice_s = int(env_v)
                except Exception:
                    slice_s = 5
            else:
                slice_s = 5
    except Exception:
        slice_s = 5
    # 解析音频分片合并填充区间优先级：传入 > 环境变量 PADDING_LENGTH > 默认 100 毫秒
    try:
        if keep_ms is None:
            env_v = os.getenv("PADDING_LENGTH")
            if env_v:
                try:
                    keep_ms = int(env_v)
                except Exception:
                    keep_ms = 100
            else:
                keep_ms = 100
    except Exception:
        keep_ms = 100
    # 解析音频静音区间长度优先级：传入 > 环境变量 SILENT_INTERVAL > 默认 700 毫秒
    try:
        if threshold_ms is None:
            env_v = os.getenv("SILENT_INTERVAL")
            if env_v:
                try:
                    threshold_ms = int(env_v)
                except Exception:
                    threshold_ms = 700
            else:
                threshold_ms = 700
    except Exception:
        threshold_ms = 700
    # 解析并发数优先级：传入 > 环境变量 FFMPEG_WORKS > 默认 16
    try:
        if max_workers is None:
            env_v = os.getenv("FFMPEG_WORKS")
            if env_v:
                try:
                    max_workers = int(env_v)
                except Exception:
                    max_workers = 16
            else:
                max_workers = 16
    except Exception:
        max_workers = 16
    try:
        max_workers = max(1, int(max_workers))
    except Exception:
        max_workers = 16

    if temp_dir is None:
        temp_dir = os.path.join(os.path.dirname(wav_path), "slice_temp")
    os.makedirs(temp_dir, exist_ok=True)
    base = os.path.splitext(os.path.basename(wav_path))[0]
    if out_merged_wav is None:
        out_merged_wav = os.path.join(os.path.dirname(wav_path), f"{base}_sliced_merged.wav")

    slice_dir = os.path.join(temp_dir, "slices")
    os.makedirs(slice_dir, exist_ok=True)
    slice_paths = _split_wav_fixed_intervals(wav_path, slice_dir, slice_s=slice_s, sample_rate=sample_rate)
    if not slice_paths:
        logger.warning("未生成任何固定切片，返回原始 wav")
        return wav_path

    trimmed_dir = os.path.join(temp_dir, "trimmed_slices")
    os.makedirs(trimmed_dir, exist_ok=True)

    # 如果需要查看移除静音的日志，请取消注释下面这一行
    # logger.info("并发处理固定切片，线程数: %d", max_workers)

    loop = asyncio.get_running_loop()

    def _process_slice(sp: str, out_trim: str, idx: int):
        """在线程池中运行的阻塞函数，返回结构化结果。"""
        try:
            trimmed = trim_long_silences_from_wav(
                sp,
                out_wav_path=out_trim,
                threshold_ms=threshold_ms,
                keep_ms=keep_ms,
                silence_thresh=None,
            )
            if isinstance(trimmed, str) and os.path.exists(trimmed):
                return {"index": idx, "trimmed": trimmed, "ok": True}
            return {"index": idx, "trimmed": None, "ok": False, "error": "no output"}
        except Exception as e:
            logger.exception("处理切片静音时失败，idx=%d", idx)
            return {"index": idx, "trimmed": None, "ok": False, "error": str(e)}

    # 使用线程池并发处理所有切片
    results: List[dict] = []
    try:
        from concurrent.futures import ThreadPoolExecutor as _TPE
        with _TPE(max_workers=max_workers) as tpe:
            tasks = []
            for sidx, sp in enumerate(slice_paths, start=1):
                out_trim = os.path.join(trimmed_dir, f"{base}_slice_trim{sidx:04d}.wav")
                tasks.append(loop.run_in_executor(tpe, _process_slice, sp, out_trim, sidx))
            results = await asyncio.gather(*tasks, return_exceptions=False)
    except Exception:
        logger.exception("并发处理切片时发生异常，尝试回退到串行处理")
        # 回退到串行实现
        results = []
        sidx = 1
        for sp in slice_paths:
            try:
                out_trim = os.path.join(trimmed_dir, f"{base}_slice_trim{sidx:04d}.wav")
                trimmed = trim_long_silences_from_wav(
                    sp,
                    out_wav_path=out_trim,
                    threshold_ms=threshold_ms,
                    keep_ms=keep_ms,
                    silence_thresh=None,
                )
                if isinstance(trimmed, str) and os.path.exists(trimmed):
                    results.append({"index": sidx, "trimmed": trimmed, "ok": True})
                else:
                    results.append({"index": sidx, "trimmed": None, "ok": False, "error": "no output"})
            except Exception as e:
                logger.exception("处理切片静音时失败，跳过该片 idx=%d", sidx)
                results.append({"index": sidx, "trimmed": None, "ok": False, "error": str(e)})
            sidx += 1

    # 按 index 排序并筛选有效的 trimmed_paths（时长 >= 0.01s）
    trimmed_paths: List[str] = []
    for r in sorted(results, key=lambda x: x.get("index", 0)):
        if r.get("ok") and r.get("trimmed") and os.path.exists(r.get("trimmed")):
            try:
                dur = get_audio_duration_seconds(r.get("trimmed"), probe_order="wave_first") or 0.0
            except Exception:
                dur = 0.0
            if dur >= 0.01:
                trimmed_paths.append(r.get("trimmed"))
            else:
                logger.debug("裁剪后片段过短，跳过: %s", r.get("trimmed"))
        else:
            logger.debug("裁剪返回异常或文件不存在，跳过 idx=%s, err=%s", r.get("index"), r.get("error"))

    if not trimmed_paths:
        logger.warning("所有切片裁剪后均为空，返回原始 wav")
        return wav_path

    try:
        _concat_wavs_with_ffmpeg(trimmed_paths, out_merged_wav)
    except Exception:
        logger.exception("concat demuxer 合并失败，尝试 filter_complex fallback")
        try:
            inputs = []
            filter_parts = []
            for idx, p in enumerate(trimmed_paths):
                inputs += ["-i", p]
                filter_parts.append(f"[{idx}:0:a]")
            concat_fc = "".join(filter_parts) + f"concat=n={len(trimmed_paths)}:v=0:a=1[out]"
            cmd = ["ffmpeg", "-hide_banner", "-loglevel", "error"] + inputs + ["-filter_complex", concat_fc, "-map", "[out]", "-ac", "1", out_merged_wav, "-y"]
            p = subprocess.run(cmd, stdout=subprocess.PIPE, stderr=subprocess.PIPE, text=True)
            if p.returncode != 0:
                logger.error("fallback 合并失败: %s", (p.stderr or "")[:1000])
                raise RuntimeError("合并失败")
        except Exception:
            raise
    # 如果需要查看移除静音的日志，请取消注释下面这一行
    # logger.info("完成固定切片裁剪并合并: %s -> %s (片段数=%d)", wav_path, out_merged_wav, len(trimmed_paths))
    logger.info("完成切片裁剪并合并，片段数: %d", len(trimmed_paths))
    return out_merged_wav


async def process_audio_file(
    file: UploadFile,
    model: str,
    endpoint_path: str,
    language: str = "",
    prompt: str = "",
    enable_lid: bool = True,
    enable_itn: bool = False,
):
    """主处理流程：保存、转码、先固定切片局部裁剪合并，再按模型走 QWEN 并发或整体识别路径。"""
    request_id = str(uuid.uuid4())
    request_id_var.set(request_id)
    lang_code = normalize_language_code(language)
    logger.info(
        "收到请求 %s, 文件: %s, 模型: %s, language: %s, enable_lid: %s, enable_itn: %s",
        endpoint_path,
        file.filename,
        model,
        lang_code,
        enable_lid,
        enable_itn,
    )

    if not file.filename:
        raise HTTPException(status_code=400, detail="未选择文件")

    temp_dir = ""
    try:
        try:
            resolved_model = model_name_map(model)
            sample_rate = get_sample_rate_by_model(resolved_model)
            logger.info("解析到模型: %s, 采样率: %d", resolved_model, sample_rate)
        except ValueError as e:
            raise HTTPException(status_code=400, detail=str(e))

        try:
            file_path, temp_dir = create_temp_dir_and_save(file, request_id)
            # 如果需要查看移除静音的日志，请取消注释下面这一行
            # logger.info("保存上传文件: %s", file_path)
        except Exception as e:
            logger.exception("保存上传文件失败")
            raise HTTPException(status_code=500, detail=f"保存文件错误: {str(e)}")

        try:
            total_ms = get_audio_duration_ms(file_path)
            logger.info("音频总时长: %d ms", total_ms)
        except Exception as e:
            logger.exception("获取音频时长失败")
            raise HTTPException(status_code=500, detail=f"获取音频信息失败: {str(e)}")

        retry_params = {
            "max_attempts": ASR_RETRY_MAX_ATTEMPTS,
            "initial_delay": ASR_RETRY_INITIAL_DELAY,
            "factor": ASR_RETRY_FACTOR,
            "max_delay": ASR_RETRY_MAX_DELAY,
        }

        try:
            wav_path = os.path.join(temp_dir, "converted.wav")
            preconvert_to_wav(file_path, wav_path, sample_rate)

            try:
                merged_wav = await remove_silence_by_fixed_slices_and_merge(
                    wav_path,
                    out_merged_wav=os.path.join(temp_dir, "converted_sliced_merged.wav"),
                    sample_rate=sample_rate,
                    temp_dir=os.path.join(temp_dir, "fixed_slice_temp"),
                )
            except Exception:
                logger.exception("固定切片并合并失败，回退使用原始 wav")
                merged_wav = wav_path

            out_dir = os.path.join(temp_dir, "out_segments")
            os.makedirs(out_dir, exist_ok=True)
            # 解析音频分片合并填充区间优先级：环境变量 PADDING_LENGTH > 默认 100 毫秒
            # 解析音频静音区间长度优先级：环境变量 SILENT_INTERVAL > 默认 700 毫秒
            keep_ms = int(os.getenv("PADDING_LENGTH", "100"))
            threshold_ms = int(os.getenv("SILENT_INTERVAL", "700"))
            # 这里只切片不移除组内静音。
            segments = split_wav_by_silence_groups(
                merged_wav,
                threshold_ms=threshold_ms,
                keep_ms=keep_ms,
                silence_thresh=None,
                sample_rate=sample_rate,
                max_segment_len_s=API_SEGMENT_LENGTH,
                opus_bitrate="32k",
                out_dir=out_dir,
                keep_temp_wavs=True,
                preserve_internal_silence=True,
            )
            if not segments:
                logger.error("未生成分段列表")
                raise HTTPException(status_code=500, detail="未能生成分段")
            total_after_ms = sum(int(seg.get("duration_ms", 0)) for seg in segments)
            logger.info("切片并生成 %d 个分段, 总时长: %d ms", len(segments), total_after_ms)

            asr_opts_for_segments: dict = {"enable_lid": enable_lid, "enable_itn": enable_itn}
            if lang_code:
                asr_opts_for_segments["language"] = lang_code

            recognize_results = await recognize_segments_concurrently(
                segments,
                resolved_model,
                retry_params=retry_params,
                asr_options=asr_opts_for_segments,
                prompt=prompt,
            )

            concatenated_text = ""
            for r in recognize_results:
                if r.get("status") != "success":
                    logger.error("分片识别失败: index=%s, msg=%s", r.get("index"), r.get("message"))
                    raise HTTPException(status_code=500, detail=f"分片识别失败: index={r.get('index')}, msg={r.get('message')}")
                concatenated_text += r.get("text", "")

            result = {"status": "success", "text": concatenated_text}
            logger.info("请求处理完毕")
            return JSONResponse(content=result)
        except Exception:
            logger.exception("移除静音/切片/识别流程失败")
            raise HTTPException(status_code=400, detail="音频处理失败")
    except HTTPException:
        raise
    except Exception:
        logger.exception("处理请求时发生未知错误")
        raise HTTPException(status_code=500, detail="处理请求时发生未知服务器错误。")
    finally:
        if temp_dir and os.path.exists(temp_dir):
            logger.info("清理临时目录: %s", temp_dir)
            shutil.rmtree(temp_dir, ignore_errors=True)
        request_id_var.set("N/A")


@app.post("/upload_audio", dependencies=[Depends(verify_token)])
async def upload_audio(
    audio: UploadFile = File(...),
    model: str = Form(...),
    language: str = Form(""),
    prompt: str = Form(""),
    enable_lid: bool = Form(True),
    enable_itn: bool = Form(False),
):
    """兼容上传接口。"""
    return await process_audio_file(
        audio,
        model,
        "/upload_audio",
        language,
        prompt,
        enable_lid,
        enable_itn,
    )


@app.post("/v1/audio/transcriptions", dependencies=[Depends(verify_token)])
async def audio_transcriptions(
    file: UploadFile = File(...),
    model: str = Form(...),
    language: str = Form(""),
    prompt: str = Form(""),
    enable_lid: bool = Form(True),
    enable_itn: bool = Form(False),
):
    # 调试：打印并记录收到的表单字段（区分普通字段与文件）
    # form = await request.form()
    # items = []
    # for k, v in form.items():
    #     if hasattr(v, "filename"):
    #         items.append(f"{k}=UploadFile(filename={getattr(v, 'filename', None)}, content_type={getattr(v, 'content_type', None)})")
    #     else:
    #         items.append(f"{k}={v}")
    # logger.info("收到原始表单字段: %s", "; ".join(items))
    # print("DEBUG form:", items)

    """OpenAI 风格转写兼容接口。"""
    return await process_audio_file(
        file,
        model,
        "/v1/audio/transcriptions",
        language,
        prompt,
        enable_lid,
        enable_itn,
    )
