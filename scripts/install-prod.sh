#!/usr/bin/env bash
set -euo pipefail

# repo root (this script is in scripts/)
REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
BIN_DST="/usr/local/bin"
STATE_DIR="/opt/backup/state"
LOG_DIR="/opt/backup/logs"

OWNER="${OWNER:-root}"
GROUP="${GROUP:-wheel}"
RUNTIME_OWNER="${RUNTIME_OWNER:-shidur}"
RUNTIME_GROUP="${RUNTIME_GROUP:-staff}"

echo "[install] from: $REPO_ROOT"
echo "[install] to:   $BIN_DST  state=$STATE_DIR  logs=$LOG_DIR"
echo "[install] file owner: $OWNER:$GROUP  runtime owner: $RUNTIME_OWNER:$RUNTIME_GROUP"

# find a source file in common locations (server/, bin/, repo root)
pick_src() {
  local fn="$1"
  for d in "server" "bin" "."; do
    if [[ -f "$REPO_ROOT/$d/$fn" ]]; then
      echo "$REPO_ROOT/$d/$fn"
      return 0
    fi
  done
  return 1
}

install_if() {
  local src="$1" dst="$2" mode="$3"
  if [[ -n "$src" && -f "$src" ]]; then
    if [[ -e "$dst" ]]; then
      local resolved_src resolved_dst
      resolved_src="$(python3 -c 'import os,sys; print(os.path.realpath(sys.argv[1]))' "$src")"
      resolved_dst="$(python3 -c 'import os,sys; print(os.path.realpath(sys.argv[1]))' "$dst")" || true
      if [[ -n "$resolved_dst" && "$resolved_src" == "$resolved_dst" ]]; then
        echo "  removing existing $dst (identical to source)"
        rm -f "$dst"
      fi
    fi
    echo "  install $src -> $dst"
    install -m "$mode" -o "$OWNER" -g "$GROUP" "$src" "$dst"
  else
    echo "  skip (missing): $dst"
  fi
}

mkdir -p "$BIN_DST" "$STATE_DIR" "$LOG_DIR"
chown "$RUNTIME_OWNER:$RUNTIME_GROUP" "$STATE_DIR" "$LOG_DIR" || true
chmod 775 "$STATE_DIR" "$LOG_DIR" || true

# --- status_http.py and wrapper (status_http) ---
SRC_STATUS="$(pick_src status_http.py || true)"
install_if "${SRC_STATUS:-}" "$BIN_DST/status_http.py" 0755

# wrapper script (create if not tracked)
if SRC_WRAPPER="$(pick_src status_http || true)"; then
  install_if "${SRC_WRAPPER:-}" "$BIN_DST/status_http" 0755
else
  echo "  create wrapper $BIN_DST/status_http"
  cat > "$BIN_DST/status_http" <<'SH'
#!/bin/sh
exec /usr/bin/env python3 /usr/local/bin/status_http.py "$@"
SH
  chown "$OWNER:$GROUP" "$BIN_DST/status_http"
  chmod 0755 "$BIN_DST/status_http"
fi

# --- other tools (kept where they are; pick from server/ or bin/) ---
install_if "$(pick_src capture_sdi_dante.sh || true)" "$BIN_DST/capture_sdi_dante.sh" 0755
install_if "$(pick_src playout_all.sh       || true)" "$BIN_DST/playout_all.sh"       0755
install_if "$(pick_src playlist_prepare.sh  || true)" "$BIN_DST/playlist_prepare.sh"  0755
install_if "$(pick_src jack_start.sh        || true)" "$BIN_DST/jack_start.sh"        0755
install_if "$(pick_src backup_prune.sh      || true)" "$BIN_DST/backup_prune.sh"      0755

# pre-create logs with safe perms so writers don’t fail
for f in status_http_capture.out status_http_capture.err status_http_playout.out status_http_playout.err playout_gst.log; do
  touch "$LOG_DIR/$f"
done
chown "$RUNTIME_OWNER:$RUNTIME_GROUP" "$LOG_DIR"/* || true
chmod 664 "$LOG_DIR"/* || true

echo "[install] done."