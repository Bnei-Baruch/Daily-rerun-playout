#!/bin/bash
set -euo pipefail

# --- where repo files are (run from repo root) ---
REPO_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"

# --- who should own runtime dirs/files ---
RUNUSER="${SUDO_USER:-$USER}"
RUNGROUP="$(id -gn "$RUNUSER")"

echo "[deploy] installing from: $REPO_DIR  as user=$RUNUSER group=$RUNGROUP"

# --- ensure runtime dirs exist ---
sudo mkdir -p /opt/backup/{logs,state,latest}
sudo chown -R "$RUNUSER:$RUNGROUP" /opt/backup
sudo chmod -R u+rwX,g+rwX /opt/backup

# --- install binaries ---
sudo install -m 0755 "$REPO_DIR/bin/status_http.py"   /usr/local/bin/status_http.py
sudo install -m 0755 "$REPO_DIR/bin/status_http"      /usr/local/bin/status_http
sudo install -m 0755 "$REPO_DIR/bin/capture_sdi_dante.sh" /usr/local/bin/capture_sdi_dante.sh
sudo install -m 0755 "$REPO_DIR/bin/playout_all.sh"   /usr/local/bin/playout_all.sh
sudo install -m 0755 "$REPO_DIR/bin/backup_prune.sh"  /usr/local/bin/backup_prune.sh || true
if [ -f "$REPO_DIR/bin/jack_start.sh" ]; then
  sudo install -m 0755 "$REPO_DIR/bin/jack_start.sh"  /usr/local/bin/jack_start.sh
fi

# --- nudge launchd HTTP server if it exists; otherwise print a hint ---
PLIST=/Library/LaunchDaemons/org.bb.status_http.plist
if [ -f "$PLIST" ]; then
  echo "[deploy] restarting status_http launchd service"
  sudo launchctl unload "$PLIST" 2>/dev/null || true
  sudo launchctl load -w "$PLIST"
else
  echo "[deploy] No launchd plist found. If you run in foreground, restart manually:"
  echo "         /usr/local/bin/status_http"
fi

echo "[deploy] done."
