# nginx VOD Module Setup Status

## Current Status on 10.66.1.76

✅ **Installed:** nginx 1.24.0 with VOD module 1.31
✅ **Running:** nginx on port 8080
✅ **Configuration:** Based on working example from production
⚠️ **Testing:** JSON format needs final validation

## What Works

- nginx VOD module compiled and running
- Health endpoint: `http://10.66.1.76:8080/health` ✓
- Remote file proxy configured for https://wfsrv.bbdomain.org/
- Large moov/metadata buffers configured (64MB/256MB)
- Thread pool configured for async file operations

## Configuration

**Location:** `/usr/local/nginx/conf/nginx.conf`

**Key Settings:**
- JSON mappings: `/usr/local/var/vod-maps/`
- URL format: `http://10.66.1.76:8080/hls/{mapping-name}.json/master.m3u8`
- Remote files accessed via `/remote/` internal location
- Proxy to: `https://wfsrv.bbdomain.org/`

## JSON Format (Still Testing)

Based on Kaltura nginx-vod-module docs, the correct format for multi-segment playback:

```json
{
  "sequences": [
    {
      "clips": [
        {
          "type": "source",
          "path": "/remote/wfapi/backup/files/sources/2025/11/27/file.mp4",
          "clipFrom": 0,
          "clipTo": 10000
        },
        {
          "type": "source",
          "path": "/remote/wfapi/backup/files/sources/2025/11/27/file.mp4",
          "clipFrom": 30000,
          "clipTo": 40000
        }
      ]
    }
  ]
}
```

**Note:** Times are in milliseconds. `clipFrom`/`clipTo` define the segment range.

## Test File Location

- File: `/usr/local/var/vod-maps/test-segments.json`
- Test URL: `http://10.66.1.76:8080/hls/test-segments.json/master.m3u8`
- Test video: `https://wfsrv.bbdomain.org/wfapi/backup/files/sources/2025/11/27/mlt_s_norav_2025-11-27_lesson_achana_n1_p0.mp4`

## Known Issues & Next Steps

1. **Caching:** VOD module caches mappings - changes require nginx restart or cache disabled
2. **JSON Format:** May need `durations` array in addition to `clipFrom`/`clipTo`
3. **Remote Access:** Verify https://wfsrv.bbdomain.org/ is accessible from 10.66.1.76

## Commands

```bash
# Restart nginx
ssh root@10.66.1.76 'pkill nginx && /usr/local/nginx/sbin/nginx'

# Check logs
ssh root@10.66.1.76 'tail -f /usr/local/var/log/nginx-vod/error.log'

# Test endpoint
curl -v "http://10.66.1.76:8080/hls/test-segments.json/master.m3u8"

# Create new mapping
cat > /usr/local/var/vod-maps/my-mapping.json << EOF
{
  "sequences": [...clips...]
}
EOF
```

## Alternative: GStreamer-based Implementation

If VOD module continues to be problematic, implement segments in Go controller using GStreamer's `concat` element, similar to playlist functionality. This would:

- Use existing working code patterns
- Avoid VOD module complexity
- Still provide continuous multi-segment playback
- Work immediately without external dependencies

Would just need to add:
1. `/source/segments` endpoint
2. `SwitchToSegments()` method in manager_gogst.go
3. Build concat pipeline with time-based seeking per segment

## Documentation

- Kaltura nginx-vod-module: https://github.com/kaltura/nginx-vod-module
- JSON Mapping API: https://github.com/kaltura/nginx-vod-module#mapping-response-format
- clipFrom/clipTo: https://github.com/kaltura/nginx-vod-module#clip-from--to


