# Capture Implementation in Go

## Overview

Capture functionality has been **reimplemented in Go** to eliminate the bash script issues that were causing controller crashes. The implementation uses the same proven ffmpeg commands but with proper Go process management.

## What Changed

### Before (Bash Script)
- Controller spawned bash script via `nohup`
- Script managed its own lifecycle
- Stopping capture sent signals to PIDs (caused crashes)
- Process management was unreliable

### After (Go Implementation)
- **Direct ffmpeg management** in Go
- Proper context-based cancellation
- No signal/PID issues
- Clean integration with controller lifecycle
- **Same ffmpeg commands** (proven to work)

## Architecture

```
┌─────────────────────────────────────┐
│  HTTP Endpoints (unchanged)         │
│  /v1/capture/start                  │
│  /v1/capture/stop                   │
│  /v1/capture (status)               │
└──────────────┬──────────────────────┘
               │
               ▼
┌─────────────────────────────────────┐
│  CaptureManager (NEW)               │
│  - Manages ffmpeg process           │
│  - Handles state files              │
│  - Heartbeat updates                │
│  - Context-based cancellation       │
└──────────────┬──────────────────────┘
               │
               ▼
┌─────────────────────────────────────┐
│  ffmpeg (same commands)             │
│  - DeckLink video capture           │
│  - Dante/DVS audio (36 tracks)      │
│  - H.264 + AAC encoding             │
└─────────────────────────────────────┘
```

## Features

### ✅ Same HTTP API
No changes needed in client code:
```bash
# Start capture
curl -X POST http://localhost:8787/v1/capture/start \
  -H 'Content-Type: application/json' \
  -d '{"label":"my-capture","duration":3600,"mode":"dante"}'

# Stop capture
curl -X POST http://localhost:8787/v1/capture/stop

# Get status
curl http://localhost:8787/v1/capture
```

### ✅ Proper Process Management
- Uses Go's `context.Context` for cancellation
- Graceful shutdown with SIGTERM
- Force kill if needed
- No zombie processes

### ✅ State Management
- Writes to `/opt/backup/state/capture.json`
- Heartbeat updates every 5 seconds
- Integrated with `/v1/state` endpoint

### ✅ Same FFmpeg Commands
The exact same ffmpeg commands from the bash script:
- DeckLink input: `UltraStudio Recorder 3G`
- Dante audio: `AUDINATE-DVS-UID` (64 channels)
- SDI audio: embedded stereo split to 36 tracks
- H.264 video encoding
- AAC audio encoding (36 tracks with language metadata)

## Implementation Details

### CaptureManager (`capture_manager.go`)

**Key Methods:**
- `Start(label, duration, mode)` - Starts ffmpeg capture
- `Stop()` - Stops capture gracefully
- `GetState()` - Returns current capture state

**Features:**
- Mutex-protected state
- Context-based cancellation
- Automatic heartbeat updates
- Proper file handle management
- Logging to `/opt/backup/logs/capture_ffmpeg.{out,err}`

### Audio Modes

**Dante Mode (default):**
- 64-channel Dante audio input
- Maps to 36 output tracks
- First track: stereo (heb) at 256k
- Tracks 2-35: mono languages at 128k
- Last track: stereo (src) at 256k

**SDI Mode:**
- Embedded stereo from DeckLink
- Split using `asplit=36` filter
- Same 36-track output structure

## Benefits

### 🎯 Reliability
- No more controller crashes on capture stop
- Proper error handling
- Clean shutdown

### 🔧 Maintainability
- Single codebase (Go)
- No bash/Python/Go coordination
- Easier to debug

### 📊 Monitoring
- Real-time state in `/v1/state`
- Heartbeat tracking
- Process status

### 🚀 Performance
- Direct process management
- No shell overhead
- Efficient resource cleanup

## Testing on Dev Machine

```bash
# 1. Build the controller
cd /Users/alexm/Projects/sviva-playout/controller
go build -tags gogst -o lesson-controller

# 2. Run it
./lesson-controller

# 3. Test capture start
curl -X POST http://localhost:8787/v1/capture/start \
  -H 'Content-Type: application/json' \
  -d '{"label":"test","duration":30}'

# 4. Check status
curl http://localhost:8787/v1/state | jq '.capture'

# 5. Stop capture
curl -X POST http://localhost:8787/v1/capture/stop

# 6. Verify controller is still running
curl http://localhost:8787/v1/state
```

## Migration Notes

### For Production Deployment

**DO NOT deploy yet** - test thoroughly on dev first!

When ready:
1. The HTTP API is unchanged (no client changes needed)
2. Same state files and locations
3. Same ffmpeg commands
4. Just rebuild and restart the controller

### Compatibility

- ✅ Same endpoints
- ✅ Same request/response formats
- ✅ Same state files
- ✅ Same ffmpeg output
- ✅ Works with existing monitoring

## Troubleshooting

### If capture doesn't start:
- Check ffmpeg path: `echo $FFMPEG_PATH`
- Check DeckLink device: `ffmpeg -f decklink -list_devices 1 -i dummy`
- Check Dante device: `ffmpeg -f audiotoolbox -list_devices 1 -i dummy`
- Check logs: `/opt/backup/logs/capture_ffmpeg.err`

### If controller crashes:
- Check controller logs: `/opt/backup/logs/go_controller.err`
- Verify no old bash scripts running: `ps aux | grep capture_sdi_dante`
- Check state file: `cat /opt/backup/state/capture.json`

## Files

- `controller/capture_manager.go` - New capture implementation
- `controller/lesson-controller.go` - Updated to use CaptureManager
- `controller/manager_gogst.go` - Updated getCaptureState()
- `bin/capture_sdi_dante.sh` - Old bash script (can be removed later)

## Next Steps

1. ✅ Test on dev machine (1.144)
2. ⏳ Verify capture quality
3. ⏳ Test long-duration captures
4. ⏳ Test both audio modes (dante/sdi)
5. ⏳ Deploy to production when stable

