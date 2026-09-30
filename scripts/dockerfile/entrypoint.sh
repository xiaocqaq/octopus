#!/bin/sh
set -e

PUID="${PUID:-0}" # 容器进程使用的用户 ID。
PGID="${PGID:-0}" # 容器进程使用的用户组 ID。
TZ="${TZ:-UTC}" # 容器进程时区, 默认 UTC。

chmod +x "/app/${APP_NAME}"
mkdir -p /app/data
chown -R "$PUID:$PGID" /app/data
cd /app
exec su-exec "$PUID:$PGID" env TZ="$TZ" "./${APP_NAME}" start
