# Enhanced Status Page

## Overview

The `/v1/state` endpoint now returns comprehensive status information matching the production Python system format.

## New Status Structure

### Complete Response Format

```json
{
  "playout": {
    "running": true,
    "mode": "playlist",
    "label": "concat-playlist",
    "file": "concat-playlist",
    "pid": 12345,
    "loop": false,
    "overlay": "/Users/shidur/shidur.png",
    "preroll": null,
    "overlay_enabled": false,
    "url": null,
    "playlist": {
      "item_count": 2,
      "current_index": 0,
      "current_file": "/opt/backup/playlist/url_1.mp4"
    },
    "started_at_iso": null,
    "ended_at_iso": null,
    "heartbeat_iso": "2025-10-22 04:35:00"
  },
  "jack": {
    "running": true,
    "pid": 20570,
    "rate": 48000,
    "buffer": 256,
    "nperiods": 3,
    "source": "jack.json"
  },
  "playlist_last_prepared": {
    "preparing": false,
    "label": "playlist",
    "urls": null,
    "url_count": 2,
    "playlist_dir": "/opt/backup/playlist",
    "file_count": 2,
    "prepared_files": 2,
    "prepared_pngs": 2,
    "prepared_at_iso": "2025-10-22 04:19:27",
    "items": [
      {
        "index": 1,
        "url": "/opt/backup/playlist/url_1.mp4",
        "file": "/opt/backup/playlist/url_1.mp4",
        "png": "/opt/backup/playlist/url_1_last.png",
        "original_name": "",
        "final_url": "",
        "duration_hms": "00:25:04",
        "file_exists": true,
        "png_exists": true,
        "file_mtime_iso": "2025-10-22 04:19:25"
      },
      {
        "index": 2,
        "url": "/opt/backup/playlist/url_2.mp4",
        "file": "/opt/backup/playlist/url_2.mp4",
        "png": "/opt/backup/playlist/url_2_last.png",
        "original_name": "",
        "final_url": "",
        "duration_hms": "00:15:30",
        "file_exists": true,
        "png_exists": true,
        "file_mtime_iso": "2025-10-22 04:19:26"
      }
    ],
    "started_at_iso": null,
    "updated_at_iso": "2025-10-22 04:19:27"
  }
}
```

## Fields Explained

### Playout Section

| Field | Type | Description |
|-------|------|-------------|
| `running` | bool | Whether playout is currently active |
| `mode` | string | Current mode: "png", "lesson", or "playlist" |
| `label` | string | Human-readable label for current content |
| `file` | string | Path to current file or description |
| `pid` | int | Process ID of the controller |
| `loop` | bool | Whether current content is looping |
| `overlay` | string | Path to overlay PNG (if any) |
| `overlay_enabled` | bool | Whether overlay is active |
| `playlist` | object | Playlist info (if playing playlist) |
| `heartbeat_iso` | string | Current timestamp in ISO format |

### JACK Section

| Field | Type | Description |
|-------|------|-------------|
| `running` | bool | Whether JACK server is running |
| `pid` | int | JACK server process ID |
| `rate` | int | Sample rate (typically 48000) |
| `buffer` | int | Buffer size (typically 256) |
| `nperiods` | int | Number of periods (typically 3) |
| `source` | string | Where info came from: "jack.json" or "pgrep" |

### Playlist Last Prepared Section

| Field | Type | Description |
|-------|------|-------------|
| `preparing` | bool | Whether playlist is currently being prepared |
| `url_count` | int | Number of URLs in playlist |
| `playlist_dir` | string | Directory where playlist files are stored |
| `prepared_files` | int | Number of successfully downloaded files |
| `prepared_pngs` | int | Number of extracted freeze frame PNGs |
| `prepared_at_iso` | string | When playlist was last prepared |
| `items` | array | Detailed info about each playlist item |

### Playlist Item Fields

| Field | Type | Description |
|-------|------|-------------|
| `index` | int | Item number (1-based) |
| `url` | string | Original URL |
| `file` | string | Local file path |
| `png` | string | Path to extracted freeze frame PNG |
| `duration_hms` | string | Video duration in HH:MM:SS format |
| `file_exists` | bool | Whether the video file exists |
| `png_exists` | bool | Whether the PNG file exists |
| `file_mtime_iso` | string | File modification time |

## Features

### 1. JACK Status Detection

The controller checks JACK status in two ways:
1. **Primary**: Reads `/opt/backup/state/jack.json` (created by `jack_start.sh`)
2. **Fallback**: Uses `pgrep -x jackd` to detect running process

### 2. Video Duration

Uses `ffprobe` to get video duration in HH:MM:SS format.

Set `FFPROBE_PATH` environment variable if `ffprobe` is not in PATH:
```bash
export FFPROBE_PATH=/opt/homebrew/bin/ffprobe
```

### 3. Playlist Persistence

Playlist information persists across controller restarts via `/opt/backup/playlist/playlist.json`.

### 4. Real-time Heartbeat

The `heartbeat_iso` field updates with each status request, showing the controller is alive.

## Installation on Dev Machine

```bash
# On MAC-4K (10.66.1.144):
/tmp/test-enhanced-status.sh
```

This will:
1. Stop the current controller
2. Install the new binary with enhanced status
3. Start the controller
4. Display the new status format

## Testing

### Test PNG Mode
```bash
curl -X POST http://127.0.0.1:8788/source/png
curl -s http://127.0.0.1:8788/v1/state | jq '.playout.mode'
# Should return: "png"
```

### Test Lesson Mode
```bash
curl -X POST http://127.0.0.1:8788/source/lesson \
  -d '{"path":"/opt/backup/lesson-2.mp4","loop":true}'
curl -s http://127.0.0.1:8788/v1/state | jq '.playout.mode'
# Should return: "lesson"
```

### Test Playlist Mode
```bash
curl -X POST http://127.0.0.1:8788/playlist/start
curl -s http://127.0.0.1:8788/v1/state | jq '.playout.mode'
# Should return: "playlist"
```

### Check JACK Status
```bash
curl -s http://127.0.0.1:8788/v1/state | jq '.jack'
```

### Check Playlist Details
```bash
curl -s http://127.0.0.1:8788/v1/state | jq '.playlist_last_prepared'
```

## Compatibility

The new status format is compatible with monitoring tools expecting the Python system format, making it easy to:
- Build dashboards
- Monitor playout status
- Track JACK health
- Verify playlist preparation
- Compare with production system

## Environment Variables

| Variable | Default | Description |
|----------|---------|-------------|
| `FFPROBE_PATH` | `ffprobe` | Path to ffprobe binary for duration detection |
| `PNG_PATH` | - | Path to PNG overlay file |

## Future Enhancements

- [ ] Track current playlist item index during playback
- [ ] Add `started_at_iso` and `ended_at_iso` timestamps
- [ ] Store original URLs in playlist metadata
- [ ] Add capture section (for recording)
- [ ] Add more detailed error states


