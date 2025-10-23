# JACK Stale Connection Issue & Solutions

## Problem

When GStreamer pipelines fail to start or are torn down improperly, JACK audio clients remain registered but disconnected. This causes subsequent pipeline starts to fail with:
```
Could not open resource for writing.
Jack client open error (status 33)
```

## Root Cause

- GStreamer creates JACK clients for each `jackaudiosink` element
- If the pipeline fails during `SetState(PLAYING)`, the JACK clients may not be properly unregistered
- JACK server accumulates "zombie" client connections
- Eventually, JACK refuses new connections

## Solutions Implemented

### 1. **Pipeline Cleanup on Failure** ✅ (Already in code)

When `SetState(PLAYING)` fails, we immediately call `SetState(StateNull)` to clean up:

```go
if err := pipe.SetState(gst.StatePlaying); err != nil {
    logBusErrors("playlist-pipeline", pipe)
    // Clean up the pipeline on failure to avoid stale JACK connections
    _ = pipe.SetState(gst.StateNull)
    return fmt.Errorf("set PLAYING: %w", err)
}
```

**Location:** 
- `buildLessonOnlyPipeline()` - line ~1117
- `StartPlaylist()` - line ~1445

### 2. **JACK Error Detection** ✅ (Just added)

Detect JACK connection errors and log helpful restart instructions:

```go
func isJackConnectionError(pipe *gst.Pipeline) bool {
    // Checks for:
    // - "Could not open resource for writing"
    // - "Jack client open error"
    // - "jackaudiosink" in debug messages
}
```

When detected, logs:
```
JACK connection error detected - JACK may need restart
Run: pkill -9 jackd && /opt/homebrew/bin/jackd -R -d coreaudio -C AUDINATE-DVS-UID -P AUDINATE-DVS-UID -r 48000 -p 256 -n 3 &
```

### 3. **Manual JACK Restart** (Current workaround)

When you see the error, restart JACK:

```bash
# On MAC-4K (10.66.1.144):
pkill -9 jackd
sleep 2
/opt/homebrew/bin/jackd -R -d coreaudio -C AUDINATE-DVS-UID -P AUDINATE-DVS-UID -r 48000 -p 256 -n 3 &
```

Or use the script:
```bash
/Users/shidur/Projects/sviva-playout/bin/jack_start.sh
```

## Future Improvements (Not Yet Implemented)

### Option A: Automatic JACK Restart

Add an HTTP endpoint to restart JACK:

```go
func (m *goGstManager) RestartJack() error {
    cmd := exec.Command("pkill", "-9", "jackd")
    _ = cmd.Run()
    time.Sleep(2 * time.Second)
    
    cmd = exec.Command("/opt/homebrew/bin/jackd",
        "-R", "-d", "coreaudio",
        "-C", "AUDINATE-DVS-UID",
        "-P", "AUDINATE-DVS-UID",
        "-r", "48000",
        "-p", "256",
        "-n", "3")
    return cmd.Start()
}
```

Then add endpoint:
```go
r.HandleFunc("/admin/jack/restart", handleJackRestart).Methods(http.MethodPost)
```

### Option B: Retry with JACK Restart

Automatically retry pipeline start after restarting JACK:

```go
func (m *goGstManager) StartPlaylist() error {
    // ... build pipeline ...
    
    err := pipe.SetState(gst.StatePlaying)
    if err != nil && isJackConnectionError(pipe) {
        log.Printf("StartPlaylist: JACK error, restarting JACK and retrying...")
        _ = pipe.SetState(gst.StateNull)
        
        if err := m.RestartJack(); err != nil {
            return fmt.Errorf("failed to restart JACK: %w", err)
        }
        
        time.Sleep(3 * time.Second)
        
        // Rebuild and retry
        pipe, err = gst.NewPipeline("playlist-pipeline")
        // ... rebuild pipeline ...
        err = pipe.SetState(gst.StatePlaying)
    }
    
    return err
}
```

### Option C: Use JACK Session Management

Configure JACK to better handle client lifecycle:
- Set JACK timeout parameters
- Use JACK session API for proper cleanup
- Monitor JACK client list and clean up zombies

### Option D: Reduce JACK Client Count

Instead of 37 separate `jackaudiosink` elements, use:
- Single JACK client with multiple ports
- Custom GStreamer element that manages JACK connections
- JACK patchbay to route single client to multiple destinations

## Monitoring

Check JACK client status:
```bash
# List JACK clients
jack_lsp -c

# Check JACK server status
jack_wait -s

# Monitor JACK connections
qjackctl  # GUI tool
```

## Best Practices

1. **Always clean up pipelines**: Call `SetState(StateNull)` before destroying
2. **Monitor logs**: Watch for JACK error messages
3. **Restart JACK periodically**: If running 24/7, restart JACK daily
4. **Test pipeline changes**: Verify JACK cleanup after errors
5. **Use the wrapper script**: Ensures consistent JACK startup parameters

## Current Status

✅ Pipeline cleanup on failure  
✅ JACK error detection and logging  
⏳ Manual JACK restart required  
❌ Automatic JACK restart not implemented  

The current solution (cleanup + detection) reduces the frequency of stale connections significantly, but manual JACK restarts are still occasionally needed.


