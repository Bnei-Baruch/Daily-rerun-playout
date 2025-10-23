# Production Installation Guide - Go Controller

## Overview

This guide will install the Go controller **alongside** the existing Python/Bash system on the production machine. Both systems will coexist and you can switch between them.

## Architecture

### Current Python/Bash System
- **Port**: 8788 (default)
- **Binary**: `/usr/local/bin/lesson_controller` (Python wrapper)
- **Status**: `/opt/backup/state/playout.json`
- **Logs**: Various locations

### New Go Controller
- **Port**: 8789 (different from Python)
- **Binary**: `/usr/local/bin/lesson-controller-go`
- **Wrapper**: `/usr/local/bin/lesson-controller-go.sh`
- **Status**: Available at `http://localhost:8789/v1/state`
- **Logs**: `/opt/backup/logs/go_controller.err` and `.out`

Both systems will:
- Share the same DeckLink output
- Share the same JACK audio server
- Use the same `/opt/backup/` directory structure
- **Cannot run simultaneously** (only one can control DeckLink/JACK at a time)

---

## Pre-Installation Checklist

### 1. Verify Production Machine Details

**Get the hostname and IP:**
```bash
ssh user@production-machine "hostname && ifconfig | grep 'inet '"
```

**Check existing services:**
```bash
ssh user@production-machine "ps aux | grep -E 'lesson_controller|jackd|python'"
```

### 2. Backup Current Configuration

```bash
# On production machine
sudo cp -r /opt/backup/state /opt/backup/state.backup.$(date +%Y%m%d)
sudo cp /usr/local/bin/lesson_controller /usr/local/bin/lesson_controller.backup
```

### 3. Check Dependencies

```bash
# On production machine
which jackd
which gst-launch-1.0
ls /opt/homebrew/lib/libgstreamer-1.0.0.dylib
```

If any are missing, install them:
```bash
brew install jack gstreamer
```

---

## Installation Steps

### Step 1: Install Go (if not already installed)

```bash
# On production machine
ssh user@production-machine

# Check if Go is installed
if ! command -v go &> /dev/null; then
    echo "Installing Go..."
    brew install go
else
    echo "Go is already installed: $(go version)"
fi
```

### Step 2: Clone/Copy the Repository

**Option A: If using Git**
```bash
# On production machine
cd ~
git clone <repository-url> Projects/sviva-playout
cd Projects/sviva-playout
git checkout <branch-name>  # e.g., Gst-with-go
```

**Option B: Copy from dev machine**
```bash
# From your local machine or dev machine
rsync -av /Users/shidur/Projects/sviva-playout/ \
    user@production-machine:~/Projects/sviva-playout/ \
    --exclude='.git' \
    --exclude='controller/lesson-controller'
```

### Step 3: Build the Go Controller

```bash
# On production machine
cd ~/Projects/sviva-playout/controller

# Set build environment
export PKG_CONFIG_PATH=/opt/homebrew/lib/pkgconfig
export CGO_CFLAGS="-I/opt/homebrew/include"
export CGO_LDFLAGS="-L/opt/homebrew/lib"

# Build
go build -tags gogst -o lesson-controller-go

# Verify the binary was created
ls -lh lesson-controller-go
```

### Step 4: Create the Wrapper Script

```bash
# On production machine
sudo tee /usr/local/bin/lesson-controller-go.sh > /dev/null <<'EOF'
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
PORT="${PORT:-8789}"  # Different port from Python version (8788)
GO_GST="${GO_GST:-1}"

# macOS dynamic library path for JACK/GStreamer
DYLD_LIBRARY_PATH="/opt/homebrew/lib${DYLD_LIBRARY_PATH:+:$DYLD_LIBRARY_PATH}"

# Export all configuration
export AUDIO_MAP_PATH PNG_PATH DECKLINK_DEVICE DECKLINK_MODE PLAYLIST_DIR \
       FFMPEG_PATH PRERUN_VIDEO_PATH PORT GO_GST DYLD_LIBRARY_PATH

exec /usr/local/bin/lesson-controller-go "$@" \
    2>>"$LOG_DIR/go_controller.err" \
    1>>"$LOG_DIR/go_controller.out"
EOF

# Make it executable
sudo chmod +x /usr/local/bin/lesson-controller-go.sh
```

