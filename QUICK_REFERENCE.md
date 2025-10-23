# Quick Reference - Production Deployment

## Installation (One-Time Setup)

### 1. Copy Repository to Production

```bash
# From dev machine or local machine
rsync -av ~/Projects/sviva-playout/ user@production:~/Projects/sviva-playout/ --exclude='.git' --exclude='controller/lesson-controller*'
```

### 2. Run Installation Script

```bash
# SSH to production machine
ssh user@production

# Run the installer
~/Projects/sviva-playout/scripts/install-production.sh
```

That's it! The script will:
- Check and install dependencies (Go, GStreamer, JACK)
- Backup your current Python system
- Build the Go controller
- Create wrapper script
- Install binaries
- Configure directories

---

## Daily Operations

### Start Go Controller

```bash
pkill -f lesson_controller    # Stop Python
/usr/local/bin/lesson-controller-go.sh &
```

### Start Python Controller

```bash
pkill -f lesson-controller-go  # Stop Go
/usr/local/bin/lesson_controller &
```

### Check Status

```bash
# Go controller (port 8789)
curl http://localhost:8789/v1/state | jq

# Python system (port 8788)
curl http://localhost:8788/v1/state | jq
```

### Check Logs

```bash
# Go controller
tail -f /opt/backup/logs/go_controller.err

# Python system
tail -f /opt/backup/logs/lesson_controller.err
```

---

## API Endpoints

### Go Controller (Port 8789)

```bash
# Status
curl http://localhost:8789/v1/state

# Switch to PNG
curl -X POST http://localhost:8789/source/png

# Play lesson
curl -X POST http://localhost:8789/source/lesson \
  -H "Content-Type: application/json" \
  -d '{"path":"/opt/backup/lesson-1.mp4","loop":false}'

# Prepare playlist
curl -X POST http://localhost:8789/playlist/prepare \
  -H "Content-Type: application/json" \
  -d '{"urls": ["https://cdn.kabbalahmedia.info/eYnxRxlE"]}'

# Start playlist
curl -X POST http://localhost:8789/playlist/start
```

### Python System (Port 8788)

Use your existing Python API endpoints (unchanged).

---

## Troubleshooting

### JACK Connection Errors

```bash
# Restart JACK
pkill -9 jackd
/opt/homebrew/bin/jackd -R -d coreaudio -C AUDINATE-DVS-UID -P AUDINATE-DVS-UID -r 48000 -p 256 -n 3 &

# Restart controller
pkill -f lesson-controller-go
/usr/local/bin/lesson-controller-go.sh &
```

### Controller Won't Start

```bash
# Check logs
tail -50 /opt/backup/logs/go_controller.err

# Check if port is in use
lsof -i :8789

# Verify binary
ls -la /usr/local/bin/lesson-controller-go
```

### Rebuild After Code Changes

```bash
# On production
cd ~/Projects/sviva-playout/controller
go build -tags gogst -o lesson-controller-go
sudo cp lesson-controller-go /usr/local/bin/lesson-controller-go
pkill -f lesson-controller-go
/usr/local/bin/lesson-controller-go.sh &
```

---

## Key Differences

| Feature | Python (Port 8788) | Go (Port 8789) |
|---------|-------------------|----------------|
| **Startup** | Fast | 10-60s preroll |
| **Playlist** | Basic | With prerun + freeze frames |
| **Status** | Basic | Detailed JSON |
| **JACK** | Manual | Auto-cleanup |
| **Original Filenames** | No | Yes |
| **Timing Logs** | No | Yes |

---

## Rollback

If issues occur, Python system is completely untouched:

```bash
pkill -f lesson-controller-go
/usr/local/bin/lesson_controller &
```

Everything works exactly as before.

---

## Important Notes

1. **Only ONE controller at a time** - They share DeckLink and JACK
2. **Different ports** - Go: 8789, Python: 8788
3. **Separate logs** - Check the right log file for each system
4. **Build on target** - Always build the Go controller ON the production machine
5. **JACK must be running** - Start JACK before the controller if needed

---

## Support Commands

```bash
# Check what's running
ps aux | grep -E 'lesson-controller|lesson_controller'

# Check JACK
pgrep -x jackd && echo "JACK running" || echo "JACK stopped"
jack_lsp -c | grep gst-

# Check ports
lsof -i :8788  # Python
lsof -i :8789  # Go

# Disk space
df -h /opt/backup

# Recent errors
tail -100 /opt/backup/logs/go_controller.err | grep -i error
```

---

## Contact / Documentation

- **Full Guide**: `~/Projects/sviva-playout/PRODUCTION_INSTALLATION.md`
- **Build Instructions**: `~/Projects/sviva-playout/BUILD_INSTRUCTIONS.md`
- **Playlist Delay Info**: `~/Projects/sviva-playout/PLAYLIST_STARTUP_DELAY.md`
- **JACK Issues**: `~/Projects/sviva-playout/JACK_STALE_CONNECTIONS.md`

