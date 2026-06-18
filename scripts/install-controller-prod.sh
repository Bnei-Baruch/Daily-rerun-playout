#!/usr/bin/env bash
set -euo pipefail

# repo root
REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
CONTROLLER_DIR="$REPO_ROOT/controller"
BIN_DST="/usr/local/bin"
LOG_DIR="/opt/backup/logs"

OWNER="${OWNER:-root}"
GROUP="${GROUP:-wheel}"
RUNTIME_OWNER="${RUNTIME_OWNER:-shidur}"
RUNTIME_GROUP="${RUNTIME_GROUP:-staff}"

# If USER_INSTALL=1, install for current user without sudo by creating symlinks in ~/bin
if [ "${USER_INSTALL:-0}" = "1" ]; then
  BIN_DST="$HOME/bin"
  echo "[install-controller] USER_INSTALL=1 → using BIN_DST=$BIN_DST (symlinks)"
fi

echo "[install-controller] from: $CONTROLLER_DIR"
echo "[install-controller] to:   $BIN_DST  logs=$LOG_DIR"

mkdir -p "$BIN_DST"
if [ "${USER_INSTALL:-0}" != "1" ]; then
  mkdir -p "$LOG_DIR"
  chown "$RUNTIME_OWNER:$RUNTIME_GROUP" "$LOG_DIR" || true
  chmod 775 "$LOG_DIR" || true
fi

build_local_binary() {
  echo "[install-controller] building lesson-controller (local build on production host)"
  (
    cd "$CONTROLLER_DIR"
    if [ ! -f go.sum ]; then
      echo "[install-controller] running go mod tidy..."
      go mod tidy || true
    fi
    if [ "${GO_GST:-1}" = "1" ]; then
      echo "[install-controller] attempting go-gst build (-tags gogst)"
      if go build -tags gogst -o lesson-controller; then
        exit 0
      else
        echo "[install-controller] go-gst build failed, falling back to standard build"
      fi
    fi
    go build -o lesson-controller
  )
}

SRC_BIN="$CONTROLLER_DIR/lesson-controller"

if [[ -x "$SRC_BIN" ]]; then
  echo "[install-controller] using existing binary: $SRC_BIN"
else
  if command -v go >/dev/null 2>&1; then
    build_local_binary || true
  else
    echo "[install-controller] go not found; cannot build. If you built locally, ensure controller/lesson-controller exists before deploy."
  fi
fi

if [[ -x "$SRC_BIN" ]]; then
  # Install binary to a private location
  PRIVATE_BIN="$BIN_DST/.lesson-controller.bin"
  if [ "${USER_INSTALL:-0}" = "1" ]; then
    echo "  symlink $SRC_BIN -> $PRIVATE_BIN"
    ln -sfn "$SRC_BIN" "$PRIVATE_BIN"
  else
    echo "  install binary to $PRIVATE_BIN"
    install -m 0755 -o "$OWNER" -g "$GROUP" "$SRC_BIN" "$PRIVATE_BIN"
  fi
else
  echo "[install-controller] ERROR: no binary to install. Aborting without changes."
  exit 1
fi

# Create wrapper as the main executable
WRAP="$BIN_DST/lesson-controller"
if [ "${USER_INSTALL:-0}" = "1" ]; then
  echo "  create user wrapper $WRAP"
  cat > "$WRAP" <<SH