### Step 5: Install the Binary

```bash
# On production machine
cd ~/Projects/sviva-playout/controller
sudo cp lesson-controller-go /usr/local/bin/lesson-controller-go
sudo chmod +x /usr/local/bin/lesson-controller-go
```

### Step 6: Create Required Directories and Files

```bash
# On production machine
sudo mkdir -p /opt/backup/playlist
sudo mkdir -p /opt/backup/logs
sudo mkdir -p /opt/backup/state

# Create audio map if it doesn't exist
if [ ! -f /opt/backup/audio_map.json ]; then
    sudo cp ~/Projects/sviva-playout/controller/audio_map.json.sample /opt/backup/audio_map.json
fi

# Ensure proper permissions
sudo chown -R $(whoami):staff /opt/backup/playlist
sudo chown -R $(whoami):staff /opt/backup/logs
```

---

## Switching Between Systems

### Using Python/Bash System (Port 8788)

```bash
# Stop Go controller if running
pkill -f lesson-controller-go

# Start Python system
/usr/local/bin/lesson_controller &

# Test
curl http://localhost:8788/v1/state
```

### Using Go Controller (Port 8789)

```bash
# Stop Python system if running
pkill -f lesson_controller

# Ensure JACK is running
if ! pgrep -x jackd > /dev/null; then
    /opt/homebrew/bin/jackd -R -d coreaudio \
        -C AUDINATE-DVS-UID -P AUDINATE-DVS-UID \
        -r 48000 -p 256 -n 3 &
    sleep 2
fi

# Start Go controller
/usr/local/bin/lesson-controller-go.sh &

# Test
curl http://localhost:8789/v1/state
```

---

## Testing the Go Controller

### 1. Check it's Running

```bash
ps aux | grep lesson-controller-go
curl http://localhost:8789/v1/state | jq
```

### 2. Test PNG Output

```bash
curl -X POST http://localhost:8789/source/png
# Check SDI output - should show PNG
```

### 3. Test Lesson Playback

```bash
curl -X POST http://localhost:8789/source/lesson \
  -H "Content-Type: application/json" \
  -d '{"path":"/opt/backup/lesson-1.mp4","loop":false}'
```

### 4. Test Playlist

```bash
# Prepare playlist
curl -X POST http://localhost:8789/playlist/prepare \
  -H "Content-Type: application/json" \
  -d '{"urls": ["https://cdn.kabbalahmedia.info/eYnxRxlE","https://cdn.kabbalahmedia.info/uNwH6KaD"]}'

# Check status
curl http://localhost:8789/v1/state | jq '.playlist_last_prepared'

# Start playlist
curl -X POST http://localhost:8789/playlist/start
```

### 5. Check Logs

```bash
tail -f /opt/backup/logs/go_controller.err
tail -f /opt/backup/logs/go_controller.out
```

---

## Systemd Service (Optional but Recommended)

Create a systemd service for auto-restart on failure:

```bash
# On production machine (if using Linux)
sudo tee /etc/systemd/system/lesson-controller-go.service > /dev/null <<'EOF'
[Unit]
Description=Lesson Controller (Go)
After=network.target sound.target

[Service]
Type=simple
User=<your-user>
WorkingDirectory=/opt/backup
ExecStart=/usr/local/bin/lesson-controller-go.sh
Restart=always
RestartSec=5
StandardOutput=append:/opt/backup/logs/go_controller.out
StandardError=append:/opt/backup/logs/go_controller.err

[Install]
WantedBy=multi-user.target
EOF

sudo systemctl daemon-reload
sudo systemctl enable lesson-controller-go
sudo systemctl start lesson-controller-go
sudo systemctl status lesson-controller-go
```

