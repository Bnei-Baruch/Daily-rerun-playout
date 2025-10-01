#!/usr/bin/env bash
set -euo pipefail

# where to deploy
PROD_HOST="${PROD_HOST:-shidur@10.66.1.82}"
PROD_DIR="${PROD_DIR:-/Users/shidur/Projects/sviva-playout}"

echo "[deploy] syncing repo to $PROD_HOST:$PROD_DIR"
rsync -avz --delete \
  --exclude '.git' \
  --exclude '.DS_Store' \
  -e ssh \
  ./ "$PROD_HOST:$PROD_DIR/"

echo "[deploy] running install on $PROD_HOST (tty for sudo prompt)"
# -tt = force TTY; sudo -v prompts once, then install runs with cached auth
ssh -tt "$PROD_HOST" bash -lc "
  set -euo pipefail
  cd '$PROD_DIR'
  chmod +x scripts/install-prod.sh || true
  echo '[remote] sudo -v (enter the **mac login password** for user shidur if asked)…'
  sudo -v
  echo '[remote] running installer…'
  sudo ./scripts/install-prod.sh
"