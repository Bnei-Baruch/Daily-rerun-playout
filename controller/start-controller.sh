#!/bin/bash
# Wrapper script to set library paths for GStreamer jack plugin

export DYLD_FALLBACK_LIBRARY_PATH="/opt/homebrew/lib:$DYLD_FALLBACK_LIBRARY_PATH"
export GST_PLUGIN_PATH="/opt/homebrew/lib/gstreamer-1.0:$GST_PLUGIN_PATH"

exec /Users/shidur/bin/lesson-controller "$@"





