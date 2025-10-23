# Graceful Shutdown & JACK Cleanup

## What Was Added

The controller now properly cleans up JACK connections when it exits.

### Signal Handling

The controller now catches termination signals and shuts down gracefully:

```go
// Catches: Ctrl+C (SIGINT), kill (SIGTERM), interrupt
signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM, syscall.SIGINT)
```

### Shutdown Sequence

When you stop the controller (Ctrl+C, kill, pkill), it:

1. **Catches the signal** and logs: `received signal: interrupt, shutting down gracefully...`
2. **Stops the pipeline**: 
   - Pauses the pipeline first
   - Sets pipeline to NULL state
   - Waits for cleanup to complete (200ms)
   - This properly unregisters all JACK clients
3. **Shuts down HTTP server**: Waits up to 5 seconds for active requests to complete
4. **Logs completion**: `shutdown complete`

### Enhanced Stop() Method

The `Stop()` method now:
- Transitions through PAUSED state before NULL (proper GStreamer cleanup)
- Waits for JACK clients to unregister
- Clears all pipeline references
- Logs the cleanup process

```go
func (m *goGstManager) Stop() error {
    // Pause first for clean transition
    _ = m.pipeline.SetState(gst.StatePaused)
    time.Sleep(100 * time.Millisecond)
    
    // Set to NULL to clean up JACK connections
    _ = m.pipeline.SetState(gst.StateNull)
    time.Sleep(200 * time.Millisecond)
    
    // Clear all references
    m.pipeline = nil
    // ... clear other references
}
```

## Benefits

### Before (Old Behavior):
```bash
$ pkill lesson-controller
# Controller dies immediately
# JACK connections left dangling
# Next start may fail with "Could not open resource for writing"
```

### After (New Behavior):
```bash
$ pkill lesson-controller
# Controller catches signal
# Logs: "received signal: terminated, shutting down gracefully..."
# Logs: "stopping pipeline and cleaning up JACK connections..."
# Logs: "Stop: setting pipeline to NULL state to clean up JACK connections..."
# Logs: "Stop: pipeline stopped and JACK connections cleaned up"
# Logs: "shutdown complete"
# JACK connections properly closed
# Next start works without JACK restart!
```

## Testing

### Test Graceful Shutdown:

```bash
# Start controller
/usr/local/bin/lesson-controller &

# Play something
curl -X POST http://127.0.0.1:8788/playlist/start

# Stop gracefully (Ctrl+C or kill)
pkill lesson-controller

# Check logs - should see graceful shutdown messages
tail /opt/backup/logs/lesson_controller.err
```

Expected log output:
```
received signal: terminated, shutting down gracefully...
stopping pipeline and cleaning up JACK connections...
Stop: setting pipeline to NULL state to clean up JACK connections...
Stop: pipeline stopped and JACK connections cleaned up
shutdown complete
```

### Verify JACK Cleanup:

```bash
# Before stopping controller
jack_lsp | grep gst
# Shows: gst-heb-L, gst-heb-R, gst-en, etc. (many clients)

# Stop controller gracefully
pkill lesson-controller
sleep 1

# After stopping controller
jack_lsp | grep gst
# Shows: (no results - all cleaned up!)
```

## Signals Handled

- **SIGINT** (Ctrl+C): Graceful shutdown
- **SIGTERM** (kill): Graceful shutdown  
- **SIGINT** (interrupt): Graceful shutdown

## Signals NOT Handled

- **SIGKILL** (kill -9): Cannot be caught, immediate termination, JACK connections may leak
  - Avoid using `kill -9` or `pkill -9` unless absolutely necessary
  - Use `pkill lesson-controller` (without -9) instead

## Best Practices

1. **Stop gracefully**: Use `pkill lesson-controller` (not `pkill -9`)
2. **Wait for shutdown**: Give it 1-2 seconds to complete cleanup
3. **Check logs**: Verify "shutdown complete" message
4. **Restart cleanly**: No need to restart JACK after graceful shutdown

## Integration with systemd/launchd

If using a service manager, configure it to:
- Send SIGTERM first (graceful)
- Wait 10 seconds
- Then SIGKILL if still running (force)

Example systemd:
```ini
[Service]
TimeoutStopSec=10
KillMode=mixed
KillSignal=SIGTERM
```

Example launchd:
```xml
<key>ExitTimeOut</key>
<integer>10</integer>
```

## Troubleshooting

**If JACK connections still leak:**
1. Check if controller received the signal: `grep "received signal" /opt/backup/logs/lesson_controller.err`
2. Check if Stop() was called: `grep "Stop: setting pipeline" /opt/backup/logs/lesson_controller.err`
3. Verify you're not using `kill -9` or `pkill -9`
4. Check JACK status: `jack_lsp | grep gst`

**If shutdown hangs:**
- The 5-second HTTP server timeout will force shutdown
- Check for active HTTP requests
- Pipeline cleanup should complete in ~300ms


