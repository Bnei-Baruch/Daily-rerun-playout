#!/usr/bin/env bash
set -euo pipefail

PROD_HOST="${PROD_HOST:-shidur@10.66.1.82}"
PROD_DIR="${PROD_DIR:-/Users/shidur/Projects/sviva-playout}"

echo "[deploy-controller] building lesson-controller locally (GO_GST=1)"
(cd controller && GO_GST=1 go build -tags gogst -o lesson-controller)

echo "[deploy-controller] syncing repo to $PROD_HOST:$PROD_DIR"
rsync -avz --delete \
  --exclude '.git' \
  --exclude '.DS_Store' \
  -e ssh \
  ./ "$PROD_HOST:$PROD_DIR/"

echo "[deploy-controller] installing controller only on $PROD_HOST (no changes to existing playout)"
ssh -tt "$PROD_HOST" bash -lc "
  set -euo pipefail
  cd '$PROD_DIR'
  chmod +x scripts/install-controller-prod.sh || true
  echo '[remote] running controller installer…'
  GO_GST=1  ./scripts/install-controller-prod.sh
  if [ -f controller/lesson-controller ]; then
    echo '[remote] controller/lesson-controller sha256:'
    shasum -a 256 controller/lesson-controller
  fi
  if [ -f /usr/local/bin/lesson-controller ]; then
    echo '[remote] /usr/local/bin/lesson-controller sha256:'
    shasum -a 256 /usr/local/bin/lesson-controller
  fi
"

#echo "[deploy-controller] done. You can start it with: ssh $PROD_HOST 'sudo -u shidur /usr/local/bin/lesson_controller'"



