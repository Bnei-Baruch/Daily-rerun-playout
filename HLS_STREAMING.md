# HLS Streaming Support

## Overview

The Go controller (`manager_gogst.go`) now supports playing HLS (HTTP Live Streaming) streams with full multi-language audio routing to JACK/Dante. This uses the **persistent pipeline** approach with go-gst bindings.

## Features

✅ **Automatic Detection** - HTTP/HTTPS URLs are automatically detected and routed through HLS demuxing  
✅ **Full Audio Routing** - All 35+ language tracks are extracted and routed to JACK/Dante  
✅ **Persistent Pipeline** - Uses go-gst with a single persistent pipeline (no gst-launch spawning)  
✅ **Buffering** - Large buffers (9 seconds) for smooth playback  
✅ **Overlay Support** - PNG overlay works with HLS streams  
✅ **Same API** - Uses the same `/source/lesson` endpoint as local files  

## How It Works

### Pipeline Architecture

**For HLS streams (http:// or https://):**
```
souphttpsrc → queue2 → hlsdemux → queue2 → tsdemux → video/audio tracks
```

**For local files:**
```
filesrc → qtdemux → video/audio tracks
```

Both paths output to:
- **Video**: 1080i50 interlaced to DeckLink SDI
- **Audio**: 35+ tracks to JACK/Dante (Hebrew stereo + 34 mono languages)

### GStreamer Elements

- **souphttpsrc** - HTTP source with custom user-agent and keep-alive
- **hlsdemux** - HLS playlist parser and segment downloader
- **tsdemux** - MPEG-TS demuxer to extract video/audio streams
- **queue2** - Large buffering elements (9 seconds max)
- **h264parse → avdec_h264** - Video decoding
- **aacparse → avdec_aac** - Audio decoding (per track)

## Usage

### HTTP API

The same `/source/lesson` endpoint handles both local files and HLS streams:

```bash
# Play HLS stream
curl -X POST http://localhost:8788/source/lesson \
  -H "Content-Type: application/json" \
  -d '{"path":"http://127.0.0.1:8081/stream.m3u8","loop":false}'

# Play local file (existing functionality)
curl -X POST http://localhost:8788/source/lesson \
  -H "Content-Type: application/json" \
  -d '{"path":"/opt/backup/lesson-1.mp4","loop":false}'
```

### Test Script

```bash
# Test with your HLS URL
./scripts/test-hls-dev.sh http://127.0.0.1:8081/stream.m3u8

# Or with a public HLS stream
./scripts/test-hls-dev.sh https://cdn.example.com/live/stream.m3u8
```

### Check Status

```bash
curl http://localhost:8788/v1/state | jq '.playout'
```

## Audio Track Mapping

All audio tracks from the HLS stream are routed to JACK ports:

| Track | Language | JACK Port(s) | Format |
|-------|----------|--------------|--------|
| 0 | Hebrew (he) | 20 (L), 21 (R) | Stereo |
| 1 | English (en) | 29 | Mono |
| 2 | Russian (ru) | 22 | Mono |
| 3 | Spanish (es) | 30 | Mono |
| 4 | French (fr) | 1 | Mono |
| 5 | Italian (it) | 2 | Mono |
| ... | ... | ... | ... |
| 34 | Azerbaijani (az) | 37 | Mono |

See `controller/audio_map.json.sample` for the complete mapping.

## Configuration

### Buffer Settings

The HLS pipeline uses large buffers for smooth playback:

```
queue2 use-buffering=true 
  max-size-time=9000000000      (9 seconds)
  max-size-bytes=0              (unlimited)
  max-size-buffers=0            (unlimited)
  low-watermark=0.1             (10% to start playback)
  high-watermark=0.8            (80% to pause buffering)
```

### User Agent

The `souphttpsrc` identifies as `sviva-playout` in HTTP requests.

### Environment Variables

- **`OVERLAY_PNG`** - Path to overlay image (works with HLS streams)
- **`OVERLAY_ENABLED`** - Set to `0` to disable overlay (default: `1`)
- **`DECKLINK_DEVICE`** - DeckLink device number (default: `0`)

## Examples

### Example 1: Basic HLS Playback

```bash
curl -X POST http://localhost:8788/source/lesson \
  -H "Content-Type: application/json" \
  -d '{"path":"http://127.0.0.1:8081/stream.m3u8"}'
```

### Example 2: HLS with Looping

```bash
curl -X POST http://localhost:8788/source/lesson \
  -H "Content-Type: application/json" \
  -d '{"path":"http://127.0.0.1:8081/stream.m3u8","loop":true}'
```

### Example 3: Switch Between HLS and Local File

```bash
# Start with HLS stream
curl -X POST http://localhost:8788/source/lesson \
  -d '{"path":"http://127.0.0.1:8081/stream.m3u8"}'

# Switch to local file
curl -X POST http://localhost:8788/source/lesson \
  -d '{"path":"/opt/backup/lesson-1.mp4"}'

# Back to HLS stream
curl -X POST http://localhost:8788/source/lesson \
  -d '{"path":"http://127.0.0.1:8081/stream.m3u8"}'
```

## Technical Details

### Implementation

The HLS detection happens in `buildLessonOnlyPipeline()` in `manager_gogst.go`:

```go
// Detect if path is HLS stream or local file
isHLS := strings.HasPrefix(path, "http://") || strings.HasPrefix(path, "https://")

if isHLS {
    // Build HLS pipeline: souphttpsrc → hlsdemux → tsdemux
} else {
    // Build local file pipeline: filesrc → qtdemux
}
```

### Pipeline Lifecycle

1. **Tear down existing pipeline** - Any running pipeline is stopped
2. **Build new pipeline** - HLS or file pipeline is created
3. **Set to PLAYING** - Pipeline starts (preroll takes 10-60 seconds)
4. **Monitor bus** - Watch for EOS (end of stream) and errors
5. **Handle EOS** - Loop if enabled, or switch to PNG

### Buffering Behavior

- **Low watermark (10%)**: Start playback when buffer reaches 10%
- **High watermark (80%)**: Pause buffering when buffer reaches 80%
- **Max size (9 seconds)**: Maximum buffering before playback must start

This ensures smooth playback even with network jitter.

## Troubleshooting

### Stream Won't Play

1. **Check the URL** - Verify the HLS URL is accessible:
   ```bash
   curl -I http://127.0.0.1:8081/stream.m3u8
   ```

2. **Check logs** - Look for errors in controller logs:
   ```bash
   tail -f /opt/backup/logs/go_controller.err
   ```

3. **Test with gst-launch** - Verify GStreamer can play the stream:
   ```bash
   gst-launch-1.0 souphttpsrc location=http://127.0.0.1:8081/stream.m3u8 \
     ! hlsdemux ! tsdemux ! h264parse ! avdec_h264 ! autovideosink
   ```

### No Audio

1. **Check JACK** - Verify JACK is running and ports are available:
   ```bash
   jack_lsp -c | grep gst-
   ```

2. **Check audio tracks** - Some HLS streams may not have all 35 audio tracks
   - The pipeline will only route tracks that exist in the stream
   - Missing tracks are ignored (no error)

3. **Check audio format** - The pipeline expects AAC audio:
   ```bash
   ffprobe http://127.0.0.1:8081/stream.m3u8
   ```

### Buffering Issues

If playback is choppy:

1. **Increase buffer size** - Edit `manager_gogst.go` and increase `max-size-time`
2. **Check network** - Verify network latency and bandwidth
3. **Check CPU** - Verify CPU usage isn't too high

## Limitations

- **Codec**: Currently only H.264 video and AAC audio are supported
- **Format**: Only HLS (m3u8) streams are supported (not DASH, RTMP, etc.)
- **Live streams**: The `is-live=false` flag assumes VOD; adjust for live streams
- **Looping**: HLS loop behavior may differ from local files depending on stream type

## Future Enhancements

Possible improvements:

- [ ] Support for other streaming protocols (DASH, RTMP, WebRTC)
- [ ] Support for other codecs (VP8/VP9, Opus, etc.)
- [ ] Adaptive bitrate logic
- [ ] Network statistics and monitoring
- [ ] Automatic fallback on network errors
- [ ] HTTPS with certificate validation

## Related Documentation

- **Build Instructions**: `BUILD_INSTRUCTIONS.md`
- **Quick Reference**: `QUICK_REFERENCE.md`
- **Audio Mapping**: `controller/audio_map.json.sample`
- **Port Configuration**: `PORT_CONFIGURATION.md`




