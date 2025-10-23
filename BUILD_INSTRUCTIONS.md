# Build Instructions for Dev Machine (MAC-4K)

## ⚠️ Important: Cross-Compilation Doesn't Work

The binary compiled on alexm's Mac **will NOT run** on shidur's Mac, even though both are ARM64 Macs. This is because:

1. **Dynamic Library Paths**: Different homebrew installations
2. **Library Versions**: GStreamer/JACK versions may differ
3. **CGO Dependencies**: The go-gst bindings link against system libraries at compile time

**Solution: Always build ON the target machine**

## Quick Build (On MAC-4K)

```bash
# Run the automated build script
/tmp/build-on-dev.sh
```

That's it! The script will:
- Check if Go is installed
- Build the controller with the correct flags
- Install it to `/usr/local/bin/.lesson-controller.bin`
- Provide instructions for starting it

## Manual Build (On MAC-4K)

If you prefer to build manually:

```bash
# 1. Install Go (if not already installed)
brew install go

# 2. Navigate to the controller directory
cd ~/Projects/sviva-playout/controller

# 3. Set up build environment
export PKG_CONFIG_PATH=/opt/homebrew/lib/pkgconfig
export CGO_CFLAGS="-I/opt/homebrew/include"
export CGO_LDFLAGS="-L/opt/homebrew/lib"

# 4. Build
go build -tags gogst -o lesson-controller

# 5. Install
sudo cp lesson-controller /usr/local/bin/.lesson-controller.bin
sudo chmod +x /usr/local/bin/.lesson-controller.bin

# 6. Restart the controller
pkill lesson-controller
/usr/local/bin/lesson-controller &
```

## Verify It's Running

```bash
# Check if the process is running
ps aux | grep lesson-controller

# Test the API
curl http://127.0.0.1:8788/v1/state

# Check logs
tail -f /opt/backup/logs/lesson_controller.err
```

## Testing the New Features

### 1. Original Filename Capture

```bash
# Prepare a playlist
curl -X POST http://127.0.0.1:8788/playlist/prepare \
  -H "Content-Type: application/json" \
  -d '{"urls": ["https://cdn.kabbalahmedia.info/eYnxRxlE"]}'

# Check the captured filename
curl -s http://127.0.0.1:8788/v1/state | jq '.playlist_last_prepared.items[0].original_name'
```

### 2. Startup Timing Logs

```bash
# Start the playlist
curl -X POST http://127.0.0.1:8788/playlist/start

# Watch the timing logs
tail -f /opt/backup/logs/lesson_controller.err | grep "pipeline started successfully"
```

You should see output like:
```
StartPlaylist: starting pipeline (preroll may take 10-60 seconds)...
StartPlaylist: pipeline started successfully in 45.3 seconds
```

## Troubleshooting

### "go: command not found"

```bash
brew install go
```

### "permission denied" when installing

Make sure you use `sudo`:
```bash
sudo cp lesson-controller /usr/local/bin/.lesson-controller.bin
```

### Controller crashes immediately after starting

Check the logs:
```bash
tail -50 /opt/backup/logs/lesson_controller.err
```

Common issues:
- JACK not running: Start JACK first
- Missing environment variables: Use the wrapper script `/usr/local/bin/lesson-controller`
- Permissions: Binary needs execute permission

### Build fails with "package not found"

Update dependencies:
```bash
cd ~/Projects/sviva-playout/controller
go mod tidy
go build -tags gogst -o lesson-controller
```

## Syncing Latest Code from alexm's Machine

If there are new code changes:

```bash
# On alexm's machine (or use git)
rsync -av /Users/alexm/Projects/sviva-playout/controller/ \
    shidur@10.66.1.144:~/Projects/sviva-playout/controller/
```

Then build on MAC-4K as described above.

## Using Git (Recommended)

If the code is in git:

```bash
# On MAC-4K
cd ~/Projects/sviva-playout
git pull
cd controller
go build -tags gogst -o lesson-controller
sudo cp lesson-controller /usr/local/bin/.lesson-controller.bin
pkill lesson-controller && /usr/local/bin/lesson-controller &
```

## Summary

**Always remember:**
1. 🚫 Don't use binaries compiled on alexm's Mac
2. ✅ Always build on the target machine (MAC-4K)
3. 📝 Use the wrapper script to start: `/usr/local/bin/lesson-controller`
4. 🔍 Check logs: `/opt/backup/logs/lesson_controller.err`

This ensures the binary is properly linked against the local system libraries.

