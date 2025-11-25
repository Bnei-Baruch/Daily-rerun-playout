#!/bin/bash
# Update Go controller on production machine
# Run this script ON the production machine (10.66.1.82)

set -e

echo "==================================================================="
echo "   Go Controller - Production Update"
echo "==================================================================="
echo ""

# Check if we're on production (optional safety check)
read -p "Are you sure you want to update the production controller? (y/N) " -n 1 -r
echo
if [[ ! $REPLY =~ ^[Yy]$ ]]; then
    echo "Update cancelled."
    exit 0
fi

echo ""
echo "Step 1: Checking prerequisites..."
echo "-----------------------------------"

# Check if Go is installed
if ! command -v go &> /dev/null; then
    echo "❌ Go is not installed"
    exit 1
else
    echo "✓ Go is installed: $(go version)"
fi

echo ""
echo "Step 2: Building new controller..."
echo "------------------------------------"

cd ~/Projects/sviva-playout-go/controller

export PKG_CONFIG_PATH=/opt/homebrew/lib/pkgconfig
export CGO_CFLAGS="-I/opt/homebrew/include"
export CGO_LDFLAGS="-L/opt/homebrew/lib"

echo "Building with go-gst bindings..."
go build -tags gogst -o lesson-controller-go 2>&1 | grep -v "format string is not a string literal" || true

if [ ! -f lesson-controller-go ]; then
    echo ""
    echo "❌ Build failed - binary not created"
    exit 1
fi

echo "✓ Build successful: $(ls -lh lesson-controller-go | awk '{print $5}')"

echo ""
echo "Step 3: Installing new binary..."
echo "----------------------------------"

sudo cp lesson-controller-go /usr/local/bin/lesson-controller-go
sudo chmod +x /usr/local/bin/lesson-controller-go
echo "✓ Binary installed"

echo ""
echo "Step 4: Checking if controller is running..."
echo "----------------------------------------------"

if pgrep -f lesson-controller-go > /dev/null; then
    echo "Controller is currently running"
    read -p "Restart the controller now? (y/N) " -n 1 -r
    echo
    if [[ $REPLY =~ ^[Yy]$ ]]; then
        echo "Stopping old controller..."
        pkill -f lesson-controller-go
        sleep 2
        
        echo "Starting new controller..."
        /usr/local/bin/lesson-controller-go.sh &
        sleep 2
        
        if pgrep -f lesson-controller-go > /dev/null; then
            echo "✓ Controller restarted successfully"
        else
            echo "✗ Controller failed to start"
            echo "Check logs: tail -20 /opt/backup/logs/go_controller.err"
            exit 1
        fi
    else
        echo "⚠️  Controller not restarted - restart manually when ready"
    fi
else
    echo "Controller is not running"
    read -p "Start the controller now? (y/N) " -n 1 -r
    echo
    if [[ $REPLY =~ ^[Yy]$ ]]; then
        /usr/local/bin/lesson-controller-go.sh &
        sleep 2
        
        if pgrep -f lesson-controller-go > /dev/null; then
            echo "✓ Controller started successfully"
        else
            echo "✗ Controller failed to start"
            echo "Check logs: tail -20 /opt/backup/logs/go_controller.err"
            exit 1
        fi
    fi
fi

echo ""
echo "Step 5: Testing..."
echo "-------------------"

if pgrep -f lesson-controller-go > /dev/null; then
    echo "Testing status endpoint on port 8787..."
    if curl -s http://localhost:8787/v1/state > /dev/null 2>&1; then
        echo "✓ Status endpoint responding"
        echo ""
        curl -s http://localhost:8787/v1/state | jq '.playout | {running, mode, pid, heartbeat_iso}'
    else
        echo "✗ Status endpoint not responding"
        echo "Check logs: tail -20 /opt/backup/logs/go_controller.err"
    fi
else
    echo "⚠️  Controller is not running - skipping test"
fi

echo ""
echo "==================================================================="
echo "   Update Complete!"
echo "==================================================================="
echo ""
echo "Controller is now running on port 8787"
echo ""
echo "Useful commands:"
echo "  Status:  curl http://localhost:8787/v1/state | jq"
echo "  Logs:    tail -f /opt/backup/logs/go_controller.err"
echo "  Restart: pkill -f lesson-controller-go && /usr/local/bin/lesson-controller-go.sh &"
echo ""

