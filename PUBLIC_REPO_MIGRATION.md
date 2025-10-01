# Public Repository Migration Guide

This document outlines the changes made to make the SVIVA Playt System safe for public repository viewing and how to migrate from hardcoded paths to environment variables.

## What Has Been Changed

### ✅ **Completed Changes**

1. **Environment Configuration Files**
   - `env.example` - Template for environment variables
   - `config.py` - Python configuration module
   - `.gitignore` - Comprehensive protection for sensitive files
   - `README.md` - Complete setup and usage documentation

2. **Shell Script Updates** (`bin/playout_all.sh`)
   - `BB_STATUS_FILE` - Now uses `${STATE_DIR:-/opt/backup}/status.json`
   - `LOG` - Now uses `${LOG_DIR:-/opt/backup/logs}/playout_gst.log`
   - `OVERLAY_PNG` - Now uses `${OVERLAY_PNG:-/path/to/your/overlay.png}`
   - `lesson-[1-4]` paths - Now use `${LATEST_DIR:-/opt/backup/latest}/$RAW.mp4`
   - `playlist_dir` - Now uses `${PLAYLIST_DIR:-/opt/backup/playlist}`
   - `FFMPEG` paths - Now use `${FFMPEG_PATH:-/path/to/ffmpeg}`
   - `GST` paths - Now use `${GST_PATH:-/path/to/gst-launch-1.0}`

3. **New Environment Variables**
   - `STATE_DIR` - Base directory for state files
   - `LOG_DIR` - Base directory for log files
   - `LATEST_DIR` - Directory for latest lesson files
   - `PLAYLIST_DIR` - Directory for playlist files
   - `GST_PATH` - Path to GStreamer binary
   - `FFMPEG_PATH` - Path to FFmpeg binary
   - `OVERLAY_PNG` - Default overlay image path

## Migration Steps for Existing Users

### 1. **Create Environment File**

```bash
# Copy the example environment file
cp env.example .env

# Edit with your actual paths
nano .env
```

### 2. **Update Your Environment Variables**

```bash
# Production deployment settings
PROD_HOST=shidur@10.66.1.82
PROD_DIR=/Users/shidur/Projects/sviva-playout

# System paths and directories
STATE_DIR=/opt/backup/state
LOG_DIR=/opt/backup/logs
BB_STATUS_FILE=/opt/backup/status.json
BIN_DST=/usr/local/bin
LATEST_DIR=/opt/backup/latest
PLAYLIST_DIR=/opt/backup/playlist

# Playt system configuration
OVERLAY_PNG=/Users/shidur/rerun.png
GST_PATH=/usr/local/bin/gst-launch-1.0
FFMPEG_PATH=/usr/local/bin/ffmpeg

# Optional security token
STATUS_HTTP_TOKEN=your-secret-token-here
```

### 3. **Source Environment Variables**

```bash
# Add to your shell profile (.bashrc, .zshrc, etc.)
export $(cat .env | xargs)

# Or source directly
source .env
```

### 4. **Test Configuration**

```bash
# Test the configuration
python3 config.py

# Test shell script syntax
bash -n bin/playout_all.sh
```

## What Still Needs to be Done

### ❌ **Remaining Hardcoded Paths**

1. **Other Shell Scripts**
   - `bin/jack_start.sh` - Still has hardcoded paths
   - `bin/jack_stop.sh` - Still has hardcoded paths
   - `bin/capture_sdi_dante.sh` - Still has hardcoded paths
   - `bin/backup_prune.sh` - Still has hardcoded paths

2. **Python Server** (`server/status_http.py`)
   - Still uses hardcoded paths in some places
   - Could benefit from using the `config.py` module

3. **Deployment Scripts**
   - `scripts/deploy-to-prod.sh` - Still has hardcoded paths
   - `scripts/install-prod.sh` - Still has hardcoded paths

### 🔧 **Recommended Next Steps**

1. **Update remaining shell scripts** to use environment variables
2. **Integrate `config.py`** into the Python server
3. **Update deployment scripts** to use environment variables
4. **Create a migration script** to help users transition
5. **Add validation** to ensure all required environment variables are set

## Testing the Changes

### **Before Migration**
```bash
# Your current working setup
./bin/playout_all.sh lesson-1
```

### **After Migration**
```bash
# Set environment variables
export STATE_DIR=/opt/backup/state
export LOG_DIR=/opt/backup/logs
export LATEST_DIR=/opt/backup/latest
export PLAYLIST_DIR=/opt/backup/playlist
export OVERLAY_PNG=/Users/shidur/rerun.png

# Test the same command
./bin/playout_all.sh lesson-1
```

## Benefits of This Migration

1. **Public Repository Safety** - No more hardcoded personal paths
2. **Flexibility** - Easy to configure for different environments
3. **Maintainability** - Centralized configuration management
4. **Deployment** - Easy to deploy to different servers
5. **Documentation** - Clear setup instructions for new users

## Troubleshooting

### **Common Issues**

1. **"No such file or directory"** - Check if environment variables are set correctly
2. **"Permission denied"** - Verify directory permissions and ownership
3. **"Command not found"** - Check `GST_PATH` and `FFMPEG_PATH` variables

### **Debug Commands**

```bash
# Check current environment variables
env | grep -E "(STATE_DIR|LOG_DIR|LATEST_DIR|PLAYLIST_DIR)"

# Test configuration
python3 config.py

# Check shell script with debug
bash -x bin/playout_all.sh --help
```

## Contributing

When adding new features:

1. **Use environment variables** instead of hardcoded paths
2. **Add new variables** to `env.example` and `config.py`
3. **Update documentation** in `README.md`
4. **Test thoroughly** with different environment configurations

## Support

If you encounter issues during migration:

1. Check the `README.md` for setup instructions
2. Verify your environment variables are set correctly
3. Test the configuration with `python3 config.py`
4. Open an issue on GitHub with details about your setup
