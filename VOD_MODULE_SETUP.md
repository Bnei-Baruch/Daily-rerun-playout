# nginx VOD Module Setup on 10.66.1.76

## Installation Complete ✓

nginx with VOD module has been successfully installed on **10.66.1.76:8080**

### Installation Details

- **nginx version:** 1.24.0
- **VOD module version:** 1.31
- **Service:** nginx-vod (systemd)
- **Port:** 8080
- **Status:** Running

### Directory Structure

```
/usr/local/nginx/          # nginx installation
├── sbin/nginx             # nginx binary
└── conf/nginx.conf        # configuration file

/opt/vod/
├── mappings/              # JSON mapping files
│   └── test-segments.json # Example multi-segment mapping
└── media/                 # Media files (if using local storage)

/var/log/nginx/            # Log files
├── access.log
└── error.log
```

### Service Management

```bash
# Start/Stop/Restart
systemctl start nginx-vod
systemctl stop nginx-vod
systemctl restart nginx-vod
systemctl status nginx-vod

# View logs
tail -f /var/log/nginx/error.log
tail -f /var/log/nginx/access.log
```

### Endpoints

- **Health Check:** `http://10.66.1.76:8080/health`
- **Status:** `http://10.66.1.76:8080/status`
- **HLS Stream:** `http://10.66.1.76:8080/vod/{mapping-id}/master.m3u8`

## JSON Mapping Format

The VOD module uses JSON files to define multi-segment playback:

### Example: Multiple Segments from Same File

File: `/opt/vod/mappings/my-segments.json`

```json
{
  "sequences": [
    {
      "clips": [
        {
          "type": "source",
          "path": "/opt/backup/lesson-1.mp4",
          "in": 100000,
          "out": 200000
        },
        {
          "type": "source",
          "path": "/opt/backup/lesson-1.mp4",
          "in": 300000,
          "out": 400000
        },
        {
          "type": "source",
          "path": "/opt/backup/lesson-1.mp4",
          "in": 500000,
          "out": 600000
        }
      ]
    }
  ]
}
```

**Access the stream:**
```
http://10.66.1.76:8080/vod/my-segments/master.m3u8
```

### Field Descriptions

- `sequences`: Array of sequences (usually just one)
- `clips`: Array of video segments to concatenate
- `type`: Always "source" for file-based clips
- `path`: Absolute path to the video file
- `in`: Start time in milliseconds
- `out`: End time in milliseconds

### How It Works

1. Go controller generates JSON mapping with desired segments
2. JSON is saved to `/opt/vod/mappings/{id}.json`
3. Controller requests HLS URL: `http://10.66.1.76:8080/vod/{id}/master.m3u8`
4. VOD module reads JSON, extracts segments, and serves as continuous HLS stream
5. Go controller plays the HLS stream using existing HLS pipeline

## Testing the Setup

### 1. Test with a Real File

Once you have an MP4 file, create a mapping:

```bash
cat > /opt/vod/mappings/test-real.json << EOF
{
  "sequences": [
    {
      "clips": [
        {
          "type": "source",
          "path": "/path/to/your/video.mp4",
          "in": 0,
          "out": 10000
        }
      ]
    }
  ]
}
EOF
```

Test the HLS stream:
```bash
curl -I http://10.66.1.76:8080/vod/test-real/master.m3u8
```

### 2. Test from Go Controller

```bash
curl -X POST http://10.66.1.144:8788/source/segments \
  -H "Content-Type: application/json" \
  -d '{
    "path": "/opt/backup/lesson-1.mp4",
    "segments": [
      {"start_ms": 100000, "end_ms": 200000},
      {"start_ms": 300000, "end_ms": 400000}
    ],
    "vod_server": "http://10.66.1.76:8080",
    "end_behavior": "loop"
  }'
```

## Configuration Details

### nginx.conf Key Settings

```nginx
# VOD mode - use JSON mappings
vod_mode mapped;
vod_upstream_location /json;

# Cache settings
vod_metadata_cache metadata_cache 512m;
vod_response_cache response_cache 128m;
vod_mapping_cache mapping_cache 64m;

# HLS segment duration (6 seconds)
vod_segment_duration 6000;

# Align segments to keyframes for smooth playback
vod_align_segments_to_key_frames on;
```

### JSON Mapping Location

The internal location `/json/` maps to `/opt/vod/mappings/`:

```nginx
location ~ ^/json/(.+)$ {
    internal;
    alias /opt/vod/mappings/$1;
    default_type application/json;
}
```

When you request `/vod/my-segments/master.m3u8`, the VOD module internally fetches `/json/my-segments.json`.

## Next Steps

1. **Copy media files** to the server (or configure network storage)
2. **Implement Go controller integration** (`/source/segments` endpoint)
3. **Test multi-segment playback** with real lesson files
4. **Configure firewall** if needed to allow port 8080
5. **Set up monitoring** for the VOD service

## Troubleshooting

### Check if nginx is running
```bash
systemctl status nginx-vod
ps aux | grep nginx
```

### Check logs for errors
```bash
tail -50 /var/log/nginx/error.log
```

### Test configuration
```bash
/usr/local/nginx/sbin/nginx -t
```

### Restart after config changes
```bash
systemctl restart nginx-vod
```

### Check if port is listening
```bash
ss -tlnp | grep 8080
```

## Performance Notes

- The VOD module caches metadata and responses for efficiency
- Segments are extracted on-the-fly without creating intermediate files
- HLS segments are aligned to keyframes for smooth playback
- CORS is enabled for web player compatibility

## Security Considerations

- Currently listening on all interfaces (0.0.0.0:8080)
- No authentication configured
- Consider adding IP restrictions or authentication for production
- CORS is wide open (`*`) - restrict if needed


