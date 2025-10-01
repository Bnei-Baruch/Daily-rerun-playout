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
  if [ "${USER_INSTALL:-0}" = "1" ]; then
    echo "  symlink $SRC_BIN -> $BIN_DST/lesson-controller"
    ln -sfn "$SRC_BIN" "$BIN_DST/lesson-controller"
  else
    echo "  install $SRC_BIN -> $BIN_DST/lesson-controller"
    install -m 0755 -o "$OWNER" -g "$GROUP" "$SRC_BIN" "$BIN_DST/lesson-controller"
  fi
else
  echo "[install-controller] ERROR: no binary to install. Aborting without changes."
  exit 1
fi

# Wrapper runner
WRAP="$BIN_DST/lesson_controller"
if [ "${USER_INSTALL:-0}" = "1" ]; then
  echo "  symlink runner $WRAP -> $BIN_DST/lesson-controller"
  ln -sfn "$BIN_DST/lesson-controller" "$WRAP"
  echo "[install-controller] done. Start with: $WRAP"
else
  echo "  create runner $WRAP"
  cat > "$WRAP" <<'SH'
#!/bin/sh
set -e
LOG_DIR="/opt/backup/logs"
mkdir -p "$LOG_DIR"
AUDIO_MAP_PATH="${AUDIO_MAP_PATH:-/Users/shidur/Projects/sviva-playout/controller/audio_map.json.sample}"
PORT="${PORT:-8788}"
GO_GST="${GO_GST:-1}"
export AUDIO_MAP_PATH
export GO_GST
exec /usr/local/bin/lesson-controller "$@" 2>>"$LOG_DIR/lesson_controller.err" 1>>"$LOG_DIR/lesson_controller.out"
SH
  chown "$OWNER:$GROUP" "$WRAP"
  chmod 0755 "$WRAP"

  touch "$LOG_DIR/lesson_controller.out" "$LOG_DIR/lesson_controller.err"
  chown "$RUNTIME_OWNER:$RUNTIME_GROUP" "$LOG_DIR/lesson_controller.out" "$LOG_DIR/lesson_controller.err" || true
  chmod 664 "$LOG_DIR/lesson_controller.out" "$LOG_DIR/lesson_controller.err" || true

  echo "[install-controller] done. Start with: sudo -u $RUNTIME_OWNER $WRAP"
fi



