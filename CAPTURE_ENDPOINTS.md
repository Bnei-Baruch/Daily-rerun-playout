# Capture Endpoints

The Go controller now includes capture endpoints that call the existing `capture_sdi_dante.sh` bash script, matching the Python version's functionality.

## Endpoints

### 1. Start Capture

**POST** `/v1/capture/start`

Starts the capture script in the background.

**Request Body (JSON, all optional):**
```json
{
  "label": "my-capture",
  "duration": 3600,
  "mode": "sdi"
}
```

**Parameters:**
- `label` (string, optional): Label for the capture. Default: "capture"
- `duration` (int, optional): Duration in seconds. If not provided, captures indefinitely
- `mode` (string, optional): Audio mode. Set to "sdi" to use embedded SDI stereo audio instead of Dante

**Response (202 Accepted):**
```json
{
  "ok": true,
  "cmd": ["/bin/bash", "/usr/local/bin/capture_sdi_dante.sh", "-r", "my-capture", "-d", "3600"],
  "pid": 12345
}
```

**Example:**
```bash
# Start capture with default settings
curl -X POST http://localhost:8787/v1/capture/start

# Start 1-hour capture with custom label
curl -X POST http://localhost:8787/v1/capture/start \
  -H 'Content-Type: application/json' \
  -d '{"label":"lesson-recording","duration":3600}'

# Start capture with SDI audio
curl -X POST http://localhost:8787/v1/capture/start \
  -H 'Content-Type: application/json' \
  -d '{"label":"sdi-capture","mode":"sdi"}'
```

### 2. Stop Capture

**POST** `/v1/capture/stop`

Stops the currently running capture.

**Response (200 OK):**
```json
{
  "ok": true,
  "killed": true,
  "pid": 12345
}
```

**Example:**
```bash
curl -X POST http://localhost:8787/v1/capture/stop
```

### 3. Get Capture Status

**GET** `/v1/capture`

Returns the current capture status.

**Response (200 OK):**
```json
{
  "running": true,
  "pid": 12345,
  "label": "my-capture",
  "file": "/opt/backup/captures/my-capture.mp4",
  "started_at": 1698765432,
  "mode": "dante"
}
```

**Example:**
```bash
curl http://localhost:8787/v1/capture
```

## Environment Variables

The capture endpoints use these environment variables (with defaults):

- `CAPTURE_BIN`: Path to capture script (default: `/usr/local/bin/capture_sdi_dante.sh`)
- `STATE_DIR`: Directory for state files (default: `/opt/backup/state`)
- `LOG_DIR`: Directory for log files (default: `/opt/backup/logs`)

These are automatically set in the wrapper scripts (`run_lesson_controller.sh` and `lesson-controller-go.sh`).

## How It Works

1. The Go controller spawns the bash capture script as a background process
2. The script writes its PID to `$STATE_DIR/capture.pid`
3. The script manages its own state in `$STATE_DIR/capture.json`
4. Logs are written to:
   - `$LOG_DIR/go_controller_capture.out` (stdout)
   - `$LOG_DIR/go_controller_capture.err` (stderr)

## Compatibility

These endpoints are compatible with the Python version's capture endpoints:
- Same URL paths (`/v1/capture/start`, `/v1/capture/stop`, `/v1/capture`)
- Same request/response formats
- Same underlying bash script (`capture_sdi_dante.sh`)

This means you can switch between the Python and Go controllers without changing your capture automation scripts.

## Notes

- The capture process runs independently of the playout pipeline
- You can capture while playing out content
- The capture script handles all the GStreamer pipeline management for capture
- The Go controller just spawns and monitors the script

