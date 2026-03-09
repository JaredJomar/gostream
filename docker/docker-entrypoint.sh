#!/bin/sh
set -eu

CONFIG_PATH="${MKV_PROXY_CONFIG_PATH:-/config.json}"
ROOT_PATH="${GOSTREAM_ROOT_PATH:-/usr/local}"
SOURCE_PATH="${GOSTREAM_SOURCE_PATH:-/mnt/gostream-mkv-real}"
MOUNT_PATH="${GOSTREAM_MOUNT_PATH:-/mnt/gostream-mkv-virtual}"
STATE_DIR="${GOSTREAM_STATE_DIR:-$ROOT_PATH/STATE}"
LOG_DIR="${GOSTREAM_LOG_DIR:-$ROOT_PATH/logs}"
HEALTH_MONITOR_ENABLED="${GOSTREAM_HEALTH_MONITOR_ENABLED:-1}"
HEALTH_MONITOR_PORT="${HEALTH_MONITOR_PORT:-8095}"

mkdir -p "$SOURCE_PATH" "$MOUNT_PATH" "$ROOT_PATH" "$STATE_DIR" "$LOG_DIR"

# Manual recovery mode: do not unmount at startup. The stale-FUSE cleanup can
# break bind propagation back to the host Plex path. We keep the shutdown
# cleanup below, but leave the startup mount topology intact.
if mountpoint -q "$MOUNT_PATH" 2>/dev/null; then
  echo "Existing mountpoint at $MOUNT_PATH detected; leaving it intact on startup." >&2
fi

if [ ! -f "$CONFIG_PATH" ]; then
  echo "Missing required config file at $CONFIG_PATH" >&2
  exit 1
fi

health_pid=""
gostream_pid=""

shutdown() {
  trap - INT TERM EXIT

  if [ -n "$health_pid" ] && kill -0 "$health_pid" 2>/dev/null; then
    kill "$health_pid" 2>/dev/null || true
  fi

  if [ -n "$gostream_pid" ] && kill -0 "$gostream_pid" 2>/dev/null; then
    kill -TERM "$gostream_pid" 2>/dev/null || true
  fi

  wait ${health_pid:+"$health_pid"} ${gostream_pid:+"$gostream_pid"} 2>/dev/null || true
  fusermount3 -uz "$MOUNT_PATH" 2>/dev/null || true
}

trap shutdown INT TERM EXIT

if [ "$HEALTH_MONITOR_ENABLED" = "1" ]; then
  echo "Starting health monitor on port $HEALTH_MONITOR_PORT" >&2
  python3 /app/scripts/health-monitor.py &
  health_pid="$!"
fi

echo "Starting gostream" >&2
/usr/local/bin/gostream --path "$ROOT_PATH" "$SOURCE_PATH" "$MOUNT_PATH" &
gostream_pid="$!"

while :; do
  if [ -n "$health_pid" ] && ! kill -0 "$health_pid" 2>/dev/null; then
    wait "$health_pid" || true
    echo "health-monitor exited; stopping container" >&2
    exit 1
  fi

  if ! kill -0 "$gostream_pid" 2>/dev/null; then
    wait "$gostream_pid"
    exit_code=$?
    fusermount3 -uz "$MOUNT_PATH" 2>/dev/null || true
    echo "gostream exited; stopping container" >&2
    exit "$exit_code"
  fi

  sleep 1
done
