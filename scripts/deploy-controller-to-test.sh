#!/usr/bin/env bash
set -euo pipefail

TEST_HOST="${TEST_HOST:-shidur@10.66.1.144}"
TEST_DIR="${TEST_DIR:-/Users/shidur/Projects/sviva-playout}"
GO_BIN="/opt/homebrew/Cellar/go/1.25.4/libexec/bin/go"

echo "[deploy-test] syncing repo to $TEST_HOST:$TEST_DIR"
rsync -avz --delete \
  --exclude '.git' \
  --exclude '.DS_Store' \
  -e ssh \
  ./ "$TEST_HOST:$TEST_DIR/"

echo "[deploy-test] building on $TEST_HOST"
ssh "$TEST_HOST" "
  set -euo pipefail
  export PATH=\"/opt/homebrew/bin:\$(dirname $GO_BIN):\$PATH\"
  cd '$TEST_DIR/controller'
  GO_GST=1 go build -tags gogst -o lesson-controller
  mkdir -p ~/bin
  cp lesson-controller ~/bin/lesson-controller
  echo '[remote] built and copied to ~/bin/lesson-controller'
  shasum -a 256 ~/bin/lesson-controller
"

echo "[deploy-test] done. Restart the controller on $TEST_HOST to pick up changes."
