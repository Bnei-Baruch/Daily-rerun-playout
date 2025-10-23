#!/bin/bash
# Test script for running the new controller with status endpoint on dev machine (10.66.1.144)

echo "=== Testing Status Endpoint on Dev Machine (10.66.1.144) ==="
echo ""
echo "This script will:"
echo "1. Stop any existing controller on port 8788"
echo "2. Start the new controller with status endpoint"
echo "3. Test the status endpoint"
echo "4. Test switching between sources"
echo ""

# SSH commands
SSH="ssh shidur@10.66.1.144"

echo "Step 1: Stopping existing controller..."
$SSH "pkill -f lesson-controller || true"
sleep 2

echo ""
echo "Step 2: Starting new controller on port 8788..."
$SSH "chmod +x /tmp/lesson-controller-new"
$SSH "cd /tmp && \
      AUDIO_MAP_PATH=/Users/shidur/Projects/sviva-playout/controller/audio_map.json.sample \
      PNG_PATH=/Users/shidur/shidur.png \
      DECKLINK_DEVICE=0 \
      DECKLINK_MODE=1080i50 \
      PLAYLIST_DIR=/opt/backup/playlist \
      PRERUN_VIDEO_PATH=/opt/backup/prerun.mp4 \
      FFMPEG_PATH=/opt/homebrew/bin/ffmpeg \
      PORT=8788 \
      GO_GST=1 \
      DYLD_LIBRARY_PATH=/opt/homebrew/lib:\$DYLD_LIBRARY_PATH \
      JACK_DEFAULT_SERVER=default \
      GST_PLUGIN_SYSTEM_PATH=/opt/homebrew/lib/gstreamer-1.0 \
      GST_PLUGIN_PATH=/opt/homebrew/lib/gstreamer-1.0 \
      nohup ./lesson-controller-new > lesson-controller.log 2>&1 &"

sleep 3

echo ""
echo "Step 3: Testing status endpoint..."
echo "GET http://10.66.1.144:8788/v1/state"
curl -s http://10.66.1.144:8788/v1/state | jq '.' || curl -s http://10.66.1.144:8788/v1/state

echo ""
echo ""
echo "Step 4: Switch to PNG and check status..."
curl -s -X POST http://10.66.1.144:8788/source/png
sleep 1
echo "Status after PNG:"
curl -s http://10.66.1.144:8788/v1/state | jq '.' || curl -s http://10.66.1.144:8788/v1/state

echo ""
echo ""
echo "Step 5: Switch to lesson and check status..."
curl -s -X POST http://10.66.1.144:8788/source/lesson -d '{"path":"/opt/backup/lesson-2.mp4","loop":true}'
sleep 2
echo "Status after lesson:"
curl -s http://10.66.1.144:8788/v1/state | jq '.' || curl -s http://10.66.1.144:8788/v1/state

echo ""
echo ""
echo "=== Test Complete ==="
echo ""
echo "To view logs: ssh shidur@10.66.1.144 'tail -f /tmp/lesson-controller.log'"
echo "To stop controller: ssh shidur@10.66.1.144 'pkill -f lesson-controller'"
echo ""

