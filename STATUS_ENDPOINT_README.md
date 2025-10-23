# Status Endpoint Implementation

## Summary

Successfully added a `/v1/state` HTTP endpoint to the Go lesson-controller that returns the current state of the playout system.

## Changes Made

### 1. Added `GetState()` Method to PipelineManager Interface

**File**: `controller/manager_iface.go`

```go
type StateResponse struct {
    Source         string         `json:"source"`          // "png", "lesson", or "playlist"
    LessonPath     string         `json:"lesson_path"`     // current lesson file path (if source=lesson)
    LessonLoop     bool           `json:"lesson_loop"`     // whether lesson is looping
    PlaylistLoaded bool           `json:"playlist_loaded"` // whether a playlist is loaded
    PlaylistCount  int            `json:"playlist_count"`  // number of items in playlist
    PlaylistItems  []PlaylistItem `json:"playlist_items"`  // playlist items (if loaded)
}
```

### 2. Implemented GetState() in goGstManager

**File**: `controller/manager_gogst.go`

The implementation determines the current source by checking the pipeline name:
- `"lesson-only"` → source is "lesson"
- `"playlist-pipeline"` → source is "playlist"
- Default → source is "png"

### 3. Added HTTP Handler

**File**: `controller/lesson-controller.go`

- Added `handleState()` function
- Registered route: `GET /v1/state`
- Made port configurable via `PORT` environment variable (default: 8788)

### 4. Fixed JACK Audio Sink Properties

**Files**: `controller/manager_gogst.go`

Removed `client-name` property from `jackaudiosink` elements as it's not supported in all GStreamer versions. Now only uses `port-pattern` and `sync` properties.

## Testing on Dev Machine (10.66.1.144)

### Status Endpoint Test Results ✅

The status endpoint is **working perfectly**:

```bash
$ curl http://10.66.1.144:8788/v1/state
{
  "source": "png",
  "lesson_path": "",
  "lesson_loop": false,
  "playlist_loaded": false,
  "playlist_count": 0,
  "playlist_items": null
}
```

### Known Limitation on Dev Machine ⚠️

The dev machine (10.66.1.144) **does not have JACK audio support** configured:
- GStreamer cannot find the `jackaudiosink` element
- This prevents testing lesson/playlist playback
- The status endpoint itself works fine

## Production Machine Requirements

For full testing on the production machine (10.66.1.82), ensure:

1. **JACK server is running** with Dante Virtual Soundcard
2. **GStreamer JACK plugin** is available at `/opt/homebrew/lib/gstreamer-1.0/libgstjack.dylib`
3. **Environment variables** are set (see wrapper script)

## API Documentation

### GET /v1/state

Returns the current state of the controller.

**Response Example (PNG source)**:
```json
{
  "source": "png",
  "lesson_path": "",
  "lesson_loop": false,
  "playlist_loaded": false,
  "playlist_count": 0,
  "playlist_items": null
}
```

**Response Example (Lesson playing)**:
```json
{
  "source": "lesson",
  "lesson_path": "/opt/backup/lesson-2.mp4",
  "lesson_loop": true,
  "playlist_loaded": false,
  "playlist_count": 0,
  "playlist_items": null
}
```

**Response Example (Playlist loaded and playing)**:
```json
{
  "source": "playlist",
  "lesson_path": "",
  "lesson_loop": false,
  "playlist_loaded": true,
  "playlist_count": 3,
  "playlist_items": [
    {"path": "/opt/backup/playlist/url_1.mp4", "map": null},
    {"path": "/opt/backup/playlist/url_2.mp4", "map": null},
    {"path": "/opt/backup/playlist/url_3.mp4", "map": null}
  ]
}
```

## Deployment

### Test Script

Use the provided test script to deploy and test:

```bash
./scripts/test-status-dev.sh
```

This script:
1. Stops any existing controller
2. Starts the new controller with status endpoint
3. Tests the status endpoint
4. Attempts to switch sources and check status

### Manual Deployment

1. Build the controller:
```bash
cd controller
go build -tags gogst -o lesson-controller
```

2. Copy to target machine:
```bash
scp lesson-controller user@host:/tmp/lesson-controller-new
```

3. Run with environment variables:
```bash
export PORT=8788
export GO_GST=1
export DYLD_LIBRARY_PATH=/opt/homebrew/lib:$DYLD_LIBRARY_PATH
export GST_PLUGIN_SYSTEM_PATH=/opt/homebrew/lib/gstreamer-1.0
export GST_PLUGIN_PATH=/opt/homebrew/lib/gstreamer-1.0
# ... other env vars ...
./lesson-controller
```

## Next Steps for Production Testing

1. **Test on production machine (10.66.1.82)** where JACK is properly configured
2. **Run on different port** (e.g., 8789) to avoid interfering with existing system
3. **Compare status** between old Python system (port 8787) and new Go system (port 8789)
4. **Verify all features**:
   - PNG source switching
   - Lesson playback (with loop)
   - Playlist preparation and playback
   - Prerun video insertion
   - Status endpoint accuracy

## Compatibility with Python System

The status endpoint provides similar information to the Python system's `/v1/state` endpoint, making it easy to:
- Monitor the controller state
- Build dashboards
- Integrate with existing monitoring tools
- Compare behavior between old and new systems


