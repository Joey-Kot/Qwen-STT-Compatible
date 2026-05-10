#!/bin/sh
cd /
exec uvicorn audio:app --host 0.0.0.0 --port 8080 --workers 16