---

## Monitoring and Maintenance

### Check Status

```bash
# Go controller
curl -s http://localhost:8789/v1/state | jq

# Python system
curl -s http://localhost:8788/v1/state | jq  # if it has a status endpoint
# or
cat /opt/backup/state/playout.json | jq
```

### Check Logs

```bash
# Go controller
tail -f /opt/backup/logs/go_controller.err

# Python system
tail -f /opt/backup/logs/lesson_controller.err  # or wherever Python logs are
```

### Check Resource Usage

```bash
# CPU/Memory
ps aux | grep -E 'lesson-controller|lesson_controller'

# JACK clients
jack_lsp -c 2>/dev/null | grep gst-
```

### Restart After JACK Issues

If you see JACK connection errors:

```bash
# Stop both controllers
pkill -f lesson-controller-go
pkill -f lesson_controller

# Restart JACK
pkill -9 jackd
/opt/homebrew/bin/jackd -R -d coreaudio \
    -C AUDINATE-DVS-UID -P AUDINATE-DVS-UID \
    -r 48000 -p 256 -n 3 &

sleep 2

# Start the controller you want to use
/usr/local/bin/lesson-controller-go.sh &
```

---

## Rollback to Python/Bash System

If there are issues with the Go controller:

```bash
# Stop Go controller
pkill -f lesson-controller-go
sudo systemctl stop lesson-controller-go  # if using systemd

# Start Python system
/usr/local/bin/lesson_controller &

# Verify
curl http://localhost:8788/v1/state
```

The Python system is completely untouched and will work exactly as before.

---

## Differences Between Python and Go Versions

| Feature | Python/Bash | Go Controller |
|---------|-------------|---------------|
| **API Port** | 8788 | 8789 |
| **Status Endpoint** | `/v1/state` or file | `/v1/state` (JSON) |
| **Playlist Prepare** | Immediate | Downloads + prepares PNGs |
| **Startup Time** | Quick | 10-60 sec (preroll) |
| **JACK Management** | Manual | Auto-cleanup on failure |
| **Freeze Frames** | ? | 5-second PNGs after each item |
| **Prerun Video** | ? | 30-second video before each item |
| **Original Filename** | No | Yes (captured from CDN) |
| **Error Detection** | Basic | JACK-specific detection |

---

## Troubleshooting

### "permission denied" errors

```bash
sudo chown -R $(whoami):staff /opt/backup/
chmod +x /usr/local/bin/lesson-controller-go
chmod +x /usr/local/bin/lesson-controller-go.sh
```

### "Could not open resource for writing" (JACK)

Restart JACK as shown in the "Restart After JACK Issues" section above.

### Build fails on production

Make sure all dependencies are installed:
```bash
brew install go gstreamer jack pkg-config
```

### Controller crashes immediately

Check logs and environment:
```bash
tail -50 /opt/backup/logs/go_controller.err
env | grep -E 'PNG_PATH|AUDIO_MAP_PATH|DYLD_LIBRARY_PATH'
```

---

## Support

- **Logs**: `/opt/backup/logs/go_controller.err`
- **Status**: `curl http://localhost:8789/v1/state`
- **JACK Status**: `jack_lsp -c`
- **Process**: `ps aux | grep lesson-controller-go`

---

## Summary Checklist

- [ ] Go installed on production
- [ ] Repository cloned/copied to production
- [ ] Controller built successfully
- [ ] Wrapper script created and executable
- [ ] Binary installed to `/usr/local/bin/`
- [ ] Required directories created
- [ ] JACK is running
- [ ] Go controller starts on port 8789
- [ ] PNG output tested
- [ ] Lesson playback tested
- [ ] Playlist tested
- [ ] Can switch back to Python system
- [ ] Logs are being written
- [ ] systemd service created (optional)

Once all items are checked, the Go controller is ready for production use alongside the Python system! 🎉

