#!/bin/zsh
set -euo pipefail

# Paths (edit if your install differs)
MEDIA_STREAMER_DIR="${MEDIA_STREAMER_DIR:-$HOME/apps/media-streamer-master 2}"
SVIVA_DIR="${SVIVA_DIR:-/Users/shidur/Projects/sviva-playout}"

# Ports
export PORT=${PORT:-8081}
export STATUS_HTTP_PORT=${STATUS_HTTP_PORT:-8787}
export MEDIA_STREAMER_URL="http://127.0.0.1:${PORT}"

if ! command -v tmux >/dev/null 2>&1; then
  echo "tmux is required. Install via: brew install tmux" >&2
  exit 1
fi

# Start a tmux session with two panes: media-streamer and status server
SESSION="hls"

tmux has-session -t "$SESSION" 2>/dev/null && tmux kill-session -t "$SESSION"

tmux new-session -d -s "$SESSION" "cd '${MEDIA_STREAMER_DIR}' && PORT=${PORT} ./media-streamer" \
  \; split-window -v -t "$SESSION" "cd '${SVIVA_DIR}' && export MEDIA_STREAMER_URL=${MEDIA_STREAMER_URL} STATUS_HTTP_PORT=${STATUS_HTTP_PORT}; python3 server/status_http.py" \
  \; select-layout even-vertical \
  \; attach -t "$SESSION"


