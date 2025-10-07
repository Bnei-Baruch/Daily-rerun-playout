#!/bin/bash
set -euo pipefail
STATE_DIR=/opt/backup/state
LOG_DIR=/opt/backup/logs
PID_FILE="$STATE_DIR/jackd.pid"
JSON="$STATE_DIR/jack.json"
JACK=/opt/homebrew/opt/jack/bin/jackd
CAPTURE_DEV="AUDINATE-DVS-UID"
PLAYBACK_DEV="AUDINATE-DVS-UID"
RATE=48000
PERIOD=256
NPER=3

mkdir -p "$STATE_DIR" "$LOG_DIR"
export JACK_NO_START_SERVER=1

# If a launchd service exists, don't fight it
if launchctl print system/org.bb.jackd >/dev/null 2>&1; then
  echo "jackd managed by launchd (org.bb.jackd); not starting."
  exit 0
fi

# Already running?
if pgrep -x jackd >/dev/null; then
  pid=$(pgrep -x jackd | head -n1)
  echo "$pid" >"$PID_FILE"
  printf '{"running":true,"pid":%d,"rate":%d,"buffer":%d,"nperiods":%d}\n' "$pid" "$RATE" "$PERIOD" "$NPER" >"$JSON"
  exit 0
fi

# Best-effort presence check (non-fatal)
if ! /usr/local/bin/ffmpeg-bb -hide_banner -f audiotoolbox -list_devices 1 -i "" 2>&1 | grep -q "$CAPTURE_DEV"; then
  echo "Warning: '$CAPTURE_DEV' not detected; retrying in 3s..."
  sleep 3
  if ! /usr/local/bin/ffmpeg-bb -hide_banner -f audiotoolbox -list_devices 1 -i "" 2>&1 | grep -q "$CAPTURE_DEV"; then
    echo "Warning: '$CAPTURE_DEV' still not detected; starting jackd anyway."
  fi
fi

nohup "$JACK" -R -d coreaudio -C "$CAPTURE_DEV" -P "$PLAYBACK_DEV" \
  -r "$RATE" -p "$PERIOD" -n "$NPER" >"$LOG_DIR/jackd.log" 2>&1 &
pid=$!
echo "$pid" >"$PID_FILE"
printf '{"running":true,"pid":%d,"rate":%d,"buffer":%d,"nperiods":%d}\n' "$pid" "$RATE" "$PERIOD" "$NPER" >"$JSON"
exit 0
