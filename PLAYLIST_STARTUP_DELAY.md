# Playlist Startup Delay

## The Issue

When starting a playlist with `/playlist/start`, there's a **10-60 second delay** before playback begins. This is normal GStreamer behavior called "prerolling".

## Why It Takes So Long

### 1. Pipeline Complexity

The playlist pipeline is **very complex**:
- **Video**: concat → videoconvert → videoscale → videorate → interlace → decklinkvideosink
- **Audio**: concat → tee → **30+ jackaudiosink clients** (one per language)

Each playlist with 2 items includes:
- 2× prerun videos (30 seconds each)
- 2× main lesson videos
- 2× freeze frame PNGs (5 seconds each)

That's **6 media sources** being prerolled!

### 2. JACK Audio Connections

The pipeline creates **30+ JACK audio clients** simultaneously:
```
gst-heb-L, gst-heb-R, gst-en, gst-ru, gst-es, gst-fr, gst-it, gst-de,
gst-tr, gst-pt, gst-bg, gst-ka, gst-ro, gst-hu, gst-sv, gst-lt, gst-hr,
gst-ja, gst-sl, gst-pl, gst-no, gst-lv, gst-uk, gst-nl, gst-zh, gst-am,
gst-hi, gst-fa, gst-ar, gst-id, gst-hy, gst-da, gst-et, gst-el, gst-tl, gst-az
```

Each JACK client needs to:
- Register with the JACK server
- Allocate ring buffers
- Set up routing to Dante ports

### 3. Preroll Process

GStreamer's preroll ensures the entire pipeline is ready before playback starts:

1. **Parse pipeline** (~0.1s): Build the GStreamer element graph
2. **Create elements** (~0.5s): Instantiate all elements
3. **Link pads** (~1s): Connect all video/audio streams
4. **Negotiate caps** (~2-5s): Determine formats for all streams
5. **Buffer data** (~5-10s): Fill initial buffers
6. **JACK registration** (~5-20s): Register all 30+ JACK clients
7. **Ready check** (~1-5s): Ensure everything is synchronized

**Total: 10-60 seconds**

The time varies based on:
- System load
- JACK server responsiveness
- File I/O speed
- CPU performance

## Current Behavior

The controller logs timing information:

```
StartPlaylist: starting pipeline (preroll may take 10-60 seconds)...
[... GStreamer internal work happens here ...]
StartPlaylist: pipeline started successfully in 45.3 seconds
StartPlaylist: playing 2 items
```

This helps you understand what's happening during the delay.

## What's Normal

- **10-20 seconds**: Good performance
- **20-40 seconds**: Normal
- **40-60 seconds**: Slow but acceptable
- **> 60 seconds**: Something might be wrong (JACK issues, slow disk, etc.)

## Optimization Options

### Option 1: Accept the Delay (Current Approach)

**Pros:**
- Simple, no changes needed
- Reliable once started
- Works with any number of playlist items

**Cons:**
- Long startup time
- Can't provide immediate feedback to users

### Option 2: Pre-build Pipeline During Preparation

Pre-create the pipeline when `/playlist/prepare` is called:

```go
func (m *goGstManager) LoadPlaylist(p Playlist) error {
    // ... download files ...
    
    // Pre-build pipeline in PAUSED state
    pipe, err := m.buildPlaylistPipeline()
    if err != nil {
        return err
    }
    pipe.SetState(gst.StatePaused) // Preroll happens here
    m.preparedPipeline = pipe
    
    return nil
}

func (m *goGstManager) StartPlaylist() error {
    // Just switch to PLAYING (instant!)
    m.preparedPipeline.SetState(gst.StatePlaying)
    return nil
}
```

**Pros:**
- Instant playback when `/playlist/start` is called
- Better user experience

**Cons:**
- Uses memory while waiting
- Pipeline might get stale if prepared far in advance
- Need to handle pipeline cleanup if never started

### Option 3: Reduce JACK Clients

Instead of creating 30+ separate JACK clients, use a single client with multiple ports:

```
Single JACK client "gst-playout" with 30 output ports
```

**Pros:**
- Faster JACK registration
- Less overhead
- Easier to manage

**Cons:**
- Requires significant code refactoring
- Might affect audio routing flexibility

### Option 4: Progressive Pipeline Building

Build the pipeline incrementally and start playing the first item while preparing the next:

**Pros:**
- Can start playing sooner
- Better perceived performance

**Cons:**
- Much more complex code
- Risk of glitches between items

## Recommendation

For now, **accept the delay** and use the timing logs to monitor performance. The delay is a one-time cost at playlist startup, and once playing, the content streams smoothly.

If instant startup becomes critical, implement **Option 2** (pre-build during preparation).

## Testing

To measure startup time on your system:

```bash
# Start the playlist
time curl -X POST http://127.0.0.1:8788/playlist/start

# Check the logs
tail -f /opt/backup/logs/lesson_controller.err | grep "pipeline started successfully"
```

Example output:
```
StartPlaylist: starting pipeline (preroll may take 10-60 seconds)...
StartPlaylist: pipeline started successfully in 32.4 seconds
```

## Related Issues

- If startup takes **> 60 seconds**, check:
  - JACK server health: `jack_lsp` should respond quickly
  - Disk I/O: Files should be on fast storage
  - CPU load: `top` should show reasonable CPU usage
  - Memory: Ensure sufficient RAM available

- If JACK clients fail to register:
  - Restart JACK: `pkill -9 jackd && /opt/homebrew/bin/jackd -R -d coreaudio ...`
  - Check JACK logs: `ps aux | grep jackd`
  - Verify Dante Virtual Soundcard is running

## Summary

The 10-60 second delay is **expected behavior** due to GStreamer's thorough preroll process and the complexity of the playlist pipeline with 30+ JACK audio clients. The delay ensures reliable, glitch-free playback once started.

