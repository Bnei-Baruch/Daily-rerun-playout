# Lesson Repeat Endpoint Examples

This document provides examples of how to use the new lesson repeat endpoints with continuous GStreamer pipeline to avoid signal drops.

## Endpoints

- `POST /v1/lesson/repeat` - Start playing a lesson multiple times with black frames between plays, then switch to shidur.png
- `POST /v1/lesson/stop` - Stop lesson and switch to shidur.png for continuous display
- `POST /v1/lesson/start` - Switch from shidur.png to lesson playback

## Examples

### 1. Default Lesson Repeat (lesson-1, 10 times, 2s black frames)

```bash
curl -X POST http://10.66.1.82:8787/v1/lesson/repeat \
  -H "Content-Type: application/json" \
  -d '{}'
```

### 2. Custom Lesson with Different Parameters

```bash
curl -X POST http://10.66.1.82:8787/v1/lesson/repeat \
  -H "Content-Type: application/json" \
  -d '{
    "lesson": "lesson-2",
    "repeat_count": 5,
    "black_duration": 3,
    "label": "my-custom-repeat"
  }'
```

### 3. Short Repeat with Quick Black Frames

```bash
curl -X POST http://10.66.1.82:8787/v1/lesson/repeat \
  -H "Content-Type: application/json" \
  -d '{
    "lesson": "lesson-3",
    "repeat_count": 3,
    "black_duration": 1,
    "label": "quick-repeat"
  }'
```

### 4. Stop Lesson Repeat (Switch to Shidur.png)

```bash
curl -X POST http://10.66.1.82:8787/v1/lesson/stop \
  -H "Content-Type: application/json" \
  -d '{
    "shidur_png": "/Users/shidur/shidur.png"
  }'
```

### 5. Start Lesson from Shidur.png

```bash
curl -X POST http://10.66.1.82:8787/v1/lesson/start \
  -H "Content-Type: application/json" \
  -d '{
    "lesson": "lesson-1",
    "label": "my-lesson"
  }'
```

## Parameters

### `/v1/lesson/repeat` Parameters

| Parameter | Type | Default | Description |
|-----------|------|---------|-------------|
| `lesson` | string | "lesson-1" | Lesson to repeat (lesson-1, lesson-2, lesson-3, or lesson-4) |
| `repeat_count` | integer | 10 | Number of times to repeat the lesson |
| `black_duration` | integer | 2 | Duration of black frame between plays (seconds) |
| `label` | string | "repeat-{lesson}" | Label for the playback session |
| `shidur_png` | string | "/Users/shidur/shidur.png" | Path to shidur.png for continuous display |

### `/v1/lesson/stop` Parameters

| Parameter | Type | Default | Description |
|-----------|------|---------|-------------|
| `shidur_png` | string | "/Users/shidur/shidur.png" | Path to shidur.png for continuous display |

### `/v1/lesson/start` Parameters

| Parameter | Type | Default | Description |
|-----------|------|---------|-------------|
| `lesson` | string | "lesson-1" | Lesson to play (lesson-1, lesson-2, lesson-3, or lesson-4) |
| `label` | string | "lesson-{lesson}" | Label for the playback session |
| `shidur_png` | string | "/Users/shidur/shidur.png" | Path to shidur.png (for reference) |

### Response Format

```json
{
  "ok": true,
  "message": "Starting lesson repeat: lesson-1 x 10 with 2s black frames",
  "script": "/opt/backup/state/lesson_repeat_1234567890.sh"
}
```

## How It Works

### **Continuous GStreamer Pipeline Approach**

1. **No Signal Drops**: GStreamer pipeline runs continuously, switching content without stopping
2. **Black Frame Creation**: The system automatically creates a 1920x1080 black PNG file if it doesn't exist
3. **Lesson Playback**: Each lesson is played using the standard `playout_all.sh` script with full Dante audio
4. **Black Frame Playback**: Between lessons, a black frame is displayed for the specified duration
5. **Shidur.png Fallback**: After lesson sequence, automatically switches to shidur.png for continuous display
6. **Seamless Switching**: Stop/start operations switch between lesson and shidur.png without signal interruption

### **Workflow**

1. **Start**: `POST /v1/lesson/repeat` → Plays lesson sequence → Switches to shidur.png
2. **Stop**: `POST /v1/lesson/stop` → Stops current content → Switches to shidur.png
3. **Resume**: `POST /v1/lesson/start` → Switches from shidur.png to lesson

## Error Handling

- **Invalid Lesson**: Returns 400 error for invalid lesson names
- **Process Conflicts**: Automatically stops conflicting playout processes
- **Black Frame Fallback**: If PNG creation fails, uses GStreamer test pattern

## Monitoring

You can monitor the progress using the status endpoint:

```bash
curl http://10.66.1.82:8787/v1/state
```

The playout state will show:
- `file`: "repeat:lesson-1" (or whatever lesson is being repeated)
- `label`: The custom label you provided
- `running`: true/false status

## Logs

Check the logs for detailed information:
- `status_http_lesson_repeat.out` - Standard output
- `status_http_lesson_repeat.err` - Error output
- `playout_gst.log` - GStreamer pipeline logs

## Python Example

```python
import requests

# Start lesson repeat
response = requests.post(
    "http://10.66.1.82:8787/v1/lesson/repeat",
    json={
        "lesson": "lesson-1",
        "repeat_count": 10,
        "black_duration": 2,
        "label": "my-repeat"
    }
)

print(response.json())

# Stop lesson repeat
stop_response = requests.post("http://10.66.1.82:8787/v1/lesson/stop")
print(stop_response.json())
```
