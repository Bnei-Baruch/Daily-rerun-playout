#!/bin/bash
# Build and install the controller on the dev machine (10.66.1.144)

set -e

DEV_HOST="shidur@10.66.1.144"
PROJECT_DIR="/Users/shidur/Projects/sviva-playout"

echo "=== Building controller on dev machine ==="

# Sync the source code
echo "Syncing source code..."
rsync -av --exclude='controller/lesson-controller' \
    /Users/alexm/Projects/sviva-playout/controller/ \
    ${DEV_HOST}:${PROJECT_DIR}/controller/

# Build on the remote machine
echo "Building on remote machine..."
ssh ${DEV_HOST} "cd ${PROJECT_DIR}/controller && \
    export PKG_CONFIG_PATH=/opt/homebrew/lib/pkgconfig && \
    export CGO_CFLAGS=\"-I/opt/homebrew/include\" && \
    export CGO_LDFLAGS=\"-L/opt/homebrew/lib\" && \
    go build -tags gogst -o lesson-controller"

# Install the binary
echo "Installing binary..."
ssh ${DEV_HOST} "sudo cp ${PROJECT_DIR}/controller/lesson-controller /usr/local/bin/.lesson-controller.bin && \
    sudo chmod +x /usr/local/bin/.lesson-controller.bin"

echo "=== Build and install complete ==="
echo ""
echo "To start the controller on dev machine:"
echo "  ssh ${DEV_HOST}"
echo "  pkill -9 lesson-controller"
echo "  /usr/local/bin/lesson-controller &"

