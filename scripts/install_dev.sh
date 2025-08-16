#!/bin/bash
set -euo pipefail
REPO_DIR="$(cd "$(dirname "$0")/.."; pwd)"
BIN_DIR="/usr/local/bin"
mkdir -p /opt/backup/{logs,state,latest}

# symlink the shell scripts
for f in capture_sdi_dante.sh playout_all.sh jack_start.sh backup_prune.sh; do
  ln -sfn "$REPO_DIR/bin/$f" "$BIN_DIR/$f"
  chmod 0755 "$REPO_DIR/bin/$f"
done

# status_http wrapper + python
cat >"$BIN_DIR/status_http"<<'WRAP'
#!/bin/sh
exec /usr/bin/env python3 /usr/local/bin/status_http.py "$@"
WRAP
chmod 0755 "$BIN_DIR/status_http"
ln -sfn "$REPO_DIR/server/status_http.py" "$BIN_DIR/status_http.py"
chmod 0755 "$REPO_DIR/server/status_http.py"

echo "✅ Dev install done. Run 'status_http' to start the server."
