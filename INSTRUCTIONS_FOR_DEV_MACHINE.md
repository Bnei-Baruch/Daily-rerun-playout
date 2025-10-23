# Instructions for Testing on Dev Machine (MAC-4K)

## The Problem

The binary I compiled on my Mac won't run on your Mac due to library version differences. You need to build it on your machine.

## Quick Fix - Run These Commands on MAC-4K

```bash
# 1. Install Go (if not already installed)
brew install go

# 2. Go to the project directory
cd ~/Projects/sviva-playout/controller

# 3. Build the controller
export PKG_CONFIG_PATH=/opt/homebrew/lib/pkgconfig
export CGO_CFLAGS="-I/opt/homebrew/include"
export CGO_LDFLAGS="-L/opt/homebrew/lib"
go build -tags gogst -o lesson-controller

# 4. Install the new binary
sudo cp lesson-controller /usr/local/bin/.lesson-controller.bin
sudo chmod +x /usr/local/bin/.lesson-controller.bin

# 5. Restart the controller
pkill lesson-controller
/usr/local/bin/lesson-controller &

# 6. Wait a moment for it to start
sleep 2

# 7. Test the original filename feature
curl -X POST http://127.0.0.1:8788/playlist/prepare \
  -H "Content-Type: application/json" \
  -d '{"urls": ["https://cdn.kabbalahmedia.info/eYnxRxlE"]}'

# 8. Check the status (should show original_name and final_url)
curl -s http://127.0.0.1:8788/v1/state | jq '.playlist_last_prepared.items[0]'
```

## Expected Output

You should see something like:

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

The important new fields are:
- `original_name`: The actual filename from the CDN
- `final_url`: The full URL after redirects

## Troubleshooting

### If Go is not found after installing:
```bash
# Add Homebrew to your PATH
export PATH="/opt/homebrew/bin:$PATH"
```

### If the build fails:
```bash
# Make sure you have the latest code
cd ~/Projects/sviva-playout
git pull

# Try the build again
cd controller
go build -tags gogst -o lesson-controller
```

### If the controller won't start:
```bash
# Check the logs
tail -50 /opt/backup/logs/lesson_controller.err

# Make sure JACK is running
pgrep -x jackd || /opt/homebrew/bin/jackd -R -d coreaudio -C AUDINATE-DVS-UID -P AUDINATE-DVS-UID -r 48000 -p 256 -n 3 &
```

### To restore the old working version:
```bash
sudo cp /usr/local/bin/.lesson-controller.bin.backup /usr/local/bin/.lesson-controller.bin
pkill lesson-controller
/usr/local/bin/lesson-controller &
```

## What Changed

The controller now captures the original filename when downloading playlist items:

1. **During Download**: Uses `curl` to capture HTTP headers and final URL
2. **Extracts Filename**: From `Content-Disposition` header or URL path
3. **Stores Metadata**: In `playlist.json` for persistence
4. **Shows in Status**: Available via `/v1/state` endpoint

This helps with:
- **Traceability**: Know which original file was played
- **Debugging**: Identify content by meaningful names
- **Monitoring**: Better audit trail

## Files Changed

- `controller/manager_gogst.go`: Enhanced `LoadPlaylist()` to capture original filename
- `controller/manager_iface.go`: Added `OriginalName` and `FinalURL` fields to `PlaylistItem`
- `controller/lesson-controller.go`: No changes needed

## Next Steps

Once this works on the dev machine, we can deploy to production using the same process (build on the production machine).

