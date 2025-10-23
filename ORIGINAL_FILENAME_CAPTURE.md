# Original Filename Capture

## Overview

The controller now captures and stores the original filename when downloading playlist URLs.

## How It Works

### 1. Download with Headers

When downloading URLs, curl is used with special flags to capture metadata:

```bash
curl -L -o /path/to/file \
  -w "final_url: %{url_effective}\n" \
  -D /path/to/headers.txt \
  https://cdn.kabbalahmedia.info/eYnxRxlE
```

### 2. Extract Original Filename

The controller extracts the original filename from two sources:

**Priority 1: Content-Disposition Header**
```
Content-Disposition: attachment; filename="heb_o_rav_2025-10-18_program_sichot-al-haderech_bakenes-haba-mitchabrim-leasyria-klalit_hd.mp4"
```

**Priority 2: Final URL Path**
```
https://files.kabbalahmedia.info/get/d2df5d521be8aaa7d4dd8b8b284c56114a904abb/heb_o_rav_2025-10-18_program_sichot-al-haderech_bakenes-haba-mitchabrim-leasyria-klalit_hd.mp4
                                                                                    ↑
                                                                    Extracted filename
```

### 3. Store Metadata

The `PlaylistItem` structure now includes:

```go
type PlaylistItem struct {
    Path         string  // Local file path: /opt/backup/playlist/url_1.mp4
    OriginalURL  string  // Original short URL: https://cdn.kabbalahmedia.info/eYnxRxlE
    OriginalName string  // Original filename: heb_o_rav_2025-10-18_program...mp4
    FinalURL     string  // Final redirect URL: https://files.kabbalahmedia.info/get/...
    Map          map     // Audio mapping (if any)
}
```

## Status Page Output

The `/v1/state` endpoint now shows the original filename:

```json
{
  "playlist_last_prepared": {
    "items": [
      {
        "index": 1,
        "url": "https://cdn.kabbalahmedia.info/eYnxRxlE",
        "file": "/opt/backup/playlist/url_1.mp4",
        "png": "/opt/backup/playlist/url_1_last.png",
        "original_name": "heb_o_rav_2025-10-18_program_sichot-al-haderech_bakenes-haba-mitchabrim-leasyria-klalit_hd.mp4",
        "final_url": "https://files.kabbalahmedia.info/get/d2df5d521be8aaa7d4dd8b8b284c56114a904abb/heb_o_rav_2025-10-18_program_sichot-al-haderech_bakenes-haba-mitchabrim-leasyria-klalit_hd.mp4",
        "duration_hms": "00:25:04",
        "file_exists": true,
        "png_exists": true,
        "file_mtime_iso": "2025-10-22 04:19:25"
      }
    ]
  }
}
```

## Benefits

1. **Traceability**: Know which original file was downloaded
2. **Debugging**: Easily identify content by original filename
3. **Logging**: Better audit trail of what was played
4. **Monitoring**: Track which specific content items were used

## Example Workflow

### Prepare Playlist
```bash
curl -X POST http://127.0.0.1:8788/playlist/prepare \
  -H "Content-Type: application/json" \
  -d '{"urls": ["https://cdn.kabbalahmedia.info/eYnxRxlE"]}'
```

### Check Status
```bash
curl -s http://127.0.0.1:8788/v1/state | jq '.playlist_last_prepared.items[0]'
```

**Output:**
```json
{
  "index": 1,
  "url": "https://cdn.kabbalahmedia.info/eYnxRxlE",
  "file": "/opt/backup/playlist/url_1.mp4",
  "png": "/opt/backup/playlist/url_1_last.png",
  "original_name": "heb_o_rav_2025-10-18_program_sichot-al-haderech_bakenes-haba-mitchabrim-leasyria-klalit_hd.mp4",
  "final_url": "https://files.kabbalahmedia.info/get/d2df5d521be8aaa7d4dd8b8b284c56114a904abb/heb_o_rav_2025-10-18_program_sichot-al-haderech_bakenes-haba-mitchabrim-leasyria-klalit_hd.mp4",
  "duration_hms": "00:25:04",
  "file_exists": true,
  "png_exists": true
}
```

## Logs

During playlist preparation, you'll see:

```
LoadPlaylist: downloading 1/1: https://cdn.kabbalahmedia.info/eYnxRxlE
LoadPlaylist: download successful: /opt/backup/playlist/url_1.mp4
LoadPlaylist: extracted filename from Content-Disposition: heb_o_rav_2025-10-18_program_sichot-al-haderech_bakenes-haba-mitchabrim-leasyria-klalit_hd.mp4
LoadPlaylist: final URL: https://files.kabbalahmedia.info/get/d2df5d521be8aaa7d4dd8b8b284c56114a904abb/heb_o_rav_2025-10-18_program_sichot-al-haderech_bakenes-haba-mitchabrim-leasyria-klalit_hd.mp4
LoadPlaylist: extracted last frame: /opt/backup/playlist/url_1_last.png
```

## Persistence

The original filename and URLs are saved in `/opt/backup/playlist/playlist.json`:

```json
{
  "items": [
    {
      "path": "/opt/backup/playlist/url_1.mp4",
      "map": null,
      "original_url": "https://cdn.kabbalahmedia.info/eYnxRxlE",
      "original_name": "heb_o_rav_2025-10-18_program_sichot-al-haderech_bakenes-haba-mitchabrim-leasyria-klalit_hd.mp4",
      "final_url": "https://files.kabbalahmedia.info/get/d2df5d521be8aaa7d4dd8b8b284c56114a904abb/heb_o_rav_2025-10-18_program_sichot-al-haderech_bakenes-haba-mitchabrim-leasyria-klalit_hd.mp4"
    }
  ]
}
```

This metadata persists across controller restarts!

## Testing on Dev Machine

```bash
# Install the new binary
ssh shidur@10.66.1.144
pkill -9 lesson-controller
sudo cp /tmp/lesson-controller-with-filename /usr/local/bin/.lesson-controller.bin
sudo chmod +x /usr/local/bin/.lesson-controller.bin
/usr/local/bin/lesson-controller &

# Prepare a playlist
curl -X POST http://127.0.0.1:8788/playlist/prepare \
  -H "Content-Type: application/json" \
  -d '{"urls": ["https://cdn.kabbalahmedia.info/eYnxRxlE"]}'

# Check the original filename
curl -s http://127.0.0.1:8788/v1/state | jq '.playlist_last_prepared.items[0].original_name'
```

## Fallback Behavior

If the server doesn't provide a Content-Disposition header:
1. The controller extracts the filename from the final URL path
2. Query parameters are stripped (e.g., `?audio=heb&video=hd`)
3. The filename is logged and stored

This ensures you always get meaningful filename information, even from simple CDN URLs.


