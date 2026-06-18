# SVIVA Playt System

A professional broadcast playout system built with GStreamer, JACK Audio, and DeckLink SDI output. Supports local files, web streams, playlists, and image-only output with 1080i50 interlaced video and multi-channel audio routing.

## Features

- **Multi-format Playback**: MP4, H.264, AAC, web streams
- **Professional Output**: 1080i50 interlaced to DeckLink SDI
- **Multi-channel Audio**: 36-channel JACK audio routing
- **Playlist Management**: Prepare, play, and test web-based playlists
- **Image Output**: Static PNG display with endless loop capability
- **HTTP API**: RESTful endpoints for remote control
- **State Management**: Persistent playout state and logging

## System Requirements

- **OS**: macOS 10.15+ or Linux
- **Hardware**: Blackmagic Design DeckLink SDI card
- **Audio**: JACK Audio Connection Kit
- **Dependencies**: GStreamer 1.0+, FFmpeg, Python 3.7+

## Quick Start

### 1. Clone the Repository

```bash
git clone https://github.com/yourusername/sviva-playout.git
cd sviva-playout
```

### 2. Environment Configuration

Copy the example environment file and customize it for your setup:

```bash
cp env.example .env
```

Edit `.env` with your specific paths and settings:

```bash
# Production server connection details
PROD_HOST=user@your-server-ip
PROD_DIR=/path/to/your/production/directory

# System paths and directories
STATE_DIR=/opt/backup/state
LOG_DIR=/opt/backup/logs
BB_STATUS_FILE=/opt/backup/status.json

# Default overlay image path
OVERLAY_PNG=/path/to/your/overlay.png

# GStreamer and FFmpeg paths
GST_PATH=/usr/local/bin/gst-launch-1.0
FFMPEG_PATH=/usr/local/bin/ffmpeg

# Optional security token
STATUS_HTTP_TOKEN=your-secret-token-here
```

### 3. Install Dependencies

```bash
# macOS (using Homebrew)
brew install gstreamer gst-plugins-base gst-plugins-good gst-plugins-bad gst-libav
brew install ffmpeg jack

# Linux (Ubuntu/Debian)
sudo apt-get install gstreamer1.0-tools gstreamer1.0-plugins-base gstreamer1.0-plugins-good
sudo apt-get install gstreamer1.0-plugins-bad gstreamer1.0-libav ffmpeg jackd
```

### 4. Start the System

```bash
# Start JACK audio
./bin/jack_start.sh

# Start the HTTP server
./server/status_http.py

# In another terminal, start playout
./bin/playout_all.sh lesson-1
```

## Usage Examples

### Local File Playback

```bash
# Play a local MP4 file
./bin/playout_all.sh /path/to/video.mp4

# Play with loop
./bin/playout_all.sh --loop lesson-1

# Play with overlay
./bin/playout_all.sh --overlay /path/to/overlay.png lesson-1
```

### Web Stream Playback

```bash
# Play web stream
./bin/playout_all.sh --url "https://example.com/stream.m3u8" --label "live-stream"

# Play with preroll
./bin/playout_all.sh --url "https://example.com/stream.m3u8" --preroll /path/to/preroll.mp4
```

### Playlist Management

```bash
# Prepare playlist from web URLs
./bin/playout_all.sh --playlist "prepare:https://url1.mp4,https://url2.mp4" --label "my-playlist"

# Play prepared playlist
./bin/playout_all.sh --playlist "play" --label "my-playlist"

# Test playlist (5 seconds per file)
./bin/playout_all.sh --playlist "test" --label "test-playlist"
```

### Image Output

```bash
# Display PNG image endlessly
./bin/playout_all.sh --image /path/to/image.png --label "image-display"
```

### HTTP API Control

```bash
# Start playout via API
curl -X POST http://127.0.0.1:8787/v1/playout/start \
  -H 'Content-Type: application/json' \
  -d '{"label": "lesson-1", "loop": true}'

# Start image output
curl -X POST http://127.0.0.1:8787/v1/image/start \
  -H 'Content-Type: application/json' \
  -d '{"image_path": "/path/to/image.png", "label": "my-image"}'

# Check status
curl http://127.0.0.1:8787/v1/state

# Stop playout
curl -X POST http://127.0.0.1:8787/v1/playout/stop \
  -H 'Content-Type: application/json' \
  -d '{}'
```

## API Endpoints

| Endpoint | Method | Description |
|----------|--------|-------------|
| `/v1/state` | GET | Get system status |
| `/v1/playout/start` | POST | Start playout |
| `/v1/playout/stop` | POST | Stop playout |
| `/v1/playlist/prepare` | POST | Prepare playlist from URLs |
| `/v1/playlist/play` | POST | Play prepared playlist |
| `/v1/playlist/test` | POST | Test playlist (5s per file) |
| `/v1/image/start` | POST | Start image output |
| `/v1/image/stop` | POST | Stop image output |
| `/healthz` | GET | Health check |

## Configuration

The system uses environment variables for configuration. Key settings include:

- **`STATE_DIR`**: Directory for state files (default: `/opt/backup/state`)
- **`LOG_DIR`**: Directory for log files (default: `/opt/backup/logs`)
- **`OVERLAY_PNG`**: Default overlay image path
- **`GST_PATH`**: GStreamer binary path
- **`FFMPEG_PATH`**: FFmpeg binary path
- **`STATUS_HTTP_TOKEN`**: Optional API authentication token

## Development

### Project Structure

```
sviva-playout/
├── bin/                    # Shell scripts
│   ├── playout_all.sh     # Main playout script
│   ├── jack_start.sh      # JACK audio startup
│   └── capture_sdi_dante.sh # SDI capture
├── server/                 # HTTP API server
│   └── status_http.py     # Main server
├── scripts/                # Deployment scripts
├── config.py               # Configuration module
├── env.example             # Environment template
└── README.md               # This file
```

### Adding New Features

1. **Shell Scripts**: Add new functions to `bin/playout_all.sh`
2. **HTTP API**: Add new endpoints to `server/status_http.py`
3. **Configuration**: Add new variables to `config.py` and `env.example`

### Testing

```bash
# Test configuration
python3 config.py

# Test syntax
bash -n bin/playout_all.sh
python3 -m py_compile server/status_http.py

# Test playout
./bin/playout_all.sh --help
```

## Security Considerations

- **Environment Variables**: Never commit `.env` files to version control
- **API Authentication**: Use `STATUS_HTTP_TOKEN` for production deployments
- **File Permissions**: Ensure proper permissions on state and log directories
- **Network Access**: Restrict HTTP server access in production

## Troubleshooting

### Common Issues

1. **GStreamer not found**: Check `GST_PATH` in environment
2. **DeckLink not working**: Verify DeckLink drivers and mode settings
3. **JACK audio issues**: Check JACK server status and port connections
4. **Permission errors**: Verify directory permissions and ownership

### Logs

- **Playout logs**: `/opt/backup/logs/playout_gst.log`
- **HTTP server logs**: `/opt/backup/logs/status_http_playout.out`
- **System logs**: Check `journalctl` or system logs

## Contributing

1. Fork the repository
2. Create a feature branch
3. Make your changes
4. Test thoroughly
5. Submit a pull request

## License

[Add your license information here]

## Support

For support and questions, please open an issue on GitHub or contact [your contact information].
