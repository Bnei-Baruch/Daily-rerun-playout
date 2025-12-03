# HLS Streaming Feature

## Endpoint

### POST `/source/hls/{playlist}` or POST `/source/hls`

Plays an HLS stream via DeckLink SDI output with full audio routing.

## Usage

### Option 1: Playlist ID in URL

```bash
curl -X POST http://localhost:8788/source/hls/01
```

Fetches playlist configuration from `http://10.66.1.105:8081/api/playlist/list` and plays the first item's HLS stream.

### Option 2: Playlist ID in JSON body

```bash
curl -X POST http://localhost:8788/source/hls \
  -H "Content-Type: application/json" \
  -d '{"playlist":"01"}'
```

### Option 3: Direct HLS URL

```bash
curl -X POST http://localhost:8788/source/hls \
  -H "Content-Type: application/json" \
  -d '{"url":"https://example.com/stream/master.m3u8"}'
```

## How It Works

1. **Playlist mode**: Fetches playlist data from API, extracts `hls_path` and `cmd_exec`
2. **Direct URL mode**: Uses the provided URL directly
3. Builds GStreamer HLS pipeline using the `cmd_exec` command if available
4. Outputs video to DeckLink SDI (1080i50)
5. Routes audio to JACK/Dante (35 channels)

## API Response Structure

The `/api/playlist/list` endpoint returns:

```json
{
  "01": {
    "playlist": [
      {
        "hls_path": "https://src.bbdomain.org/.../master.m3u8",
        "cmd_exec": "gst-launch-1.0 souphttpsrc location=... ! hlsdemux ! ..."
      }
    ]
  }
}
```

## End of Stream Behavior

When the HLS stream ends (EOS), the controller automatically falls back to `lesson-1.mp4` (loop mode).

## Status

Check current playback:

```bash
curl http://localhost:8788/v1/state | jq '.playout'
```

Response when playing HLS:

```json
{
  "running": true,
  "mode": "hls",
  "file": "https://src.bbdomain.org/.../master.m3u8",
  "started_at": "2025-11-27T14:30:00Z"
}
```

## Technical Details

- **Video**: 1080i50 interlaced output via DeckLink
- **Audio**: Multi-track routing to 35 JACK ports (Hebrew stereo + 34 mono languages)
- **Buffering**: 9 seconds for network resilience
- **Fallback**: Uses `curl` if Go HTTP client fails to fetch playlist API
