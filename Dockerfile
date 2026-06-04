# Copyright (C) 2026 Joey Kot <joey.kot.x@gmail.com>
#
# This program is free software: you can redistribute it and/or modify
# it under the terms of the GNU General Public License as published by
# the Free Software Foundation, either version 3 of the License, or
# (at your option) any later version.
#
# This program is distributed WITHOUT ANY WARRANTY; without even the
# implied warranty of MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE.
# See <https://www.gnu.org/licenses/> for more details.

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
