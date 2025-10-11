#!/bin/zsh
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
ROOT_DIR="$(cd "$SCRIPT_DIR/.." && pwd)"

CONTROLLER_DIR="$ROOT_DIR/controller"

if ! command -v go >/dev/null 2>&1; then
  echo "go toolchain not found in PATH" >&2
  exit 1
fi

cd "$CONTROLLER_DIR"
if [ ! -f go.sum ]; then
  echo "Initializing Go module dependencies..."
  go mod tidy
fi
echo "Building lesson-controller (entire package)..."
if [ "${GO_GST:-0}" = "1" ]; then
  echo "Using go-gst build tag"
  go build -tags gogst -o lesson-controller
else
  go build -o lesson-controller
fi

# Set default environment variables if not already set
: "${AUDIO_MAP_PATH:=$CONTROLLER_DIR/audio_map.json.sample}"
: "${PNG_PATH:=/Users/shidur/shidur.png}"
: "${DECKLINK_DEVICE:=0}"
: "${DECKLINK_MODE:=1080i50}"
: "${PLAYLIST_DIR:=/opt/backup/playlist}"
: "${FFMPEG_PATH:=/opt/homebrew/bin/ffmpeg}"
: "${PORT:=8788}"
: "${DYLD_LIBRARY_PATH:=/opt/homebrew/lib}"

export AUDIO_MAP_PATH PNG_PATH DECKLINK_DEVICE DECKLINK_MODE PLAYLIST_DIR FFMPEG_PATH PORT DYLD_LIBRARY_PATH

echo "Starting controller with:"
echo "  AUDIO_MAP_PATH=$AUDIO_MAP_PATH"
echo "  PNG_PATH=$PNG_PATH"
echo "  DECKLINK_DEVICE=$DECKLINK_DEVICE"
echo "  DECKLINK_MODE=$DECKLINK_MODE"
echo "  PORT=$PORT"
exec ./lesson-controller


