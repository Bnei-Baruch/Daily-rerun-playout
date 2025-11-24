# Port Configuration Change

## Summary

Changed the default port for the Go controller from **8789** to **8787**.

## Ports Overview

| System | Port | Purpose |
|--------|------|---------|
| **Python/Bash** | 8788 | Existing production system |
| **Go Controller** | **8787** | New Go-based controller |

## Files Updated

1. **`bin/run_lesson_controller.sh`**
   - Changed default PORT from 8788 to 8787

2. **`PRODUCTION_INSTALLATION.md`**
   - Updated all port references from 8789 to 8787
   - Updated documentation examples

3. **`scripts/install-production.sh`**
   - Changed PORT default to 8787

4. **`QUICK_REFERENCE.md`**
   - Updated all API endpoint examples

5. **`STATUS_ENDPOINT_README.md`**
   - Updated status endpoint documentation

## API Endpoints

The Go controller now listens on port **8787**:

```bash
# Status
curl http://localhost:8787/v1/state

# Switch to PNG
curl -X POST http://localhost:8787/source/png

# Play lesson
curl -X POST http://localhost:8787/source/lesson \
  -H "Content-Type: application/json" \
  -d '{"path":"/opt/backup/lesson-1.mp4","loop":false}'

# Prepare playlist
curl -X POST http://localhost:8787/playlist/prepare \
  -H "Content-Type: application/json" \
  -d '{"urls": ["https://cdn.kabbalahmedia.info/eYnxRxlE"]}'

# Start playlist
curl -X POST http://localhost:8787/playlist/start
```

## Environment Variable

You can override the port by setting the `PORT` environment variable:

```bash
export PORT=8787
/usr/local/bin/lesson-controller-go.sh &
```

## Wrapper Script

The wrapper script at `/usr/local/bin/lesson-controller-go.sh` now defaults to port 8787:

```bash
PORT="${PORT:-8787}"
```

## Testing

**On dev machine (10.66.1.144):**
```bash
# The controller should already be running on the old port
# You'll need to restart it to pick up the new port

pkill lesson-controller
export PORT=8787
/usr/local/bin/lesson-controller &

# Test
curl http://localhost:8787/v1/state
```

**On production (10.66.1.82):**
```bash
# When you deploy, it will automatically use port 8787
/usr/local/bin/lesson-controller-go.sh &

# Test
curl http://localhost:8787/v1/state
```

## Why Port 8787?

- **8788**: Used by existing Python/Bash system
- **8787**: New Go controller (one less than Python)
- Both can coexist on the same machine
- Easy to remember: Go controller is one port below Python

## Migration Path

1. Python system continues on **8788**
2. Go controller runs on **8787**
3. Switch between them by stopping one and starting the other
4. Both use the same DeckLink and JACK resources (can't run simultaneously)

## Note

The port change is in the code and documentation. To apply it:

1. **Dev machine**: Restart the controller with `PORT=8787`
2. **Production**: Will use 8787 automatically when deployed
3. **Update any monitoring/scripts** that reference port 8789 → 8787

