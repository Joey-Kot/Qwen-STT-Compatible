#!/bin/sh
cd /
exec gunicorn -w 16 -k gthread --threads 4 -b 0.0.0.0:8080 audio:app
