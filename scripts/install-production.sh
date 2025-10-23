#!/bin/bash
# Install Go controller on production machine (coexisting with Python version)
# Run this script ON the production machine

set -e

echo "==================================================================="
echo "   Go Controller - Production Installation"
echo "==================================================================="
echo ""
echo "This will install the Go controller alongside the existing Python system."
echo "Both will be able to run, but only ONE at a time (sharing DeckLink/JACK)."
echo ""
echo "Go Controller: Port 8789"
echo "Python System: Port 8788"
echo ""
read -p "Continue? (y/N) " -n 1 -r
echo
if [[ ! $REPLY =~ ^[Yy]$ ]]; then
    echo "Installation cancelled."
    exit 0
fi

echo ""
echo "Step 1: Checking prerequisites..."
echo "-----------------------------------"

# Check if Go is installed
if ! command -v go &> /dev/null; then
    echo "❌ Go is not installed"
    echo ""
    read -p "Install Go now? (y/N) " -n 1 -r
    echo
    if [[ $REPLY =~ ^[Yy]$ ]]; then
        echo "Installing Go..."
        brew install go
    else
        echo "ERROR: Go is required. Install it with: brew install go"
        exit 1
    fi
else
    echo "✓ Go is installed: $(go version)"
fi

# Check for GStreamer
if ! command -v gst-launch-1.0 &> /dev/null; then
    echo "⚠️  GStreamer not found"
    read -p "Install GStreamer now? (y/N) " -n 1 -r
    echo
    if [[ $REPLY =~ ^[Yy]$ ]]; then
        brew install gstreamer
    else
        echo "WARNING: GStreamer may be required"
    fi
else
    echo "✓ GStreamer is installed"
fi

# Check for JACK
if ! command -v jackd &> /dev/null; then
    echo "⚠️  JACK not found"
    read -p "Install JACK now? (y/N) " -n 1 -r
    echo
    if [[ $REPLY =~ ^[Yy]$ ]]; then
        brew install jack
    else
        echo "WARNING: JACK is required"
    fi
else
    echo "✓ JACK is available"
fi

echo ""
echo "Step 2: Creating backup of current system..."
echo "----------------------------------------------"

BACKUP_DATE=$(date +%Y%m%d_%H%M%S)

if [ -d /opt/backup/state ]; then
    sudo cp -r /opt/backup/state /opt/backup/state.backup.$BACKUP_DATE
    echo "✓ Backed up /opt/backup/state to state.backup.$BACKUP_DATE"
fi

if [ -f /usr/local/bin/lesson_controller ]; then
    sudo cp /usr/local/bin/lesson_controller /usr/local/bin/lesson_controller.backup.$BACKUP_DATE
    echo "✓ Backed up lesson_controller binary"
fi

echo ""
echo "Step 3: Building Go controller..."
echo "-----------------------------------"

cd ~/Projects/sviva-playout/controller

export PKG_CONFIG_PATH=/opt/homebrew/lib/pkgconfig
export CGO_CFLAGS="-I/opt/homebrew/include"
export CGO_LDFLAGS="-L/opt/homebrew/lib"

echo "Building with go-gst bindings..."
go build -tags gogst -o lesson-controller-go 2>&1 | grep -v "format string is not a string literal" || true

if [ ! -f lesson-controller-go ]; then
    echo "❌ Build failed"
    exit 1
fi

echo "✓ Build successful: $(ls -lh lesson-controller-go | awk '{print $5}')"

echo ""
echo "Step 4: Creating wrapper script..."
echo "------------------------------------"

sudo tee /usr/local/bin/lesson-controller-go.sh > /dev/null <<'WRAPPER_EOF'
#!/bin/sh
set -e
LOG_DIR="/opt/backup/logs"
mkdir -p "$LOG_DIR"

# Configuration for Go controller (different port from Python version)
AUDIO_MAP_PATH="${AUDIO_MAP_PATH:-/opt/backup/audio_map.json}"
PNG_PATH="${PNG_PATH:-/opt/backup/default.png}"
DECKLINK_DEVICE="${DECKLINK_DEVICE:-0}"
DECKLINK_MODE="${DECKLINK_MODE:-1080i50}"
PLAYLIST_DIR="${PLAYLIST_DIR:-/opt/backup/playlist}"
FFMPEG_PATH="${FFMPEG_PATH:-/opt/homebrew/bin/ffmpeg}"
PRERUN_VIDEO_PATH="${PRERUN_VIDEO_PATH:-/opt/backup/prerun.mp4}"
PORT="${PORT:-8789}"
GO_GST="${GO_GST:-1}"

DYLD_LIBRARY_PATH="/opt/homebrew/lib${DYLD_LIBRARY_PATH:+:$DYLD_LIBRARY_PATH}"

export AUDIO_MAP_PATH PNG_PATH DECKLINK_DEVICE DECKLINK_MODE PLAYLIST_DIR \
       FFMPEG_PATH PRERUN_VIDEO_PATH PORT GO_GST DYLD_LIBRARY_PATH

exec /usr/local/bin/lesson-controller-go "$@" \
    2>>"$LOG_DIR/go_controller.err" \
    1>>"$LOG_DIR/go_controller.out"
WRAPPER_EOF

sudo chmod +x /usr/local/bin/lesson-controller-go.sh
echo "✓ Wrapper script created"

echo ""
echo "Step 5: Installing binary..."
echo "------------------------------"

sudo cp lesson-controller-go /usr/local/bin/lesson-controller-go
sudo chmod +x /usr/local/bin/lesson-controller-go
echo "✓ Binary installed to /usr/local/bin/lesson-controller-go"

echo ""
echo "Step 6: Creating directories and files..."
echo "-------------------------------------------"

sudo mkdir -p /opt/backup/playlist
sudo mkdir -p /opt/backup/logs
sudo mkdir -p /opt/backup/state

if [ ! -f /opt/backup/audio_map.json ]; then
    if [ -f ~/Projects/sviva-playout/controller/audio_map.json.sample ]; then
        sudo cp ~/Projects/sviva-playout/controller/audio_map.json.sample /opt/backup/audio_map.json
        echo "✓ Created audio_map.json from sample"
    else
        echo "⚠️  audio_map.json.sample not found, skipping"
    fi
else
    echo "✓ audio_map.json already exists"
fi

sudo chown -R $(whoami):staff /opt/backup/playlist 2>/dev/null || true
sudo chown -R $(whoami):staff /opt/backup/logs 2>/dev/null || true

echo "✓ Directories configured"

echo ""
echo "==================================================================="
echo "   Installation Complete!"
echo "==================================================================="
echo ""
echo "Go Controller: Port 8789"
echo "Python System: Port 8788"
echo ""
echo "To start the Go controller:"
echo "  pkill -f lesson_controller  # Stop Python version"
echo "  /usr/local/bin/lesson-controller-go.sh &"
echo ""
echo "To test:"
echo "  curl http://localhost:8789/v1/state"
echo ""
echo "To switch back to Python:"
echo "  pkill -f lesson-controller-go"
echo "  /usr/local/bin/lesson_controller &"
echo ""
echo "Logs:"
echo "  tail -f /opt/backup/logs/go_controller.err"
echo ""
echo "Full documentation:"
echo "  cat ~/Projects/sviva-playout/PRODUCTION_INSTALLATION.md"
echo ""

