# Playlist API Documentation

## Overview

The Go controller supports playlist preparation and playback. The preparation process is asynchronous and downloads files, extracts last frames as PNGs, and prepares metadata. Playback plays files sequentially with optional preroll videos and PNG transitions.

## Endpoints

### 1. Prepare Playlist

**POST** `/playlist/prepare`

Asynchronously downloads and prepares playlist files.

**Request Body:**
```json
{
  "urls": [
    "https://example.com/video1.mp4",
    "https://example.com/video2.mp4"
  ],
  "label": "my-playlist"
}
```

**Response (202 Accepted):**
```json
{
  "status": "preparing",
  "count": 2,
  "label": "my-playlist",
  "message": "playlist preparation started in background"
}
```

**Process:**
1. Cleans up previous playlist files in `PLAYLIST_DIR` (default: `/opt/backup/playlist`)
2. Downloads each URL to `url_1.mp4`, `url_2.mp4`, etc.
3. Extracts last frame of each video to `last_frame-1.png`, `last_frame-2.png`, etc.
4. Extracts metadata (duration, original filename, final URL)
5. Updates preparation state accessible via `/v1/state`

### 2. Start Playlist Playback

**POST** `/playlist/start`

Starts playing the prepared playlist files.

**Response (200 OK):**
```json
{
  "ok": true
}
```

**Playback Structure:**

For each file in the playlist:
1. **Preroll video** (if `PREROLL` env var is set, e.g., `/opt/backup/preroll.mp4`)
2. **Main video file** (the downloaded content)
3. **PNG transition** (5 seconds of the last frame with silence audio)

Then proceeds to the next file.

After all files finish, automatically switches back to PNG mode.

### 3. Check State

**GET** `/v1/state`

Returns current controller state including playlist preparation status.

**Response:**
```json
{
  "playout": {
    "running": true,
    "mode": "playlist",
    "pid": 12345,
    "heartbeat_iso": "2025-11-25 12:00:00"
  },
  "playlist_last_prepared": {
    "preparing": false,
    "label": "my-playlist",
    "url_count": 2,
    "playlist_dir": "/opt/backup/playlist",
    "file_count": 2,
    "prepared_files": 2,
    "prepared_pngs": 2,
    "prepared_at_iso": "2025-11-25 11:55:00",
    "items": [
      {
        "index": 1,
        "url": "https://example.com/video1.mp4",
        "file": "/opt/backup/playlist/url_1.mp4",
        "png": "/opt/backup/playlist/last_frame-1.png",
        "original_name": "video1.mp4",
        "duration_hms": "00:15:30",
        "file_exists": true,
        "png_exists": true
      }
    ]
  }
}
```

## Environment Variables

- `PLAYLIST_DIR`: Directory for playlist files (default: `/opt/backup/playlist`)
- `PREROLL`: Path to preroll video file (default: `/opt/backup/preroll.mp4`)
- `DECKLINK_MODE`: DeckLink output mode (default: `1080i50`)

## Example Usage

```bash
# 1. Prepare playlist
curl -X POST http://10.66.1.144:8788/playlist/prepare \
  -H "Content-Type: application/json" \
  -d '{
    "urls": [
      "https://cdn.kabbalahmedia.info/WWR2RnD5",
      "https://cdn.kab.info/get/T9rp0I8W.mp4?audio=heb&video=hd"
    ],
    "label": "daily-lessons"
  }'

# 2. Check preparation status
curl http://localhost:8788/v1/state | jq '.playlist_last_prepared'

# 3. Start playback when ready
curl -X POST http://localhost:8788/playlist/start

# 4. Switch back to PNG anytime
curl -X POST http://localhost:8788/source/png
```

## Technical Details

### Pipeline Architecture

The playlist uses a GStreamer `concat` element to chain all segments:

```
concat (video) -> videoconvert -> interlace -> decklinkvideosink
concat (audio) -> tee -> 36 JACK audio sinks (all languages)
```

Each segment (preroll, file, PNG) is decoded and normalized to:
- Video: UYVY 1920x1080@25fps progressive (interlaced before DeckLink)
- Audio: F32LE 48kHz stereo (distributed to all JACK ports)

### File Structure

```
/opt/backup/playlist/
├── url_1.mp4              # Downloaded file 1
├── url_2.mp4              # Downloaded file 2
├── last_frame-1.png       # Last frame of file 1
└── last_frame-2.png       # Last frame of file 2
```

### Async Preparation

Preparation runs in a background goroutine and does not block the HTTP response. The controller can continue playing other sources (PNG, lessons, HLS) while preparation is in progress.

### Error Handling

- If a download fails, that URL is skipped and preparation continues
- If PNG extraction fails, playback continues without the PNG transition
- If the playlist pipeline encounters an error, it automatically switches to PNG
- When playlist finishes (EOS), it automatically switches to PNG

