#!/bin/zsh
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
ROOT_DIR="$(cd "$SCRIPT_DIR/.." && pwd)"

CONTROLLER_DIR="$ROOT_DIR/controller"

if ! command -v go >/dev/null 2>&1; then
  echo "go toolchain not found in PATH" >&2
  exit 1
fi

cd "$CONTROLLER_DIR"
if [ ! -f go.sum ]; then
  echo "Initializing Go module dependencies..."
  go mod tidy
fi
echo "Building lesson-controller (entire package)..."
if [ "${GO_GST:-0}" = "1" ]; then
  echo "Using go-gst build tag"
  go build -tags gogst -o lesson-controller
else
  go build -o lesson-controller
fi

echo "Starting controller..."
exec ./lesson-controller


