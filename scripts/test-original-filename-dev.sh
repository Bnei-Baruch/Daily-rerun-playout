#!/bin/bash
# Test the original filename feature on dev machine
# This script needs to be run ON the dev machine (10.66.1.144)

set -e

echo "=== Testing Original Filename Feature ==="
echo ""

# Check if we're on the right machine
if [ "$(hostname)" != "MAC-4K.local" ] && [ "$(hostname)" != "MAC-4K" ]; then
    echo "ERROR: This script should be run on the dev machine (10.66.1.144)"
    echo "Please run: ssh shidur@10.66.1.144"
    echo "Then run this script from there."
    exit 1
fi

# The problem: The binary was cross-compiled from alexm's Mac and won't run on shidur's Mac
# Solution: We need to either:
#   1. Build the binary ON this machine (requires Go to be installed)
#   2. Use the old working binary and prepare a new playlist to test

echo "Step 1: Checking if controller is running..."
if pgrep -x "lesson-controller" > /dev/null; then
    echo "  ✓ Controller is running"
else
    echo "  ✗ Controller is NOT running"
    echo ""
    echo "Starting controller with the backup binary..."
    sudo cp /usr/local/bin/.lesson-controller.bin.backup /usr/local/bin/.lesson-controller.bin
    /usr/local/bin/lesson-controller &
    sleep 3
    
    if pgrep -x "lesson-controller" > /dev/null; then
        echo "  ✓ Controller started successfully"
    else
        echo "  ✗ Controller failed to start"
        echo ""
        echo "Check logs:"
        echo "  tail -50 /opt/backup/logs/lesson_controller.err"
        exit 1
    fi
fi

echo ""
echo "Step 2: Checking current status..."
curl -s http://127.0.0.1:8788/v1/state | jq '.playlist_last_prepared.items[0] | {index, url, original_name, final_url}' || {
    echo "Status endpoint returned an error or doesn't have the new fields yet"
    echo "This is expected if using the old binary"
}

echo ""
echo "Step 3: Preparing a test playlist..."
curl -X POST http://127.0.0.1:8788/playlist/prepare \
  -H "Content-Type: application/json" \
  -d '{"urls": ["https://cdn.kabbalahmedia.info/eYnxRxlE"]}'

echo ""
echo ""
echo "Step 4: Checking if original filename was captured..."
sleep 2
curl -s http://127.0.0.1:8788/v1/state | jq '.playlist_last_prepared.items[0]' || {
    echo "Could not parse status response"
}

echo ""
echo ""
echo "=== Test Complete ==="
echo ""
echo "NOTE: The original_name and final_url fields will only appear if the"
echo "      controller binary includes the latest code changes."
echo ""
echo "To build the new binary ON this machine:"
echo "  1. Install Go: brew install go"
echo "  2. cd ~/Projects/sviva-playout/controller"
echo "  3. go build -tags gogst -o lesson-controller"
echo "  4. sudo cp lesson-controller /usr/local/bin/.lesson-controller.bin"
echo "  5. pkill lesson-controller && /usr/local/bin/lesson-controller &"

