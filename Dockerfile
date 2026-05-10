FROM alpine:3.23.4

# 安装依赖，包括 python3、python3-venv、ffmpeg、bash 等
RUN apk update \
    && apk add python3 \
    && apk add py3-pip \
    && apk add ffmpeg \
    && apk add mediainfo \
    && apk add vim

# 安装 python 包
RUN python -m pip install --upgrade pip --no-cache-dir --break-system-packages \
    && pip install dashscope \
    ffmpeg-python \
    fastapi \
    uvicorn \
    python-multipart \
    python-dotenv \
    --no-cache-dir --break-system-packages

# 工作目录
WORKDIR /

# 复制代码和启动脚本
COPY audio.py /audio.py
COPY start.sh /start.sh

RUN chmod +x /start.sh

# 暴露端口
EXPOSE 8080

# 启动命令
CMD ["sh", "/start.sh"]
