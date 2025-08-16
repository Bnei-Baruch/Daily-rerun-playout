#!/bin/bash
set -euo pipefail
STATE_DIR=/opt/backup/state
PID_FILE="$STATE_DIR/jackd.pid"
JSON="$STATE_DIR/jack.json"

PID=""
if [ -f "$PID_FILE" ]; then PID=$(cat "$PID_FILE" || true); fi
if [ -z "$PID" ]; then PID=$(pgrep -x jackd || true); fi

if [ -n "$PID" ]; then
  kill -INT "$PID" 2>/dev/null || true
  sleep 1
  kill -TERM "$PID" 2>/dev/null || true
  sleep 1
fi
pgrep -x jackd >/dev/null && pkill -9 -x jackd || true
rm -f "$PID_FILE"
printf '{"running":false}\n' >"$JSON"
echo "stopped"
