## Cross-Compilation Issue

### Problem

The controller binary compiled on one Mac (alexm's machine) crashes immediately when run on another Mac (shidur's dev machine at 10.66.1.144), even though both are ARM64 Macs running macOS.

### Why This Happens

1. **Dynamic Library Paths**: The compiled binary has hardcoded paths to dynamic libraries:
   ```
   /opt/homebrew/opt/gstreamer/lib/libgstreamer-1.0.0.dylib
   /opt/homebrew/opt/glib/lib/libgobject-2.0.0.dylib
   ```

2. **Library Version Mismatches**: Even if the paths exist, the library versions might differ between machines.

3. **CGO Dependencies**: The `go-gst` bindings use CGO, which links against system libraries at compile time.

### Symptoms

When running the cross-compiled binary:
```bash
/usr/local/bin/.lesson-controller.bin
# Process exits immediately with no output
# Exit code: 255 or "killed" message
```

The binary crashes before it can even initialize logging, so no error messages appear in the logs.

### Solution Options

#### Option 1: Build on Target Machine (RECOMMENDED)

Install Go on the dev machine and build there:

```bash
# On dev machine (10.66.1.144)
brew install go

cd ~/Projects/sviva-playout/controller

export PKG_CONFIG_PATH=/opt/homebrew/lib/pkgconfig
export CGO_CFLAGS="-I/opt/homebrew/include"
export CGO_LDFLAGS="-L/opt/homebrew/lib"

go build -tags gogst -o lesson-controller

sudo cp lesson-controller /usr/local/bin/.lesson-controller.bin
sudo chmod +x /usr/local/bin/.lesson-controller.bin

pkill lesson-controller
/usr/local/bin/lesson-controller &
```

#### Option 2: Use Existing Working Binary

The dev machine has a working binary backup at:
```
/usr/local/bin/.lesson-controller.bin.backup
```

This was compiled on the dev machine previously. To restore it:

```bash
sudo cp /usr/local/bin/.lesson-controller.bin.backup /usr/local/bin/.lesson-controller.bin
/usr/local/bin/lesson-controller &
```

**Note**: This binary may not have the latest code changes (like original filename capture).

#### Option 3: Static Linking (Complex)

Theoretically possible but very difficult with GStreamer:
- Would need to statically link all GStreamer plugins
- GStreamer plugins are typically loaded dynamically
- Not practical for this use case

### Testing the New Feature

To test the original filename capture feature:

1. **Install Go on dev machine** (if not already installed):
   ```bash
   brew install go
   ```

2. **Sync and build**:
   ```bash
   # From your local machine
   rsync -av controller/ shidur@10.66.1.144:~/Projects/sviva-playout/controller/
   
   # On dev machine
   ssh shidur@10.66.1.144
   cd ~/Projects/sviva-playout/controller
   go build -tags gogst -o lesson-controller
   sudo cp lesson-controller /usr/local/bin/.lesson-controller.bin
   pkill lesson-controller
   /usr/local/bin/lesson-controller &
   ```

3. **Test the feature**:
   ```bash
   # Prepare a playlist
   curl -X POST http://127.0.0.1:8788/playlist/prepare \
     -H "Content-Type: application/json" \
     -d '{"urls": ["https://cdn.kabbalahmedia.info/eYnxRxlE"]}'
   
   # Check the status
   curl -s http://127.0.0.1:8788/v1/state | jq '.playlist_last_prepared.items[0]'
   ```

   You should see:
   ```json
   {
     "index": 1,
     "url": "https://cdn.kabbalahmedia.info/eYnxRxlE",
     "file": "/opt/backup/playlist/url_1.mp4",
     "png": "/opt/backup/playlist/url_1_last.png",
     "original_name": "heb_o_rav_2025-10-18_program_sichot-al-haderech_bakenes-haba-mitchabrim-leasyria-klalit_hd.mp4",
     "final_url": "https://files.kabbalahmedia.info/get/d2df5d521be8aaa7d4dd8b8b284c56114a904abb/heb_o_rav_2025-10-18_program_sichot-al-haderech_bakenes-haba-mitchabrim-leasyria-klalit_hd.mp4",
     "duration_hms": "00:25:04",
     "file_exists": true,
     "png_exists": true
   }
   ```

### For Production Deployment

When deploying to the production machine:

1. **Check if Go is installed**:
   ```bash
   ssh user@production-machine "which go"
   ```

2. **If Go is available**: Build on the production machine
3. **If Go is NOT available**: 
   - Install Go first, OR
   - Set up a build machine with the same OS/architecture as production
   - Use that build machine to compile binaries for production

### Wrapper Script

The `/usr/local/bin/lesson-controller` wrapper script handles:
- Setting environment variables (DYLD_LIBRARY_PATH, etc.)
- Redirecting logs to `/opt/backup/logs/`
- Executing the actual binary at `/usr/local/bin/.lesson-controller.bin`

Always use the wrapper script to start the controller:
```bash
/usr/local/bin/lesson-controller &
```

NOT:
```bash
/usr/local/bin/.lesson-controller.bin &  # Will fail without env vars
```

### Debugging Cross-Compilation Issues

If you encounter similar issues:

1. **Check library dependencies**:
   ```bash
   otool -L /path/to/binary
   ```

2. **Verify libraries exist on target**:
   ```bash
   ls -la /opt/homebrew/opt/gstreamer/lib/libgstreamer-1.0.0.dylib
   ```

3. **Check for crash reports**:
   ```bash
   ls -lt ~/Library/Logs/DiagnosticReports/
   ```

4. **Try running with verbose output**:
   ```bash
   GST_DEBUG=3 /path/to/binary
   ```

5. **Check system logs**:
   ```bash
   sudo dmesg | tail -20
   ```

### Summary

**The Issue**: CGO-based Go applications with dynamic library dependencies cannot be reliably cross-compiled between different machines, even with the same architecture.

**The Solution**: Always build on the target machine or on a machine with identical library versions.

**For This Project**: Install Go on the dev machine and build there.

