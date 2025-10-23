#!/bin/bash
# Build the controller on the dev machine (MAC-4K / 10.66.1.144)
# This script should be run ON the dev machine, not on alexm's machine

set -e

echo "=== Building lesson-controller on $(hostname) ==="
echo ""

# Check if we're on the dev machine
if [ "$(hostname)" != "MAC-4K.local" ] && [ "$(hostname)" != "MAC-4K" ]; then
    echo "WARNING: This script should be run on MAC-4K (10.66.1.144)"
    echo "Current hostname: $(hostname)"
    echo ""
    read -p "Continue anyway? (y/N) " -n 1 -r
    echo
    if [[ ! $REPLY =~ ^[Yy]$ ]]; then
        exit 1
    fi
fi

# Check if Go is installed
if ! command -v go &> /dev/null; then
    echo "ERROR: Go is not installed"
    echo ""
    echo "Install Go first:"
    echo "  brew install go"
    echo ""
    exit 1
fi

echo "Go version: $(go version)"
echo ""

# Set up build environment
export PKG_CONFIG_PATH=/opt/homebrew/lib/pkgconfig
export CGO_CFLAGS="-I/opt/homebrew/include"
export CGO_LDFLAGS="-L/opt/homebrew/lib"

# Navigate to controller directory
cd ~/Projects/sviva-playout/controller

echo "Building controller with go-gst bindings..."
go build -tags gogst -o lesson-controller 2>&1 | grep -v "format string is not a string literal" || true

if [ ! -f lesson-controller ]; then
    echo ""
    echo "ERROR: Build failed - binary not created"
    exit 1
fi

echo ""
echo "Build successful: $(ls -lh lesson-controller | awk '{print $5}')"
echo ""

# Install the binary
echo "Installing to /usr/local/bin/.lesson-controller.bin"
sudo cp lesson-controller /usr/local/bin/.lesson-controller.bin
sudo chmod +x /usr/local/bin/.lesson-controller.bin

echo ""
echo "=== Installation Complete ==="
echo ""
echo "To start the controller:"
echo "  pkill lesson-controller"
echo "  /usr/local/bin/lesson-controller &"
echo ""
echo "To test playlist timing:"
echo "  curl -X POST http://127.0.0.1:8788/playlist/start"
echo "  tail -f /opt/backup/logs/lesson_controller.err | grep 'pipeline started successfully'"
echo ""

