#!/bin/bash
# Quick fix for JACK connection issues on MAC-4K

echo "=== JACK Connection Fix ==="
echo ""

echo "Step 1: Check JACK status..."
if pgrep -x jackd > /dev/null; then
    echo "  ✓ JACK is running (PID: $(pgrep -x jackd))"
    
    # Check for stale connections
    echo ""
    echo "Step 2: Checking for stale JACK clients..."
    if command -v jack_lsp &> /dev/null; then
        client_count=$(jack_lsp -c 2>/dev/null | grep -c "^gst-" || echo "0")
        echo "  Found $client_count GStreamer JACK clients"
        
        if [ "$client_count" -gt 10 ]; then
            echo "  ⚠️  Too many stale clients detected!"
            echo ""
            echo "Solution: Restart JACK to clean up connections"
        fi
    else
        echo "  jack_lsp not available, skipping check"
    fi
else
    echo "  ✗ JACK is NOT running"
    echo ""
    echo "Solution: Start JACK first"
fi

echo ""
echo "Step 3: Restart JACK (this will clean up all stale connections)..."
read -p "Restart JACK now? (y/N) " -n 1 -r
echo
if [[ $REPLY =~ ^[Yy]$ ]]; then
    echo "  Stopping JACK..."
    pkill -9 jackd
    sleep 1
    
    echo "  Starting JACK..."
    /opt/homebrew/bin/jackd -R -d coreaudio \
        -C AUDINATE-DVS-UID \
        -P AUDINATE-DVS-UID \
        -r 48000 -p 256 -n 3 > /dev/null 2>&1 &
    
    sleep 2
    
    if pgrep -x jackd > /dev/null; then
        echo "  ✓ JACK restarted successfully (PID: $(pgrep -x jackd))"
    else
        echo "  ✗ JACK failed to start"
        exit 1
    fi
else
    echo "  Skipped JACK restart"
fi

echo ""
echo "Step 4: Restart the controller..."
read -p "Restart lesson-controller now? (y/N) " -n 1 -r
echo
if [[ $REPLY =~ ^[Yy]$ ]]; then
    echo "  Stopping controller..."
    pkill lesson-controller
    sleep 1
    
    echo "  Starting controller..."
    /usr/local/bin/lesson-controller > /dev/null 2>&1 &
    sleep 2
    
    if pgrep lesson-controller > /dev/null; then
        echo "  ✓ Controller started (PID: $(pgrep lesson-controller))"
    else
        echo "  ✗ Controller failed to start"
        echo "  Check logs: tail -20 /opt/backup/logs/lesson_controller.err"
        exit 1
    fi
else
    echo "  Skipped controller restart"
fi

echo ""
echo "=== Fix Complete ==="
echo ""
echo "Test the lesson now:"
echo "  curl -X POST http://127.0.0.1:8788/source/lesson \\"
echo "    -d '{\"path\":\"/opt/backup/lesson-2.mp4\",\"loop\":true}'"
echo ""
echo "If it still fails, you need to build the updated controller:"
echo "  /tmp/build-on-dev.sh"
echo ""

