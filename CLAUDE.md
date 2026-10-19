# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## What This Project Is

SVIVA Playout is a broadcast-grade video/audio playout system for 24/7 streaming to Blackmagic DeckLink SDI output. It manages multi-format playback (MP4, HLS streams, PNG images, playlist queues) with hot source-switching and multi-language audio routing (36 JACK channels for Hebrew + 34 other languages).

## Build & Run Commands

### Go Controller (primary component)

```bash
cd controller

# Build without GStreamer (for syntax/structure checks only)
go build -o lesson-controller

# Build with full GStreamer integration (required for actual operation)
go build -tags gogst -o lesson-controller

# Run (from project root — uses bin wrapper)
./bin/run_lesson_controller.sh
```

### Dependencies & validation

```bash
# Go module tidy
cd controller && go mod tidy

# Python config validation
python3 config.py

# Shell script syntax check
bash -n bin/playout_all.sh

# Python server syntax check
python3 -m py_compile server/status_http.py
```

### Infrastructure

```bash
./bin/jack_start.sh          # Start JACK audio server (required before playback)
./bin/jack_stop.sh           # Stop JACK
```

### Deployment

```bash
./scripts/deploy-controller-to-prod.sh    # Deploy to production
```

### Logs

The controller redirects its own stdout/stderr to `$LOG_DIR/lesson_controller.log`
at startup (fd-level, so panic traces and cgo crash reports are captured); set
`LOG_TO_STDERR=1` to keep output on the terminal. Unclean exits are detected via a
run marker in `$STATE_DIR/controller_run.json` and tallied in
`$LOG_DIR/lesson_controller.crashes.log`.

There are no automated tests. Validation is done by running the system and exercising the HTTP API.

## Architecture

The system has two parallel implementations — the Go controller (active/preferred) and legacy shell+Python stack:

```
Go Controller (port 8788)         Python Status Server (port 8787)
controller/lesson-controller.go   server/status_http.py
controller/manager_gogst.go       ↕ manages subprocesses
        ↓                         bin/playout_all.sh (GStreamer cmdline)
GStreamer pipeline (go-gst cgo)
        ↓
DeckLink SDI output (1080i50, UYVY)
        ↓
JACK Audio (36 channels, 48kHz)
```

### Go Controller

- **[controller/lesson-controller.go](controller/lesson-controller.go)** — HTTP handlers, routing (gorilla/mux), state management. All API endpoints are defined here.
- **[controller/manager_gogst.go](controller/manager_gogst.go)** — GStreamer pipeline engine (1844 lines). This is the core of the system: creates a persistent pipeline and hot-swaps sources without restarting. Built with `-tags gogst` (cgo required).
- **[controller/manager_iface.go](controller/manager_iface.go)** — Interface definitions for the pipeline manager, allowing the build without GStreamer tags.
- **[controller/capture_manager.go](controller/capture_manager.go)** — SDI capture integration.
- **[controller/playlists.go](controller/playlists.go)** — Watches the editor's playlist feed (`https://shidur.kab.info/db/shidur/playlist`, every 30s) and keeps every playlist prepared in memory: each in/out pair becomes a segment, consecutive segments with the same audio layout are stitched into one HLS stream, and the pipeline routing audio to JACK is built from the master playlist (rules ported from Bnei-Baruch/media-streamer). The last feed is saved to `$STATE_DIR/playlist_feed.json` for restarts.
- **[controller/hls_concat.go](controller/hls_concat.go)** — HLS helpers: master/media playlist parsing and stitching clips with `EXT-X-DISCONTINUITY`.

### Source Switching Model

The GStreamer pipeline stays running continuously. Source switching works by swapping `uridecodebin` elements or using `funnel`/`selector` patterns — no pipeline teardown. Sources:
- **PNG**: Static image via `filesrc` → `pngdec` → scale to 1920x1080i50
- **Lesson**: MP4 file via `uridecodebin`
- **HLS**: M3U8 URL via `hlsdemux`. A playlist id plays its prepared segments: one stitched stream per audio layout, the pipeline rebuilt only where the layout changes, PNG after the last one. In-points must be on keyframes or A/V drifts at each join (the editor already snaps them).
- **Playlist**: Sequential lesson files with preroll and PNG transitions between items

### Audio Routing

Audio is routed to 36 JACK ports. Port assignment is configured in `controller/audio_map.json` (see `audio_map.json.sample`). Hebrew is ports 20-21 (stereo); all other languages are mono ports. The pipeline extracts named audio tracks from the source file by language tag.

### State Persistence

System state is written to JSON files in `$STATE_DIR` (default: `/opt/backup/state/`). The Go controller reads/writes these files to survive restarts. Key state fields include current source type, file path, loop settings, and playlist position.

## API Endpoints (Go Controller, port 8788)

```
POST /source/png          { "path": "/path/to/image.png" }
POST /source/lesson       { "path": "/path/to/video.mp4", "loop": true }
POST /source/hls          { "url": "http://..." }
POST /source/hls/{id}     Play prepared playlist {id} from the editor's feed
GET  /api/playlists       Every playlist by id: total_ms, total, date, file_path, segments, parts (Companion reads total_ms)
GET  /api/playlists/{id}  Segments, parts and pipelines of one playlist
POST /api/playlists/refresh  Read the feed now instead of at the next poll
GET  /hls-concat/{token}/{file}  Stitched HLS playlists (used by the pipeline itself)
POST /playlist/prepare    { "urls": ["..."], "name": "..." }  — async download
POST /playlist/start      { "name": "..." }
POST /playlist/next
POST /capture/start
POST /capture/stop
GET  /v1/state            Returns current pipeline state as JSON
```

## Configuration

Environment variables are defined in `.env` (copy from `env.example`). Key variables:

| Variable | Purpose |
|----------|---------|
| `STATE_DIR` | Where JSON state files are written (default: `/opt/backup/state`) |
| `PLAYLIST_SOURCE_URL` | Editor playlist feed (default: `https://shidur.kab.info/db/shidur/playlist`) |
| `PLAYLIST_POLL` | How often the feed is checked (default: `30s`) |
| `LOG_DIR` | Log file directory |
| `JACK_RATE` | Audio sample rate (48000) |
| `JACK_BUFFER` | JACK buffer size (256) |
| `PLAYLIST_DIR` | Where prepared playlist files are stored |
| `NGINX_VOD_URL` | Base URL for nginx VOD module (HLS-map mode) |

`config.py` loads the `.env` file and validates required variables — run `python3 config.py` to check.

## Key Constraints

- The GStreamer build tag (`-tags gogst`) is required to compile the pipeline code; without it, `manager_gogst.go` is excluded and only the interface stub compiles.
- The system targets **1080i50 interlaced** output (not progressive). Any video scaling or format conversion in the pipeline must preserve the interlace structure and field order (top-field-first).
- JACK must be running before the controller starts audio output. `jack_start.sh` sets up the server with the hardware device.
- The nginx VOD module must be running for HLS-map source type to work (see `VOD_SETUP_STATUS.md`).
