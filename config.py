#!/usr/bin/env python3
"""
Configuration module for SVIVA Playt System
Loads environment variables and provides defaults
"""

import os
from pathlib import Path

# =============================================================================
# ENVIRONMENT VARIABLES WITH DEFAULTS
# =============================================================================

# Production deployment settings
PROD_HOST = os.environ.get("PROD_HOST", "user@your-server-ip")
PROD_DIR = os.environ.get("PROD_DIR", "/path/to/your/production/directory")

# System paths and directories
STATE_DIR = os.environ.get("STATE_DIR", "/opt/backup/state")
LOG_DIR = os.environ.get("LOG_DIR", "/opt/backup/logs")
BB_STATUS_FILE = os.environ.get("BB_STATUS_FILE", "/opt/backup/status.json")
BIN_DST = os.environ.get("BIN_DST", "/usr/local/bin")
LATEST_DIR = os.environ.get("LATEST_DIR", "/opt/backup/latest")
PLAYLIST_DIR = os.environ.get("PLAYLIST_DIR", "/opt/backup/playlist")

# Playt system configuration
OVERLAY_PNG = os.environ.get("OVERLAY_PNG", "/path/to/your/overlay.png")
GST_PATH = os.environ.get("GST_PATH", "/usr/local/bin/gst-launch-1.0")
FFMPEG_PATH = os.environ.get("FFMPEG_PATH", "/usr/local/bin/ffmpeg")

# Security settings
STATUS_HTTP_TOKEN = os.environ.get("STATUS_HTTP_TOKEN", "")

# Audio/Video settings
JACK_RATE = int(os.environ.get("JACK_RATE", "48000"))
JACK_BUFFER = int(os.environ.get("JACK_BUFFER", "256"))
JACK_NPERIODS = int(os.environ.get("JACK_NPERIODS", "3"))

VIDEO_WIDTH = int(os.environ.get("VIDEO_WIDTH", "1920"))
VIDEO_HEIGHT = int(os.environ.get("VIDEO_HEIGHT", "1080"))
VIDEO_FRAMERATE = int(os.environ.get("VIDEO_FRAMERATE", "25"))
VIDEO_FORMAT = os.environ.get("VIDEO_FORMAT", "UYVY")
VIDEO_INTERLACE = os.environ.get("VIDEO_INTERLACE", "true").lower() == "true"

# Network settings
HTTP_HOST = os.environ.get("HTTP_HOST", "127.0.0.1")
HTTP_PORT = int(os.environ.get("HTTP_PORT", "8787"))

# Development settings
DEV_MODE = os.environ.get("DEV_MODE", "false").lower() == "true"
LOG_LEVEL = os.environ.get("LOG_LEVEL", "info")

# =============================================================================
# DERIVED PATHS
# =============================================================================

# Ensure directories exist
def ensure_directories():
    """Create necessary directories if they don't exist"""
    Path(STATE_DIR).mkdir(parents=True, exist_ok=True)
    Path(LOG_DIR).mkdir(parents=True, exist_ok=True)
    Path(os.path.dirname(BB_STATUS_FILE)).mkdir(parents=True, exist_ok=True)

# =============================================================================
# VALIDATION
# =============================================================================

def validate_config():
    """Validate configuration values"""
    errors = []
    
    # Check if critical paths are writable
    try:
        Path(STATE_DIR).mkdir(parents=True, exist_ok=True)
        test_file = Path(STATE_DIR) / ".test"
        test_file.touch()
        test_file.unlink()
    except Exception as e:
        errors.append(f"STATE_DIR not writable: {STATE_DIR} - {e}")
    
    try:
        Path(LOG_DIR).mkdir(parents=True, exist_ok=True)
        test_file = Path(LOG_DIR) / ".test"
        test_file.touch()
        test_file.unlink()
    except Exception as e:
        errors.append(f"LOG_DIR not writable: {LOG_DIR} - {e}")
    
    # Check if binaries exist
    if not Path(GST_PATH).exists():
        errors.append(f"GStreamer not found: {GST_PATH}")
    
    if not Path(FFMPEG_PATH).exists():
        errors.append(f"FFmpeg not found: {FFMPEG_PATH}")
    
    return errors

# =============================================================================
# CONFIGURATION SUMMARY
# =============================================================================

def print_config():
    """Print current configuration for debugging"""
    print("=== SVIVA PLAYT SYSTEM CONFIGURATION ===")
    print(f"STATE_DIR: {STATE_DIR}")
    print(f"LOG_DIR: {LOG_DIR}")
    print(f"BB_STATUS_FILE: {BB_STATUS_FILE}")
    print(f"LATEST_DIR: {LATEST_DIR}")
    print(f"PLAYLIST_DIR: {PLAYLIST_DIR}")
    print(f"OVERLAY_PNG: {OVERLAY_PNG}")
    print(f"GST_PATH: {GST_PATH}")
    print(f"FFMPEG_PATH: {FFMPEG_PATH}")
    print(f"HTTP_HOST: {HTTP_HOST}")
    print(f"HTTP_PORT: {HTTP_PORT}")
    print(f"VIDEO: {VIDEO_WIDTH}x{VIDEO_HEIGHT}@{VIDEO_FRAMERATE}fps {VIDEO_FORMAT}")
    print(f"INTERLACED: {VIDEO_INTERLACE}")
    print(f"DEV_MODE: {DEV_MODE}")
    print(f"LOG_LEVEL: {LOG_LEVEL}")
    print("========================================")

if __name__ == "__main__":
    print_config()
    errors = validate_config()
    if errors:
        print("\nConfiguration errors:")
        for error in errors:
            print(f"  - {error}")
    else:
        print("\nConfiguration is valid!")
