#!/bin/bash
# Start the Go lesson controller on the test machine (port 8788)

export PNG_PATH=/Users/shidur/shidur.png
export DECKLINK_MODE=1080i50
export DECKLINK_DEVICE=0
export RERUN_PNG=/Users/shidur/rerun.png
export PREROLL=/Users/shidur/30sec.mp4
export TIMER_PATH=/opt/backup/timer.mov
export CLIP_PATH=/Users/shidur/logo.mp4

export AUDIO_MAP_PATH=/Users/shidur/Projects/sviva-playout/controller/audio_map.json.sample
export PLAYLIST_DIR=/opt/backup/playlist

# GStreamer + JACK runtime paths
export DYLD_LIBRARY_PATH="/opt/homebrew/lib${DYLD_LIBRARY_PATH:+:$DYLD_LIBRARY_PATH}"
export GST_PLUGIN_SYSTEM_PATH=/opt/homebrew/lib/gstreamer-1.0
export GST_PLUGIN_PATH=/opt/homebrew/lib/gstreamer-1.0
export JACK_DEFAULT_SERVER=default

cd ~
exec /Users/shidur/bin/lesson-controller 2>&1 | tee -a ~/go-controller.log