#!/bin/sh
set -e
AUDIO_MAP_PATH="\${AUDIO_MAP_PATH:-$HOME/Projects/sviva-playout/controller/audio_map.json.sample}"
PNG_PATH="\${PNG_PATH:-$HOME/shidur.png}"
DECKLINK_DEVICE="\${DECKLINK_DEVICE:-0}"
DECKLINK_MODE="\${DECKLINK_MODE:-1080i50}"
PLAYLIST_DIR="\${PLAYLIST_DIR:-$HOME/playlist}"
PRERUN_VIDEO_PATH="\${PRERUN_VIDEO_PATH:-/opt/backup/prerun.mp4}"
FFMPEG_PATH="\${FFMPEG_PATH:-/opt/homebrew/bin/ffmpeg}"
PORT="\${PORT:-8788}"
GO_GST="\${GO_GST:-1}"
DYLD_LIBRARY_PATH="/opt/homebrew/lib\${DYLD_LIBRARY_PATH:+:\$DYLD_LIBRARY_PATH}"
JACK_DEFAULT_SERVER="default"
GST_PLUGIN_SYSTEM_PATH="/opt/homebrew/lib/gstreamer-1.0"
GST_PLUGIN_PATH="/opt/homebrew/lib/gstreamer-1.0"
export AUDIO_MAP_PATH PNG_PATH DECKLINK_DEVICE DECKLINK_MODE PLAYLIST_DIR PRERUN_VIDEO_PATH FFMPEG_PATH PORT GO_GST DYLD_LIBRARY_PATH JACK_DEFAULT_SERVER GST_PLUGIN_SYSTEM_PATH GST_PLUGIN_PATH
exec "$PRIVATE_BIN" "\$@"
SH
  chmod 0755 "$WRAP"
  echo "[install-controller] done. Start with: $WRAP"
else
  echo "  create system wrapper $WRAP"
  cat > "$WRAP" <<'SH'
#!/bin/sh
set -e
LOG_DIR="/opt/backup/logs"
mkdir -p "$LOG_DIR"

# Configuration defaults
AUDIO_MAP_PATH="${AUDIO_MAP_PATH:-/Users/shidur/Projects/sviva-playout/controller/audio_map.json.sample}"
PNG_PATH="${PNG_PATH:-/Users/shidur/shidur.png}"
DECKLINK_DEVICE="${DECKLINK_DEVICE:-0}"
DECKLINK_MODE="${DECKLINK_MODE:-1080i50}"
PLAYLIST_DIR="${PLAYLIST_DIR:-/opt/backup/playlist}"
PRERUN_VIDEO_PATH="${PRERUN_VIDEO_PATH:-/opt/backup/prerun.mp4}"
FFMPEG_PATH="${FFMPEG_PATH:-/opt/homebrew/bin/ffmpeg}"
PORT="${PORT:-8788}"
GO_GST="${GO_GST:-1}"

# macOS dynamic library path for JACK/GStreamer
DYLD_LIBRARY_PATH="/opt/homebrew/lib${DYLD_LIBRARY_PATH:+:$DYLD_LIBRARY_PATH}"

# JACK environment - ensure we can find the server
JACK_DEFAULT_SERVER="default"

# GStreamer plugin paths
GST_PLUGIN_SYSTEM_PATH="/opt/homebrew/lib/gstreamer-1.0"
GST_PLUGIN_PATH="/opt/homebrew/lib/gstreamer-1.0"

# Export all configuration
export AUDIO_MAP_PATH PNG_PATH DECKLINK_DEVICE DECKLINK_MODE PLAYLIST_DIR PRERUN_VIDEO_PATH FFMPEG_PATH PORT GO_GST DYLD_LIBRARY_PATH JACK_DEFAULT_SERVER GST_PLUGIN_SYSTEM_PATH GST_PLUGIN_PATH

exec /usr/local/bin/.lesson-controller.bin "$@" 2>>"$LOG_DIR/lesson_controller.err" 1>>"$LOG_DIR/lesson_controller.out"
SH
  chown "$OWNER:$GROUP" "$WRAP"
  chmod 0755 "$WRAP"

  touch "$LOG_DIR/lesson_controller.out" "$LOG_DIR/lesson_controller.err"
  chown "$RUNTIME_OWNER:$RUNTIME_GROUP" "$LOG_DIR/lesson_controller.out" "$LOG_DIR/lesson_controller.err" || true
  chmod 664 "$LOG_DIR/lesson_controller.out" "$LOG_DIR/lesson_controller.err" || true

  echo "[install-controller] done. Start with: sudo -u $RUNTIME_OWNER $WRAP"
fi



