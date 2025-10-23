#!/bin/bash
# Install the new controller with status endpoint on dev machine (10.66.1.144)

echo "=== Installing Controller with Status Endpoint on Dev Machine ==="
echo ""

# Copy binary to dev machine
echo "Step 1: Copying new binary to dev machine..."
scp /Users/alexm/Projects/sviva-playout/controller/lesson-controller shidur@10.66.1.144:/tmp/lesson-controller-new
echo "✓ Binary copied"

echo ""
echo "Step 2: Please run the following commands on the dev machine (10.66.1.144):"
echo ""
echo "  ssh shidur@10.66.1.144"
echo ""
echo "  # Stop the controller"
echo "  sudo pkill -f lesson-controller"
echo ""
echo "  # Backup the old binary"
echo "  sudo cp /usr/local/bin/.lesson-controller.bin /usr/local/bin/.lesson-controller.bin.backup"
echo ""
echo "  # Install the new binary"
echo "  sudo cp /tmp/lesson-controller-new /usr/local/bin/.lesson-controller.bin"
echo "  sudo chmod +x /usr/local/bin/.lesson-controller.bin"
echo ""
echo "  # Start the controller"
echo "  /usr/local/bin/lesson-controller &"
echo ""
echo "  # Test the status endpoint"
echo "  curl http://localhost:8788/v1/state | jq '.'"
echo ""
echo "  # Test lesson switching"
echo "  curl -X POST http://localhost:8788/source/lesson -d '{\"path\":\"/opt/backup/lesson-2.mp4\",\"loop\":true}'"
echo "  curl http://localhost:8788/v1/state | jq '.'"
echo ""
echo "  # Switch back to PNG"
echo "  curl -X POST http://localhost:8788/source/png"
echo "  curl http://localhost:8788/v1/state | jq '.'"
echo ""
echo "=== Installation Instructions Complete ==="

