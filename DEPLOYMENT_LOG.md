# Deployment Log - HLS Streaming Feature

## Deployment Date
**Date**: November 11, 2025  
**Time**: 05:30 (server time)  
**Target**: 10.66.1.144 (shidur@MAC-4K)

## What Was Deployed

### HLS Streaming Support
- **Feature**: Full HLS (HTTP Live Streaming) support in persistent pipeline
- **Implementation**: `manager_gogst.go` with automatic HTTP/HTTPS URL detection
- **Audio**: All 35+ language tracks routed to JACK/Dante
- **Video**: 1080i50 interlaced to DeckLink SDI

### Modified Files
1. **`controller/manager_gogst.go`** - Added HLS detection and pipeline (lines 1125-1229)

### New Files
1. **`scripts/test-hls-dev.sh`** - Test script for HLS streaming
2. **`HLS_STREAMING.md`** - Complete documentation
3. **`HLS_FEATURE_SUMMARY.md`** - Quick reference guide

## Installation Details

### Build Information
- **Build Machine**: alexm's Mac (local build)
- **Build Command**: `GO_GST=1 go build -tags gogst -o lesson-controller`
- **Binary Size**: ~38MB (includes go-gst bindings)
- **Go Version**: 1.x (with CGO enabled)

### Installation Location
```
Installation Type: USER_INSTALL=1 (no sudo required)
Binary: /Users/shidur/bin/.lesson-controller.bin (symlink)
Wrapper: /Users/shidur/bin/lesson-controller
```

### Files Transferred
- Total files: 747
- Transfer size: ~11MB
- Transfer time: ~10 seconds
- Method: rsync over SSH

## Verification

### Controller Startup Test
```bash
$ /Users/shidur/bin/lesson-controller
2025/11/11 05:30:12 capture manager initialized
2025/11/11 05:30:12 loadPlaylistMetadata: loaded 2 items from /Users/shidur/playlist/playlist.json
2025/11/11 05:30:12 NewGoGstManager: loaded saved playlist with 2 items
2025/11/11 05:30:12 NewGoGstManager: starting JACK health monitor...
2025/11/11 05:30:12 JACK monitor: started
```

✅ Controller starts successfully  
✅ go-gst manager loaded  
✅ JACK monitor active  
✅ Playlist loaded (2 items)

## How to Use

### Start the Controller
```bash
ssh shidur@10.66.1.144
/Users/shidur/bin/lesson-controller &
```

Or with custom port:
```bash
PORT=8788 /Users/shidur/bin/lesson-controller &
```

### Test HLS Streaming
```bash
# From any machine that can reach 10.66.1.144
curl -X POST http://10.66.1.144:8788/source/lesson \
  -H "Content-Type: application/json" \
  -d '{"path":"http://127.0.0.1:8081/stream.m3u8"}'
```

### Check Status
```bash
curl http://10.66.1.144:8788/v1/state | jq '.playout'
```

## API Endpoints

The controller listens on **port 8788** (default):

| Endpoint | Method | Purpose |
|----------|--------|---------|
| `/source/png` | POST | Switch to PNG/test pattern |
| `/source/lesson` | POST | Play file or HLS stream |
| `/playlist/prepare` | POST | Prepare playlist from URLs |
| `/playlist/start` | POST | Start playlist playback |
| `/v1/state` | GET | Get system status |
| `/v1/capture/start` | POST | Start capture |
| `/v1/capture/stop` | POST | Stop capture |

## Configuration

### Environment Variables
Set in the wrapper script (`/Users/shidur/bin/lesson-controller`):

```bash
AUDIO_MAP_PATH=/Users/shidur/Projects/sviva-playout/controller/audio_map.json.sample
PNG_PATH=/Users/shidur/shidur.png
DECKLINK_DEVICE=0
DECKLINK_MODE=1080i50
PLAYLIST_DIR=/Users/shidur/playlist
PORT=8788
GO_GST=1
```

### Logs
- **stdout**: `/opt/backup/logs/lesson_controller.out`
- **stderr**: `/opt/backup/logs/lesson_controller.err`

Or run in foreground to see logs directly.

## HLS Pipeline Details

### URL Detection
- URLs starting with `http://` or `https://` → HLS pipeline
- Local file paths → qtdemux pipeline

### HLS Pipeline Chain
```
souphttpsrc (blocksize=32KB)
  ↓
queue2 (buffer: 0.9s-9s, watermark: 10%-80%)
  ↓
hlsdemux (parses m3u8, downloads segments)
  ↓
queue2 (buffer: 0.9s-9s)
  ↓
tsdemux (extracts video/audio from MPEG-TS)
  ↓
35+ audio tracks → AAC decode → JACK/Dante
video track → H.264 decode → 1080i50 → DeckLink
```

### Buffer Configuration
- **Max buffer**: 9 seconds
- **Start playback**: 10% full (~0.9 seconds)
- **Pause buffering**: 80% full (~7.2 seconds)
- **Expected startup**: ~1 second from trigger

## Testing

### Test with Local HLS Server
```bash
# On 10.66.1.144
curl -X POST http://localhost:8788/source/lesson \
  -d '{"path":"http://127.0.0.1:8081/stream.m3u8"}'
```

### Test with Remote HLS Server
```bash
curl -X POST http://10.66.1.144:8788/source/lesson \
  -d '{"path":"https://cdn.example.com/live/stream.m3u8"}'
```

### Test Local File (backward compatibility)
```bash
curl -X POST http://10.66.1.144:8788/source/lesson \
  -d '{"path":"/opt/backup/lesson-1.mp4","loop":true}'
```

## Rollback Procedure

If issues occur, the previous version is still in place at:
```bash
/usr/local/bin/lesson-controller  # System-wide installation (if exists)
```

Or rebuild from the previous commit:
```bash
ssh shidur@10.66.1.144
cd /Users/shidur/Projects/sviva-playout/controller
git checkout HEAD~1
go build -tags gogst -o lesson-controller
```

## Known Limitations

1. **Codec Support**: H.264 video and AAC audio only
2. **Protocol Support**: HLS only (not DASH, RTMP, WebRTC)
3. **Live Streams**: `is-live=false` flag assumes VOD
4. **JACK Required**: Audio routing requires JACK server running

## Monitoring

### Check if Running
```bash
ssh shidur@10.66.1.144 "ps aux | grep lesson-controller"
```

### Check Logs
```bash
ssh shidur@10.66.1.144 "tail -f /opt/backup/logs/lesson_controller.err"
```

### Check JACK Connections
```bash
ssh shidur@10.66.1.144 "jack_lsp -c | grep gst-"
```

## Next Steps

1. ✅ Deploy completed successfully
2. ⏭️ Test with actual HLS streams
3. ⏭️ Monitor performance and logs
4. ⏭️ Verify audio routing to all JACK/Dante channels
5. ⏭️ Test switching between HLS and local files
6. ⏭️ Test looping behavior with HLS

## Support

### Documentation
- **Full Guide**: `HLS_STREAMING.md`
- **Quick Reference**: `HLS_FEATURE_SUMMARY.md`
- **Test Script**: `scripts/test-hls-dev.sh`

### Contact
- **Server**: shidur@10.66.1.144
- **Controller Port**: 8788
- **Logs**: `/opt/backup/logs/lesson_controller.err`

---

**Deployment Status**: ✅ **SUCCESS**  
**Feature Status**: ✅ **READY FOR TESTING**





