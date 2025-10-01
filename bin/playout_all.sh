#!/bin/bash
set -euo pipefail

# =================== legacy bb-status (status.json) ===================
BB_STATUS_FILE="${BB_STATUS_FILE:-${STATE_DIR:-/opt/backup}/status.json}"
status_write () {
  local running="$1" mode="$2" label="$3" file="$4" pid="$5"
  local ts; ts="$(date +%s)"
  local dir; dir="$(dirname "$BB_STATUS_FILE")"
  mkdir -p "$dir"
  [[ -f "$BB_STATUS_FILE" ]] || echo '{"running":false}' > "$BB_STATUS_FILE"
  local tmp="$BB_STATUS_FILE.tmp.$$"
  jq -c \
    --arg MODE "$mode" --arg LABEL "$label" --arg FILE "$file" \
    --argjson PID "$pid" --argjson RUN "$running" --argjson TS "$ts" \
    '
      .running=$RUN | .mode=$MODE | .["label"]=$LABEL | .file=$FILE | .pid=$PID
      | (if $RUN then (.started_at=$TS|del(.ended_at)) else (.ended_at=$TS) end)
    ' "$BB_STATUS_FILE" > "$tmp" && mv "$tmp" "$BB_STATUS_FILE"
}
status_start () { status_write true  "playout" "$1" "$2" "$$"; }
status_stop  () { status_write false "playout" "$1" "$2" 0; }

# =================== state paths / dirs logs ===================
STATE_DIR="${STATE_DIR:-/opt/backup/state}"
LOG_DIR="${LOG_DIR:-/opt/backup/logs}"
PLAY_STATE="$STATE_DIR/playout.json"
PID_FILE="$STATE_DIR/playout.pid"
LOG="$LOG_DIR/playout_gst.log"
mkdir -p "$STATE_DIR" "$LOG_DIR" "$(dirname "$BB_STATUS_FILE")" "${LATEST_DIR:-/opt/backup/latest}"
if ! : >>"$LOG" 2>/dev/null; then
  echo "[warn] $LOG not writable; using /dev/null" >&2
  LOG="/dev/null"
fi

# =================== stale-state coercion ===================
/usr/bin/python3 - "$PLAY_STATE" <<'PY'
import json, os, sys, time, tempfile
p = sys.argv[1]
try:
    with open(p) as f: d = json.load(f)
except Exception:
    d = {}
pid = int(d.get("pid") or 0)
running = bool(d.get("running"))
if running and pid > 0:
    try:
        os.kill(pid, 0)
    except OSError:
        d["running"] = False
        d["pid"] = 0
        d["ended_at"] = int(time.time())
        d["mode"] = "playout"
        dirp = os.path.dirname(p); os.makedirs(dirp, exist_ok=True)
        fd, tmp = tempfile.mkstemp(dir=dirp, prefix=".playout.", text=True)
        with os.fdopen(fd, "w") as f: json.dump(d, f)
        os.replace(tmp, p)
PY

# =================== state writer (/v1/playout) ===================
play_state_write () {
  local run="$1" label="$2" file="$3" pid="$4"
  /usr/bin/python3 - "$PLAY_STATE" "$run" "$label" "$file" "$pid" <<'PY'
import sys, json, os, tempfile, time
path, running, label, file, pid = sys.argv[1], sys.argv[2]=='true', sys.argv[3], sys.argv[4], int(sys.argv[5])

loop_env = (os.environ.get("LOOP","0") or "0").strip().lower()
loop = loop_env not in ("0","false","no","off","")
overlay = os.environ.get("OVERLAY_PNG") or None
overlay_enabled = os.environ.get("OVERLAY_ENABLED", "1").strip().lower() not in ("0","false","no","off","")
url = os.environ.get("URL") or None
playlist = os.environ.get("PLAYLIST") or None
preroll = os.environ.get("PREROLL") or None


if overlay and not os.path.isfile(overlay):
    overlay = None

try:
  with open(path,'r') as f: data=json.load(f)
except Exception:
  data={}

data.update(dict(
    running=running,
    mode="playout",
    label=label,
    file=file,
    pid=(pid if running else 0),
    loop=loop,
    overlay_enabled=overlay_enabled,
    url=url,
    playlist=playlist,
    preroll=preroll,

))
if overlay is not None and overlay_enabled:
    data["overlay"] = overlay

t = int(time.time())
if running:
  data['started_at']=t; data.pop('ended_at', None)
else:
  data['ended_at']=t

d=os.path.dirname(path); os.makedirs(d, exist_ok=True)
fd,tmp=tempfile.mkstemp(dir=d, prefix='.playout.', text=True)
with os.fdopen(fd,'w') as f: json.dump(data, f)
os.replace(tmp, path)
PY
}

# =================== heartbeat ===================
HB_PID=""
play_hb_tick() {
  /usr/bin/python3 - "$PLAY_STATE" <<'PY'
import json, os, sys, time, tempfile
p = sys.argv[1]
try:
    with open(p) as f: d = json.load(f)
except Exception:
    d = {}
d["heartbeat"] = int(time.time())
d["mode"] = "playout"
dirp = os.path.dirname(p); os.makedirs(dirp, exist_ok=True)
fd, tmp = tempfile.mkstemp(dir=dirp, prefix=".playout.", text=True)
with os.fdopen(fd, "w") as f: json.dump(d, f)
os.replace(tmp, p)
PY
}
start_playout_heartbeat() {
  stop_playout_heartbeat
  (
    while kill -0 "$GST_PID" 2>/dev/null; do
      play_hb_tick
      sleep 2
    done
  ) &
  HB_PID=$!
}
stop_playout_heartbeat() {
  if [ -n "${HB_PID:-}" ] && kill -0 "$HB_PID" 2>/dev/null; then
    kill -TERM "$HB_PID" 2>/dev/null || true
    wait "$HB_PID" 2>/dev/null || true
  fi
  HB_PID=""
}

# =================== guard ===================
preflight_pid_guard() {
  if [ -f "$PID_FILE" ]; then
    local oldpid
    oldpid="$(cat "$PID_FILE" 2>/dev/null || echo 0)"
    if [ "${oldpid:-0}" -gt 0 ] && kill -0 "$oldpid" 2>/dev/null; then
      echo "playout already running (pid $oldpid)" >&2
      exit 1
    else
      rm -f "$PID_FILE"
    fi
  fi
}

# =================== args ===================
LOOP=0
OVERLAY_PNG="${OVERLAY_PNG:-/path/to/your/overlay.png}"
OVERLAY_ENABLED=1
URL=""
PLAYLIST=""
PREROLL=""
TEST_SECONDS=0
IMAGE_PATH=""
REPEAT_CONCAT=0


usage() {
  echo "usage: $0 [--loop|-L] [--overlay /path.png] [--no-overlay] [--url URL] [--preroll /path.mp4] [--test-seconds SECONDS] [--image /path.png] [--repeat-concat N] {/path/to/file.mp4 | lesson-1..lesson-4 | URL}" >&2
  exit 1
}
while [[ $# -gt 0 ]]; do
  case "$1" in
    --loop|-L) LOOP=1; shift;;
    --overlay) OVERLAY_PNG="$2"; OVERLAY_ENABLED=1; shift 2;;
    --no-overlay) OVERLAY_ENABLED=0; shift;;
    --url) URL="$2"; shift 2;;
    --playlist) PLAYLIST="$2"; shift 2;;
    --preroll) PREROLL="$2"; shift 2;;
    --test-seconds) TEST_SECONDS="${2:-0}"; shift 2;;
    --image) IMAGE_PATH="$2"; shift 2;;
    --repeat-concat) REPEAT_CONCAT="${2:-0}"; shift 2;;

    --label) LABEL="$2"; shift 2;;
    -h|--help) usage;;
    --) shift; break;;
    -*) echo "unknown option: $1" >&2; usage;;
    *) break;;
  esac
done
# For playlist prepare/play/test requests, we don't need a file argument
if [[ "$PLAYLIST" == "play" ]]; then
  RAW="play"
elif [[ "$PLAYLIST" == "test" ]]; then
  RAW="play-test"
elif [[ -n "$IMAGE_PATH" ]]; then
  # For image requests, we don't need a file argument
  RAW="image"
else
  : "${1:?missing file, lesson-N, or URL; try --help}"
  RAW="$1"
fi

# export flags so Python can read them
export LOOP OVERLAY_PNG OVERLAY_ENABLED URL PLAYLIST PREROLL TEST_SECONDS IMAGE_PATH REPEAT_CONCAT
export STATE_DIR LOG_DIR LATEST_DIR PLAYLIST_DIR GST_PATH FFMPEG_PATH

# normalize input
case "$RAW" in *$'\r') RAW="${RAW%$'\r'}";; esac
while :; do case "$RAW" in *" ") RAW="${RAW% }";; *$'\t') RAW="${RAW%$'\t'}";; *\}) RAW="${RAW%\}}";; *\]) RAW="${RAW%\]}";; *\") RAW="${RAW%\"}";; *\') RAW="${RAW%\'}";; *) break;; esac; done
while :; do case "$RAW" in " "*) RAW="${RAW# }";; $'\t'*) RAW="${RAW#$'\t'}";; \{*) RAW="${RAW#\{}";; [[]*) RAW="${RAW#[[]}" ;; \"*) RAW="${RAW#\"}";; \'*) RAW="${RAW#\'}";; *) break;; esac; done

case "$RAW" in
  prepare) FILE="prepare"; URL="";;  # Prepare request case
  play) FILE="play"; URL="";;  # Play request case
  play-test) FILE="play-test"; URL="";;  # Test request case
  image) FILE="image:$IMAGE_PATH"; URL="";;  # Image request case
  lesson-[1-4]) FILE="${LATEST_DIR:-/opt/backup/latest}/$RAW.mp4" ;;
  http://*|https://*) FILE="$RAW"; URL="$RAW" ;;  # Web URL case
  *)            FILE="$RAW" ;;
esac



# Handle playlist input
if [[ -n "$PLAYLIST" ]]; then
  :
fi

# Check if file exists (skip for web URLs, prepare, play, play-test, and image requests)
if [[ -n "$URL" ]]; then
  echo "[info] using web URL: $URL" >&2
elif [[ "$FILE" == "play" ]]; then
  echo "[info] play request - no file check needed" >&2
elif [[ "$FILE" == "play-test" ]]; then
  echo "[info] play-test request - no file check needed" >&2
elif [[ "$FILE" == image:* ]]; then
  echo "[info] image request - no file check needed" >&2
elif [[ ! -f "$FILE" ]]; then
  echo "file not found: $FILE" >&2
  exit 2
fi
LABEL="${LABEL:-$(basename "${FILE%.*}")}"

# =================== tools ===================
# Try to find gst-launch-1.0 in various locations
GST="${GST:-${GST_PATH:-/opt/homebrew/bin/gst-launch-1.0}}"
[ -x "$GST" ] || GST="${GST_PATH:-/usr/local/bin/gst-launch-1.0}"
[ -x "$GST" ] || GST="${GST_PATH:-/opt/homebrew/bin/gst-launch-1.0}"

# If still not found, try to find it in PATH
if [ ! -x "$GST" ]; then
  GST=$(command -v gst-launch-1.0 2>/dev/null || echo "")
fi

# Final check
[ -x "$GST" ] || { echo "gst-launch-1.0 not found in standard locations or PATH" >&2; exit 127; }

echo "[info] using GStreamer: $GST" >&2

# =================== cleanup/trap ===================
FIFO=""
LOGGER_PID=""
cleanup() {
  local rc=${GST_RC:-$?}
  printf '[%s] [playout] STOP label=%s file=%s rc=%s\n' "$(date '+%F %T')" "$LABEL" "$FILE" "${rc:-0}" >>"$LOG"

  if [ -n "${GST_PID:-}" ] && kill -0 "$GST_PID" 2>/dev/null; then
    kill -TERM "$GST_PID" 2>/dev/null || true
    wait "$GST_PID" 2>/dev/null || true
  fi
  if [ -n "${LOGGER_PID:-}" ] && kill -0 "$LOGGER_PID" 2>/dev/null; then
    kill -TERM "$LOGGER_PID" 2>/dev/null || true
    wait "$LOGGER_PID" 2>/dev/null || true
  fi
  [ -n "${FIFO:-}" ] && rm -f "$FIFO" || true

  stop_playout_heartbeat
  rm -f "$PID_FILE"
  play_state_write false "$LABEL" "$FILE" 0
  status_stop "$LABEL" "$FILE"
  case "${rc:-0}" in 255|143) rc=0;; esac
  exit $rc
}
trap cleanup EXIT INT TERM

# =================== playlist preparation function ===================
prepare_playlist() {
  local playlist_urls=("$@")
  local total_urls=${#playlist_urls[@]}
  local playlist_dir="${PLAYLIST_DIR:-/opt/backup/playlist}"
  local meta_file="$playlist_dir/playlist_meta.json"
  
  # locate ffmpeg (optional, used for last-frame capture)
  local FFMPEG="${FFMPEG:-ffmpeg}"
  if ! command -v "$FFMPEG" >/dev/null 2>&1; then
    if [ -x "${FFMPEG_PATH:-/opt/homebrew/bin/ffmpeg}" ]; then
      FFMPEG="${FFMPEG_PATH:-/opt/homebrew/bin/ffmpeg}"
    elif [ -x "${FFMPEG_PATH:-/usr/local/bin/ffmpeg}" ]; then
      FFMPEG="${FFMPEG_PATH:-/usr/local/bin/ffmpeg}"
    fi
  fi
  # locate ffprobe (optional, used for duration)
  local FFPROBE="${FFPROBE:-ffprobe}"
  if ! command -v "$FFPROBE" >/dev/null 2>&1; then
    if [ -x "${FFPROBE_PATH:-/opt/homebrew/bin/ffprobe}" ]; then
      FFPROBE="${FFPROBE_PATH:-/opt/homebrew/bin/ffprobe}"
    elif [ -x "${FFPROBE_PATH:-/usr/local/bin/ffprobe}" ]; then
      FFPROBE="${FFPROBE_PATH:-/usr/local/bin/ffprobe}"
    fi
  fi
  
  echo "[info] preparing playlist with $total_urls URLs" >&2
  echo "[info] using fixed folder: $playlist_dir" >&2
  
  # Create playlist directory if it doesn't exist
  mkdir -p "$playlist_dir"
  
  # Clean up previous files
  echo "[info] cleaning up previous playlist files..." >&2
  rm -f "$playlist_dir"/*
  # Recreate empty metadata file
  echo '{}' > "$meta_file"
  
  # Download all URLs to the fixed folder
  local downloaded_files=()
  for i in "${!playlist_urls[@]}"; do
    local url="${playlist_urls[$i]}"
    local url_number=$((i + 1))
    local filename="url_${url_number}.mp4"
    local filepath="$playlist_dir/$filename"
    
    echo "[info] downloading URL $url_number/$total_urls: $url" >&2
    echo "[info] saving to: $filepath" >&2
    
    # Download the file
    if curl -L -o "$filepath" "$url"; then
      echo "[info] download successful: $filepath" >&2
      downloaded_files+=("$filepath")
    else
      echo "[error] download failed for: $url" >&2
      continue
    fi
  done
  
  # Check if we have any downloaded files
  if [[ ${#downloaded_files[@]} -eq 0 ]]; then
    echo "[error] no files downloaded successfully" >&2
    return 1
  fi
  
  echo "[info] playlist preparation completed: ${#downloaded_files[@]} files ready in $playlist_dir" >&2
  
  # List all downloaded files
  echo "[info] downloaded files:" >&2
  for file in "${downloaded_files[@]}"; do
    echo "[info]   - $file" >&2
  done
  
  return 0
}

# =================== playlist playback function ===================
play_playlist() {
  local playlist_dir="${PLAYLIST_DIR:-/opt/backup/playlist}"
  local fps=25
  local last_png_secs=5
  local v_frames=$((fps * last_png_secs))
  local a_numbuf=$((100 * last_png_secs))

  echo "[info] starting playlist playback from: $playlist_dir" >&2
  echo "[debug] current working directory: $(pwd)" >&2
  echo "[debug] playlist directory contents:" >&2
  ls -la "$playlist_dir" >&2

  if [[ ! -d "$playlist_dir" ]]; then
    echo "[error] playlist directory not found: $playlist_dir" >&2
    return 1
  fi

  local playlist_files=()
  while IFS= read -r -d '' file; do
    playlist_files+=("$file")
  done < <(find "$playlist_dir" -name "*.mp4" -type f -print0 | sort -z)

  if [[ ${#playlist_files[@]} -eq 0 ]]; then
    echo "[error] no .mp4 files found in playlist directory: $playlist_dir" >&2
    return 1
  fi

  echo "[info] found ${#playlist_files[@]} files to play:" >&2
  for file in "${playlist_files[@]}"; do echo "[info]   - $file" >&2; done

  echo "[info] building unified concat pipeline for playlist..." >&2
  local pipeline="concat name=vcat ! videoconvert ! videoscale ! videorate ! video/x-raw,format=UYVY,width=1920,height=1080,framerate=${fps}/1 ! interlace field-pattern=2:2 top-field-first=true ! decklinkvideosink mode=1080i50"
  pipeline+=" concat name=acat ! tee name=audio_tee"
  pipeline+=" audio_tee. ! queue ! jackaudiosink client-name=gst-heb-L port-pattern=20 sync=false"
  pipeline+=" audio_tee. ! queue ! jackaudiosink client-name=gst-heb-R port-pattern=21 sync=false"
  pipeline+=" audio_tee. ! queue ! jackaudiosink client-name=gst-en    port-pattern=29 sync=false"
  pipeline+=" audio_tee. ! queue ! jackaudiosink client-name=gst-ru    port-pattern=22 sync=false"
  pipeline+=" audio_tee. ! queue ! jackaudiosink client-name=gst-es    port-pattern=30 sync=false"
  pipeline+=" audio_tee. ! queue ! jackaudiosink client-name=gst-fr    port-pattern=1  sync=false"
  pipeline+=" audio_tee. ! queue ! jackaudiosink client-name=gst-it    port-pattern=2  sync=false"
  pipeline+=" audio_tee. ! queue ! jackaudiosink client-name=gst-de    port-pattern=3  sync=false"
  pipeline+=" audio_tee. ! queue ! jackaudiosink client-name=gst-tr    port-pattern=4  sync=false"
  pipeline+=" audio_tee. ! queue ! jackaudiosink client-name=gst-pt    port-pattern=5  sync=false"
  pipeline+=" audio_tee. ! queue ! jackaudiosink client-name=gst-bg    port-pattern=6  sync=false"
  pipeline+=" audio_tee. ! queue ! jackaudiosink client-name=gst-ka    port-pattern=7  sync=false"
  pipeline+=" audio_tee. ! queue ! jackaudiosink client-name=gst-ro    port-pattern=8  sync=false"
  pipeline+=" audio_tee. ! queue ! jackaudiosink client-name=gst-hu    port-pattern=9  sync=false"
  pipeline+=" audio_tee. ! queue ! jackaudiosink client-name=gst-sv    port-pattern=10 sync=false"
  pipeline+=" audio_tee. ! queue ! jackaudiosink client-name=gst-lt    port-pattern=11 sync=false"
  pipeline+=" audio_tee. ! queue ! jackaudiosink client-name=gst-hr    port-pattern=12 sync=false"
  pipeline+=" audio_tee. ! queue ! jackaudiosink client-name=gst-ja    port-pattern=13 sync=false"
  pipeline+=" audio_tee. ! queue ! jackaudiosink client-name=gst-sl    port-pattern=14 sync=false"
  pipeline+=" audio_tee. ! queue ! jackaudiosink client-name=gst-pl    port-pattern=15 sync=false"
  pipeline+=" audio_tee. ! queue ! jackaudiosink client-name=gst-no    port-pattern=16 sync=false"
  pipeline+=" audio_tee. ! queue ! jackaudiosink client-name=gst-lv    port-pattern=17 sync=false"
  pipeline+=" audio_tee. ! queue ! jackaudiosink client-name=gst-uk    port-pattern=18 sync=false"
  pipeline+=" audio_tee. ! queue ! jackaudiosink client-name=gst-nl    port-pattern=19 sync=false"
  pipeline+=" audio_tee. ! queue ! jackaudiosink client-name=gst-zh    port-pattern=23 sync=false"
  pipeline+=" audio_tee. ! queue ! jackaudiosink client-name=gst-am    port-pattern=24 sync=false"
  pipeline+=" audio_tee. ! queue ! jackaudiosink client-name=gst-hi    port-pattern=25 sync=false"
  pipeline+=" audio_tee. ! queue ! jackaudiosink client-name=gst-fa    port-pattern=26 sync=false"
  pipeline+=" audio_tee. ! queue ! jackaudiosink client-name=gst-ar    port-pattern=27 sync=false"
  pipeline+=" audio_tee. ! queue ! jackaudiosink client-name=gst-id    port-pattern=28 sync=false"
  pipeline+=" audio_tee. ! queue ! jackaudiosink client-name=gst-hy    port-pattern=32 sync=false"
  pipeline+=" audio_tee. ! queue ! jackaudiosink client-name=gst-da    port-pattern=33 sync=false"
  pipeline+=" audio_tee. ! queue ! jackaudiosink client-name=gst-et    port-pattern=34 sync=false"
  pipeline+=" audio_tee. ! queue ! jackaudiosink client-name=gst-el    port-pattern=35 sync=false"
  pipeline+=" audio_tee. ! queue ! jackaudiosink client-name=gst-tl    port-pattern=36 sync=false"
  pipeline+=" audio_tee. ! queue ! jackaudiosink client-name=gst-az    port-pattern=37 sync=false"

  local sink_index=0
  local i=0
  for current_file in "${playlist_files[@]}"; do
    i=$((i+1))
    echo "[info] append preroll/file/png segments for item $i" >&2
    if [[ -n "$PREROLL" ]] && [[ -f "$PREROLL" ]]; then
      pipeline+=" filesrc location=\"$PREROLL\" ! qtdemux name=p$i"
      pipeline+=" p$i.video_0 ! queue ! h264parse ! avdec_h264 ! videoconvert ! videoscale ! videorate ! video/x-raw,format=UYVY,width=1920,height=1080,framerate=${fps}/1 ! vcat.sink_${sink_index}"; sink_index=$((sink_index+1))
      pipeline+=" p$i.audio_0 ! queue ! aacparse ! avdec_aac ! audioconvert ! audioresample ! audio/x-raw,rate=48000,channels=2,format=F32LE,layout=interleaved ! acat.sink_${sink_index-1}"
    fi
    pipeline+=" filesrc location=\"$current_file\" ! qtdemux name=u$i"
    pipeline+=" u$i.video_0 ! queue ! h264parse ! avdec_h264 ! videoconvert ! videoscale ! videorate ! video/x-raw,format=UYVY,width=1920,height=1080,framerate=${fps}/1 ! vcat.sink_${sink_index}"; sink_index=$((sink_index+1))
    pipeline+=" u$i.audio_0 ! queue ! aacparse ! avdec_aac ! audioconvert ! audioresample ! audio/x-raw,rate=48000,channels=2,format=F32LE,layout=interleaved ! acat.sink_${sink_index-1}"
    local png_path="$playlist_dir/last_frame-$i.png"
    if [[ -f "$png_path" ]]; then
      pipeline+=" filesrc location=\"$png_path\" ! decodebin ! queue ! imagefreeze ! videoscale ! videoconvert ! videorate ! video/x-raw,format=UYVY,width=1920,height=1080,framerate=${fps}/1 ! identity eos-after=${v_frames} ! vcat.sink_${sink_index}"; sink_index=$((sink_index+1))
      pipeline+=" audiotestsrc wave=silence samplesperbuffer=480 num-buffers=${a_numbuf} ! audioconvert ! audioresample ! audio/x-raw,rate=48000,channels=2,format=F32LE,layout=interleaved ! acat.sink_${sink_index-1}"
    else
      echo "[warn] missing PNG for item $i: $png_path" >&2
    fi
  done

  echo "[debug] launching unified playlist gst pipeline" >&2
  local FIFO="$STATE_DIR/.gstlog.$$.$RANDOM"; rm -f "$FIFO"; mkfifo "$FIFO"
  (
    while IFS= read -r line; do printf '[%s] [gst] %s\n' "$(date '+%F %T')" "$line" >>"$LOG"; done <"$FIFO"
  ) &
  local LOGGER_PID=$!
  "$GST" -e -vvv ${pipeline} >"$FIFO" 2>&1 &
  GST_PID=$!
  play_state_write true "$LABEL" "play" "$GST_PID"; status_write true "playout" "$LABEL" "play" "$GST_PID"; start_playout_heartbeat
  wait "$GST_PID"; GST_RC=$?
  stop_playout_heartbeat
  kill -TERM "$LOGGER_PID" 2>/dev/null || true; wait "$LOGGER_PID" 2>/dev/null || true
  rm -f "$FIFO"
  echo "[info] unified playlist pipeline finished rc=$GST_RC" >&2

  # --- implicit fallback: after successful playlist end, resume concat repeat
  if [ "${GST_RC:-1}" -eq 0 ]; then
    echo "[info] playlist finished successfully; resuming concat repeat after 2s" >&2
    sleep 2
    # Determine fallback label/repeats
    local fb_label="${REPEAT_CONCAT_FALLBACK_LABEL:-lesson-1}"
    local fb_repeats="${REPEAT_CONCAT_FALLBACK_REPEATS:-6}"

    # Read last lesson overlay settings if available; otherwise fall back to playout.json overlay
    py_out="$(/usr/bin/python3 - <<'PY'
import json, os
state_dir = os.environ.get('STATE_DIR','/opt/backup/state')
last_path = os.path.join(state_dir,'last_lesson.json')
play_path = os.path.join(state_dir,'playout.json')
overlay = ''
enabled = True
try:
    with open(last_path) as f:
        d = json.load(f)
        overlay = str(d.get('overlay') or '')
        enabled = bool(d.get('overlay_enabled', True))
except Exception:
    try:
        with open(play_path) as f:
            p = json.load(f)
            overlay = str(p.get('overlay') or '')
            enabled = bool(overlay)
    except Exception:
        pass
print((overlay or '').strip())
print('1' if enabled else '0')
PY
    )"
    last_overlay_path="$(printf '%s' "$py_out" | sed -n '1p')"
    last_overlay_enabled="$(printf '%s' "$py_out" | sed -n '2p')"

    args=( "--repeat-concat" "$fb_repeats" "--label" "$fb_label" "$fb_label" )
    if [ "${last_overlay_enabled:-1}" = "1" ] && [ -n "${last_overlay_path:-}" ] && [ -f "$last_overlay_path" ]; then
      args=( "--overlay" "$last_overlay_path" "${args[@]}" )
    else
      if [ -n "${OVERLAY_PNG:-}" ] && [ -f "$OVERLAY_PNG" ]; then
        args=( "--overlay" "$OVERLAY_PNG" "${args[@]}" )
      elif [ -f "/Users/shidur/rerun.png" ]; then
        args=( "--overlay" "/Users/shidur/rerun.png" "${args[@]}" )
      else
        args=( "--no-overlay" "${args[@]}" )
      fi
    fi

    echo "[info] launching fallback concat repeat: label=$fb_label repeats=$fb_repeats" >&2
    rm -f "$PID_FILE" 2>/dev/null || true
    exec "$0" "${args[@]}"
  fi
}

# =================== playlist test (first 5s per file, no preroll/png) ===================
play_playlist_test() {
  local playlist_dir="${PLAYLIST_DIR:-/opt/backup/playlist}"
  local fps=25
  local V=$((fps * 10))

  echo "[info] starting playlist TEST (5s per file) from: $playlist_dir" >&2
  if [[ ! -d "$playlist_dir" ]]; then echo "[error] playlist dir missing" >&2; return 1; fi

  local playlist_files=()
  while IFS= read -r -d '' file; do playlist_files+=("$file"); done < <(find "$playlist_dir" -name "*.mp4" -type f -print0 | sort -z)
  if [[ ${#playlist_files[@]} -eq 0 ]]; then echo "[error] no mp4 files" >&2; return 1; fi

  echo "[info] building TEST concat pipeline (DeckLink 1080i50, 10s per file)" >&2
  local pipeline="concat name=vcat ! videoconvert ! videoscale ! videorate ! video/x-raw,format=UYVY,width=1920,height=1080,framerate=${fps}/1 ! interlace field-pattern=2:2 top-field-first=true ! decklinkvideosink mode=1080i50"
  local idx=0
  for f in "${playlist_files[@]}"; do
    idx=$((idx+1))
    pipeline+=" filesrc location=\"$f\" ! qtdemux name=t$idx"
    pipeline+=" t$idx.video_0 ! queue ! h264parse ! avdec_h264 ! videoconvert ! videoscale ! videorate ! video/x-raw,format=UYVY,width=1920,height=1080,framerate=${fps}/1 ! identity eos-after=${V} ! vcat.sink_$((idx-1))"
  done

  echo "[debug] launching TEST pipeline" >&2
  local FIFO="$STATE_DIR/.gstlog.$$.$RANDOM"; rm -f "$FIFO"; mkfifo "$FIFO"
  (
    while IFS= read -r line; do printf '[%s] [gst] %s\n' "$(date '+%F %T')" "$line" >>"$LOG"; done <"$FIFO"
  ) &
  local LOGGER_PID=$!
  "$GST" -e -vvv ${pipeline} >"$FIFO" 2>&1 &
  GST_PID=$!
  play_state_write true "$LABEL" "play-test" "$GST_PID"; status_write true "playout" "$LABEL" "play-test" "$GST_PID"; start_playout_heartbeat
  wait "$GST_PID"; GST_RC=$?
  stop_playout_heartbeat
  play_state_write false "$LABEL" "play-test" 0; status_stop "$LABEL" "play-test"
  kill -TERM "$LOGGER_PID" 2>/dev/null || true; wait "$LOGGER_PID" 2>/dev/null || true
  rm -f "$FIFO"
  echo "[info] TEST pipeline finished rc=$GST_RC" >&2
}

# =================== lesson concat repeat (with 1s lead-in, 36ch Dante) ===================
play_lesson_repeat_concat() {
  local repeats="$1"
  local src_file="$2"
  local fps=25
  local lead_v=$((fps))
  local lead_a_buf=100
  # use the source file directly (no pre-trim)

  echo "[info] building concat repeat pipeline (repeats=$repeats)" >&2
  local overlay_stage=""
  if [ "${OVERLAY_ENABLED:-1}" -eq 1 ] && [ -n "${OVERLAY_PNG:-}" ] && [ -f "$OVERLAY_PNG" ]; then
    echo "[info] overlay enabled for concat: $OVERLAY_PNG" >&2
    overlay_stage=" ! gdkpixbufoverlay location=\"$OVERLAY_PNG\" alpha=1.0 offset-x=0 offset-y=0"
  else
    echo "[info] overlay disabled for concat" >&2
  fi
  local pipeline="concat name=vcat ! videoconvert${overlay_stage} ! videoscale ! videorate ! video/x-raw,format=UYVY,width=1920,height=1080,framerate=${fps}/1 ! interlace field-pattern=2:2 top-field-first=true ! decklinkvideosink mode=1080i50 sync=true"
  # Per-lane audio concats mapped to JACK (36 total lanes)
  pipeline+=" concat name=ahebL ! queue ! jackaudiosink client-name=gst-heb-L port-pattern=20 sync=true"
  pipeline+=" concat name=ahebR ! queue ! jackaudiosink client-name=gst-heb-R port-pattern=21 sync=true"
  pipeline+=" concat name=a1 ! queue ! jackaudiosink client-name=gst-en  port-pattern=29 sync=true"
  pipeline+=" concat name=a2 ! queue ! jackaudiosink client-name=gst-ru  port-pattern=22 sync=true"
  pipeline+=" concat name=a3 ! queue ! jackaudiosink client-name=gst-es  port-pattern=30 sync=true"
  pipeline+=" concat name=a4 ! queue ! jackaudiosink client-name=gst-fr  port-pattern=1  sync=true"
  pipeline+=" concat name=a5 ! queue ! jackaudiosink client-name=gst-it  port-pattern=2  sync=true"
  pipeline+=" concat name=a6 ! queue ! jackaudiosink client-name=gst-de  port-pattern=3  sync=true"
  pipeline+=" concat name=a7 ! queue ! jackaudiosink client-name=gst-tr  port-pattern=4  sync=true"
  pipeline+=" concat name=a8 ! queue ! jackaudiosink client-name=gst-pt  port-pattern=5  sync=true"
  pipeline+=" concat name=a9 ! queue ! jackaudiosink client-name=gst-bg  port-pattern=6  sync=true"
  pipeline+=" concat name=a10 ! queue ! jackaudiosink client-name=gst-ka port-pattern=7  sync=true"
  pipeline+=" concat name=a11 ! queue ! jackaudiosink client-name=gst-ro port-pattern=8  sync=true"
  pipeline+=" concat name=a12 ! queue ! jackaudiosink client-name=gst-hu port-pattern=9  sync=true"
  pipeline+=" concat name=a13 ! queue ! jackaudiosink client-name=gst-sv port-pattern=10 sync=true"
  pipeline+=" concat name=a14 ! queue ! jackaudiosink client-name=gst-lt port-pattern=11 sync=true"
  pipeline+=" concat name=a15 ! queue ! jackaudiosink client-name=gst-hr port-pattern=12 sync=true"
  pipeline+=" concat name=a16 ! queue ! jackaudiosink client-name=gst-ja port-pattern=13 sync=true"
  pipeline+=" concat name=a17 ! queue ! jackaudiosink client-name=gst-sl port-pattern=14 sync=true"
  pipeline+=" concat name=a18 ! queue ! jackaudiosink client-name=gst-pl port-pattern=15 sync=true"
  pipeline+=" concat name=a19 ! queue ! jackaudiosink client-name=gst-no port-pattern=16 sync=true"
  pipeline+=" concat name=a20 ! queue ! jackaudiosink client-name=gst-lv port-pattern=17 sync=true"
  pipeline+=" concat name=a21 ! queue ! jackaudiosink client-name=gst-uk port-pattern=18 sync=true"
  pipeline+=" concat name=a22 ! queue ! jackaudiosink client-name=gst-nl port-pattern=19 sync=true"
  pipeline+=" concat name=a23 ! queue ! jackaudiosink client-name=gst-zh port-pattern=23 sync=true"
  pipeline+=" concat name=a24 ! queue ! jackaudiosink client-name=gst-am port-pattern=24 sync=true"
  pipeline+=" concat name=a25 ! queue ! jackaudiosink client-name=gst-hi port-pattern=25 sync=true"
  pipeline+=" concat name=a26 ! queue ! jackaudiosink client-name=gst-fa port-pattern=26 sync=true"
  pipeline+=" concat name=a27 ! queue ! jackaudiosink client-name=gst-ar port-pattern=27 sync=true"
  pipeline+=" concat name=a28 ! queue ! jackaudiosink client-name=gst-id port-pattern=28 sync=true"
  pipeline+=" concat name=a29 ! queue ! jackaudiosink client-name=gst-hy port-pattern=32 sync=true"
  pipeline+=" concat name=a30 ! queue ! jackaudiosink client-name=gst-da port-pattern=33 sync=true"
  pipeline+=" concat name=a31 ! queue ! jackaudiosink client-name=gst-et port-pattern=34 sync=true"
  pipeline+=" concat name=a32 ! queue ! jackaudiosink client-name=gst-el port-pattern=35 sync=true"
  pipeline+=" concat name=a33 ! queue ! jackaudiosink client-name=gst-tl port-pattern=36 sync=true"
  pipeline+=" concat name=a34 ! queue ! jackaudiosink client-name=gst-az port-pattern=37 sync=true"
  pipeline+=" concat name=aextra ! fakesink sync=false"

  # (Lead-in removed per request)

  # Append N repetitions
  local idx=0
  while [ $idx -lt $repeats ]; do
    local n=$((idx+1))
    pipeline+=" filesrc location=\"$src_file\" ! qtdemux name=d$n"
    # video
    pipeline+=" d$n.video_0 ! queue ! h264parse ! avdec_h264 ! videoconvert ! videoscale ! videorate ! video/x-raw,format=UYVY,width=1920,height=1080,framerate=${fps}/1 ! vcat.sink_$((n-1))"
    # heb L/R from audio_0
    pipeline+=" d$n.audio_0 ! queue ! aacparse ! avdec_aac ! audioconvert ! audioresample ! audio/x-raw,rate=48000,channels=2,format=F32LE,layout=interleaved ! deinterleave name=heb$n"
    pipeline+=" heb$n.src_0 ! queue ! ahebL.sink_$((n-1))"
    pipeline+=" heb$n.src_1 ! queue ! ahebR.sink_$((n-1))"
    # languages 1..34
    local a=1
    while [ $a -le 34 ]; do
      pipeline+=" d$n.audio_$a ! queue ! aacparse ! avdec_aac ! audioconvert ! audioresample ! audio/x-raw,rate=48000,channels=1,format=F32LE ! a$a.sink_$((n-1))"
      a=$((a+1))
    done
    # extra track 35 (optional)
    pipeline+=" d$n.audio_35 ! queue ! aacparse ! avdec_aac ! audioconvert ! audioresample ! audio/x-raw,rate=48000,channels=1,format=F32LE ! aextra.sink_$((n-1))"
    idx=$((idx+1))
  done

  # Final tail: 2s black and silence to ensure the last frame is black
  local tail_sec=2
  local tail_v=$((fps * tail_sec))
  local tail_a_buf=$((100 * tail_sec))
  local tail_idx=$((repeats))
  pipeline+=" videotestsrc is-live=true do-timestamp=true pattern=black ! videoconvert ! videoscale ! videorate ! video/x-raw,format=UYVY,width=1920,height=1080,framerate=${fps}/1 ! identity eos-after=${tail_v} ! vcat.sink_${tail_idx}"
  for lane in ahebL ahebR a1 a2 a3 a4 a5 a6 a7 a8 a9 a10 a11 a12 a13 a14 a15 a16 a17 a18 a19 a20 a21 a22 a23 a24 a25 a26 a27 a28 a29 a30 a31 a32 a33 a34 aextra; do
    pipeline+=" audiotestsrc is-live=true do-timestamp=true wave=silence samplesperbuffer=480 num-buffers=${tail_a_buf} ! audioconvert ! audioresample ! audio/x-raw,rate=48000,channels=1,format=F32LE ! ${lane}.sink_${tail_idx}"
  done

  echo "[debug] launching concat repeat pipeline" >&2
  local FIFO="$STATE_DIR/.gstlog.$$.$RANDOM"; rm -f "$FIFO"; mkfifo "$FIFO"
  (
    while IFS= read -r line; do printf '[%s] [gst] %s\n' "$(date '+%F %T')" "$line" >>"$LOG"; done <"$FIFO"
  ) &
  local LOGGER_PID=$!
  "$GST" -e -vvv ${pipeline} >"$FIFO" 2>&1 &
  GST_PID=$!
  play_state_write true "$LABEL" "concat-repeat" "$GST_PID"; status_write true "playout" "$LABEL" "concat-repeat" "$GST_PID"; start_playout_heartbeat
  wait "$GST_PID"; GST_RC=$?
  stop_playout_heartbeat
  play_state_write false "$LABEL" "concat-repeat" 0; status_stop "$LABEL" "concat-repeat"
  kill -TERM "$LOGGER_PID" 2>/dev/null || true; wait "$LOGGER_PID" 2>/dev/null || true
  rm -f "$FIFO"
  echo "[info] concat repeat pipeline finished rc=$GST_RC" >&2
}

# =================== local file playback function ===================
play_local_file() {
  local file="$1"
  local file_label="$2"
  
  echo "[info] playing local file: $file (label: $file_label)" >&2
  
  # Set up GStreamer pipeline for local file
  local FIFO="$STATE_DIR/.gstlog.$$.$RANDOM"
  rm -f "$FIFO"; mkfifo "$FIFO"
  
  (
    while IFS= read -r line; do
      printf '[%s] [gst] %s\n' "$(date '+%F %T')" "$line" >>"$LOG"
    done <"$FIFO"
  ) &
  local LOGGER_PID=$!
  
  # Launch GStreamer for local file; support test-seconds trimming
  if [ "${TEST_SECONDS:-0}" -gt 0 ]; then
    local V=$((25 * TEST_SECONDS))
    local ABUF=$((100 * TEST_SECONDS))
    "$GST" -e -vvv \
      filesrc location="$file" ! qtdemux name=d \
      d.video_0 ! queue ! h264parse ! decodebin ! videoconvert ! videoscale ! videorate \
        ! video/x-raw,format=UYVY,width=1920,height=1080,framerate=25/1 \
        ! identity eos-after=$V \
        ! interlace field-pattern=2:2 top-field-first=true \
        ! decklinkvideosink mode=1080i50 \
      d.audio_0 ! queue ! aacparse ! avdec_aac ! audioconvert ! audioresample \
        ! audio/x-raw,rate=48000,channels=2,format=F32LE,layout=interleaved ! identity eos-after=$ABUF \
        ! deinterleave name=heb \
        heb.src_0 ! queue ! jackaudiosink client-name=gst-heb-L port-pattern=20 sync=false \
        heb.src_1 ! queue ! jackaudiosink client-name=gst-heb-R port-pattern=21 sync=false \
      >"$FIFO" 2>&1 &
  else
    "$GST" -e -vvv \
      filesrc location="$file" ! qtdemux name=d \
      d.video_0 ! queue ! h264parse ! decodebin ! videoconvert ! videoscale ! videorate \
        ! video/x-raw,format=UYVY,width=1920,height=1080,framerate=25/1 \
        ! interlace field-pattern=2:2 top-field-first=true \
        ! decklinkvideosink mode=1080i50 \
      d.audio_0 ! queue ! aacparse ! avdec_aac ! audioconvert ! audioresample \
        ! tee name=audio_tee \
        audio_tee. ! queue ! jackaudiosink client-name=gst-heb-L port-pattern=20 sync=false \
        audio_tee. ! queue ! jackaudiosink client-name=gst-heb-R port-pattern=21 sync=false \
        audio_tee. ! queue ! jackaudiosink client-name=gst-en  port-pattern=29 sync=false \
        audio_tee. ! queue ! jackaudiosink client-name=gst-ru  port-pattern=22 sync=false \
        audio_tee. ! queue ! jackaudiosink client-name=gst-es  port-pattern=30 sync=false \
        audio_tee. ! queue ! jackaudiosink client-name=gst-fr  port-pattern=1  sync=false \
        audio_tee. ! queue ! jackaudiosink client-name=gst-it  port-pattern=2  sync=false \
        audio_tee. ! queue ! jackaudiosink client-name=gst-de  port-pattern=3  sync=false \
        audio_tee. ! queue ! jackaudiosink client-name=gst-tr  port-pattern=4  sync=false \
        audio_tee. ! queue ! jackaudiosink client-name=gst-pt  port-pattern=5  sync=false \
        audio_tee. ! queue ! jackaudiosink client-name=gst-bg  port-pattern=6  sync=false \
        audio_tee. ! queue ! jackaudiosink client-name=gst-ka  port-pattern=7  sync=false \
        audio_tee. ! queue ! jackaudiosink client-name=gst-ro  port-pattern=8  sync=false \
        audio_tee. ! queue ! jackaudiosink client-name=gst-hu  port-pattern=9  sync=false \
        audio_tee. ! queue ! jackaudiosink client-name=gst-sv  port-pattern=10 sync=false \
        audio_tee. ! queue ! jackaudiosink client-name=gst-lt  port-pattern=11 sync=false \
        audio_tee. ! queue ! jackaudiosink client-name=gst-hr  port-pattern=12 sync=false \
        audio_tee. ! queue ! jackaudiosink client-name=gst-ja  port-pattern=13 sync=false \
        audio_tee. ! queue ! jackaudiosink client-name=gst-sl  port-pattern=14 sync=false \
        audio_tee. ! queue ! jackaudiosink client-name=gst-pl  port-pattern=15 sync=false \
        audio_tee. ! queue ! jackaudiosink client-name=gst-no  port-pattern=16 sync=false \
        audio_tee. ! queue ! jackaudiosink client-name=gst-lv  port-pattern=17 sync=false \
        audio_tee. ! queue ! jackaudiosink client-name=gst-uk  port-pattern=18 sync=false \
        audio_tee. ! queue ! jackaudiosink client-name=gst-nl  port-pattern=19 sync=false \
        audio_tee. ! queue ! jackaudiosink client-name=gst-zh  port-pattern=23 sync=false \
        audio_tee. ! queue ! jackaudiosink client-name=gst-am  port-pattern=24 sync=false \
        audio_tee. ! queue ! jackaudiosink client-name=gst-hi  port-pattern=25 sync=false \
        audio_tee. ! queue ! jackaudiosink client-name=gst-fa  port-pattern=26 sync=false \
        audio_tee. ! queue ! jackaudiosink client-name=gst-ar  port-pattern=27 sync=false \
        audio_tee. ! queue ! jackaudiosink client-name=gst-id  port-pattern=28 sync=false \
        audio_tee. ! queue ! jackaudiosink client-name=gst-hy  port-pattern=32 sync=false \
        audio_tee. ! queue ! jackaudiosink client-name=gst-da  port-pattern=33 sync=false \
        audio_tee. ! queue ! jackaudiosink client-name=gst-et  port-pattern=34 sync=false \
        audio_tee. ! queue ! jackaudiosink client-name=gst-el  port-pattern=35 sync=false \
        audio_tee. ! queue ! jackaudiosink client-name=gst-tl  port-pattern=36 sync=false \
        audio_tee. ! queue ! jackaudiosink client-name=gst-az  port-pattern=37 sync=false \
      >"$FIFO" 2>&1 &
  fi
  
  local GST_PID=$!
  
  # Wait for this file to finish
  wait "$GST_PID"
  local GST_RC=$?
  
  # Clean up
  kill -TERM "$LOGGER_PID" 2>/dev/null || true
  wait "$LOGGER_PID" 2>/dev/null || true
  rm -f "$FIFO"
  
  echo "[info] file $file_label finished with exit code $GST_RC" >&2
  return $GST_RC
}

# =================== run (supports --loop) ===================
preflight_pid_guard
status_start "$LABEL" "$FILE"
printf '[%s] [playout] START label=%s file=%s loop=%s overlay=%s url=%s playlist=%s preroll=%s\n' \
  "$(date '+%F %T')" "$LABEL" "$FILE" "$LOOP" "${OVERLAY_PNG:-}" "${URL:-}" "${PLAYLIST:-}" "${PREROLL:-}" >>"$LOG"

# write controller PID ONCE (so /v1/playout/stop kills the loop reliably)
echo "$$" > "$PID_FILE"

# Build video chain safely (overlay optional) – output interlaced 1080i50
VIDEO_CHAIN=( d.video_0 ! queue ! h264parse ! avdec_h264 ! videoconvert )
if [ "$OVERLAY_ENABLED" -eq 1 ] && [ -f "$OVERLAY_PNG" ]; then
  VIDEO_CHAIN+=( ! gdkpixbufoverlay location="$OVERLAY_PNG" alpha=1.0 offset-x=0 offset-y=0 )
  echo "[info] overlay enabled: $OVERLAY_PNG" >&2
else
  echo "[info] overlay disabled or not found; continuing without overlay" >&2
fi
VIDEO_CHAIN+=( ! videoconvert ! videoscale ! videorate \
  ! video/x-raw,format=UYVY,width=1920,height=1080,framerate=25/1 \
  ! interlace field-pattern=2:2 top-field-first=true \
  ! decklinkvideosink mode=1080i50 sync=true )
while :; do
  # ---- robust timestamped logging via FIFO (no SIGPIPE) ----
  FIFO="$STATE_DIR/.gstlog.$$.$RANDOM"
  rm -f "$FIFO"; mkfifo "$FIFO"

  (
    # background logger: prefix each line with timestamp and append to $LOG
    # tolerate EINTR (read returning 4 / "Interrupted system call") which happens
    # when we signal the logger during shutdown. //TODO: consider replacing with dedicated helper binary if flakes persist.
    set +e
    trap '' INT TERM
    while true; do
      if IFS= read -r line; then
        printf '[%s] [gst] %s\n' "$(date '+%F %T')" "$line" >>"$LOG"
      else
        status=$?
        if [ "$status" = "4" ]; then
          # EINTR: retry the read
          continue
        fi
        break
      fi
    done
  ) <"$FIFO" &
  LOGGER_PID=$!

  # Check for playlist first - if we have a playlist, handle it separately
  echo "[debug] PLAYLIST variable value: '$PLAYLIST'" >&2
  echo "[debug] PLAYLIST length: ${#PLAYLIST}" >&2
  if [ -n "$PLAYLIST" ]; then
    echo "[debug] PLAYLIST is not empty, checking type..." >&2
    if [[ "$PLAYLIST" == "test" ]]; then
      echo "[debug] This is a test playlist request" >&2
      play_playlist_test
      GST_RC=$?
    else
      echo "[debug] This is a play request" >&2
      # Regular playlist playback
      echo "[info] launching playlist playback..." >&2
      play_playlist
      GST_RC=$?
    fi
    # Exit the loop for playlists - they handle their own logic
    echo "[debug] Exiting main loop for playlist handling" >&2
    break
  else
    echo "[debug] PLAYLIST is empty, continuing with main pipeline" >&2
  fi

  # If concat-repeat mode requested, run it once and exit
  if [ "${REPEAT_CONCAT:-0}" -gt 0 ]; then
    echo "[info] concat-repeat mode: repeats=$REPEAT_CONCAT file=$FILE" >&2
    play_lesson_repeat_concat "$REPEAT_CONCAT" "$FILE"
    GST_RC=$?
    break
  fi

  # Build source pipeline based on input type (only for non-playlist cases)
  if [[ "$FILE" == image:* ]]; then
    # Extract image path from "image:/path/to/image.png"
    image_file="${FILE#image:}"
    echo "[info] playing image: $image_file" >&2
    SOURCE_PIPELINE="filesrc location=\"$image_file\" ! decodebin ! imagefreeze"
  elif [ -n "$URL" ]; then
    echo "[info] playing web URL: $URL" >&2
    # For web URLs, use uridecodebin directly (no need to download)
    SOURCE_PIPELINE="uridecodebin name=u2 uri=\"$URL\" use-buffering=true"
  elif [ -n "$PREROLL" ] && [ -f "$PREROLL" ]; then
    echo "[info] playing preroll video: $PREROLL" >&2
    SOURCE_PIPELINE="filesrc location=\"$PREROLL\" ! qtdemux name=d"
  else
    SOURCE_PIPELINE="filesrc location=\"$FILE\" ! qtdemux name=d"
  fi

  # Build preroll pipeline if specified (only for non-playlist cases)
  if [ -n "$PREROLL" ] && [ -f "$PREROLL" ]; then
    echo "[info] preroll video: $PREROLL" >&2
    PREROLL_PIPELINE="filesrc location=\"$PREROLL\" ! qtdemux name=d1 \
      d1.video_0 ! queue ! h264parse ! avdec_h264 ! videoconvert ! videoscale ! videorate \
                 ! video/x-raw,format=NV12,width=1920,height=1080,framerate=25/1 ! vcat.sink_0 \
      d1.audio_0 ! queue ! aacparse ! avdec_aac ! audioconvert ! audioresample \
                 ! audio/x-raw,format=F32LE,rate=48000,channels=1 ! acat.sink_0"
  else
    PREROLL_PIPELINE=""
  fi

  # launch gst and send both stdout/stderr to the FIFO
  if [[ "$FILE" == image:* ]]; then
    # For images, use the working image pipeline
    echo "[info] launching image pipeline..." >&2
    image_file="${FILE#image:}"
    "$GST" -e -vvv \
      filesrc location="$image_file" ! decodebin ! imagefreeze ! videoscale ! videoconvert \
        ! video/x-raw,format=UYVY,width=1920,height=1080,framerate=25/1 \
        ! interlace field-pattern=2:2 top-field-first=true \
        ! decklinkvideosink mode=1080i50 sync=true \
      >"$FIFO" 2>&1 &
    GST_PID=$!
  elif [ -n "$URL" ]; then
    # For web URLs, use the exact working pipeline structure with concat
    echo "[info] launching web URL pipeline..." >&2
    "$GST" -e -vvv \
      concat name=vcat ! videoconvert ! videoscale ! videorate \
        ! video/x-raw,format=UYVY,width=1920,height=1080,framerate=25/1 \
        ! interlace field-pattern=2:2 top-field-first=true \
        ! decklinkvideosink mode=1080i50 \
      concat name=acat ! tee name=mono \
        mono. ! queue ! jackaudiosink client-name=gst-heb-L port-pattern=20 sync=false \
        mono. ! queue ! jackaudiosink client-name=gst-heb-R port-pattern=21 sync=false \
        mono. ! queue ! jackaudiosink client-name=gst-en  port-pattern=29 sync=false \
        mono. ! queue ! jackaudiosink client-name=gst-ru  port-pattern=22 sync=false \
        mono. ! queue ! jackaudiosink client-name=gst-es  port-pattern=30 sync=false \
        mono. ! queue ! jackaudiosink client-name=gst-fr  port-pattern=1  sync=false \
        mono. ! queue ! jackaudiosink client-name=gst-it  port-pattern=2  sync=false \
        mono. ! queue ! jackaudiosink client-name=gst-de  port-pattern=3  sync=false \
        mono. ! queue ! jackaudiosink client-name=gst-tr  port-pattern=4  sync=false \
        mono. ! queue ! jackaudiosink client-name=gst-pt  port-pattern=5  sync=false \
        mono. ! queue ! jackaudiosink client-name=gst-bg  port-pattern=6  sync=false \
        mono. ! queue ! jackaudiosink client-name=gst-ka  port-pattern=7  sync=false \
        mono. ! queue ! jackaudiosink client-name=gst-ro  port-pattern=8  sync=false \
        mono. ! queue ! jackaudiosink client-name=gst-hu  port-pattern=9  sync=false \
        mono. ! queue ! jackaudiosink client-name=gst-sv  port-pattern=10 sync=false \
        mono. ! queue ! jackaudiosink client-name=gst-lt  port-pattern=11 sync=false \
        mono. ! queue ! jackaudiosink client-name=gst-hr  port-pattern=12 sync=false \
        mono. ! queue ! jackaudiosink client-name=gst-ja  port-pattern=13 sync=false \
        mono. ! queue ! jackaudiosink client-name=gst-sl  port-pattern=14 sync=false \
        mono. ! queue ! jackaudiosink client-name=gst-pl  port-pattern=15 sync=false \
        mono. ! queue ! jackaudiosink client-name=gst-no  port-pattern=16 sync=false \
        mono. ! queue ! jackaudiosink client-name=gst-lv  port-pattern=17 sync=false \
        mono. ! queue ! jackaudiosink client-name=gst-uk  port-pattern=18 sync=false \
        mono. ! queue ! jackaudiosink client-name=gst-nl  port-pattern=19 sync=false \
        mono. ! queue ! jackaudiosink client-name=gst-zh  port-pattern=23 sync=false \
        mono. ! queue ! jackaudiosink client-name=gst-am  port-pattern=24 sync=false \
        mono. ! queue ! jackaudiosink client-name=gst-hi  port-pattern=25 sync=false \
        mono. ! queue ! jackaudiosink client-name=gst-fa  port-pattern=26 sync=false \
        mono. ! queue ! jackaudiosink client-name=gst-ar  port-pattern=27 sync=false \
        mono. ! queue ! jackaudiosink client-name=gst-id  port-pattern=28 sync=false \
        mono. ! queue ! jackaudiosink client-name=gst-hy  port-pattern=32 sync=false \
        mono. ! queue ! jackaudiosink client-name=gst-da  port-pattern=33 sync=false \
        mono. ! queue ! jackaudiosink client-name=gst-et  port-pattern=34 sync=false \
        mono. ! queue ! jackaudiosink client-name=gst-el  port-pattern=35 sync=false \
        mono. ! queue ! jackaudiosink client-name=gst-tl  port-pattern=36 sync=false \
        mono. ! queue ! jackaudiosink client-name=gst-az  port-pattern=37 sync=false \
      $PREROLL_PIPELINE \
      $SOURCE_PIPELINE \
        u2. ! queue ! video/x-raw \
            ! videoconvert ! videoscale ! videorate \
            ! video/x-raw,format=NV12,width=1920,height=1080,framerate=25/1 ! vcat.sink_1 \
        u2. ! queue ! audio/x-raw \
            ! audioconvert ! audioresample \
            ! audio/x-raw,format=F32LE,rate=48000,channels=1 ! acat.sink_1 \
      >"$FIFO" 2>&1 &
    GST_PID=$!
  else
    # For local files, use the original complex pipeline
    echo "[info] launching local file pipeline..." >&2
    "$GST" -e -vvv \
      $SOURCE_PIPELINE \
      "${VIDEO_CHAIN[@]}" \
      d.audio_0  ! queue ! aacparse ! avdec_aac ! audioconvert ! audioresample \
        ! capsfilter caps=audio/x-raw,channels=2 ! deinterleave name=heb \
        heb.src_0 ! queue ! jackaudiosink client-name=gst-heb-L port-pattern=20 sync=false \
        heb.src_1 ! queue ! jackaudiosink client-name=gst-heb-R port-pattern=21 sync=false \
      d.audio_1  ! queue ! aacparse ! avdec_aac ! audioconvert ! audioresample ! jackaudiosink client-name=gst-en port-pattern=29 sync=false \
      d.audio_2  ! queue ! aacparse ! avdec_aac ! audioconvert ! audioresample ! jackaudiosink client-name=gst-ru port-pattern=22 sync=false \
      d.audio_3  ! queue ! aacparse ! avdec_aac ! audioconvert ! audioresample ! jackaudiosink client-name=gst-es port-pattern=30 sync=false \
      d.audio_4  ! queue ! aacparse ! avdec_aac ! audioconvert ! audioresample ! jackaudiosink client-name=gst-fr port-pattern=1  sync=false \
      d.audio_5  ! queue ! aacparse ! avdec_aac ! audioconvert ! audioresample ! jackaudiosink client-name=gst-it port-pattern=2  sync=false \
      d.audio_6  ! queue ! aacparse ! avdec_aac ! audioconvert ! audioresample ! jackaudiosink client-name=gst-de port-pattern=3  sync=false \
      d.audio_7  ! queue ! aacparse ! avdec_aac ! audioconvert ! audioresample ! jackaudiosink client-name=gst-tr port-pattern=4  sync=false \
      d.audio_8  ! queue ! aacparse ! avdec_aac ! audioconvert ! audioresample ! jackaudiosink client-name=gst-pt port-pattern=5  sync=false \
      d.audio_9  ! queue ! aacparse ! avdec_aac ! audioconvert ! audioresample ! jackaudiosink client-name=gst-bg port-pattern=6  sync=false \
      d.audio_10 ! queue ! aacparse ! avdec_aac ! audioconvert ! audioresample ! jackaudiosink client-name=gst-ka port-pattern=7  sync=false \
      d.audio_11 ! queue ! aacparse ! avdec_aac ! audioconvert ! audioresample ! jackaudiosink client-name=gst-ro port-pattern=8  sync=false \
      d.audio_12 ! queue ! aacparse ! avdec_aac ! audioconvert ! audioresample ! jackaudiosink client-name=gst-hu port-pattern=9  sync=false \
      d.audio_13 ! queue ! aacparse ! avdec_aac ! audioconvert ! audioresample ! jackaudiosink client-name=gst-sv port-pattern=10 sync=false \
      d.audio_14 ! queue ! aacparse ! avdec_aac ! audioconvert ! audioresample ! jackaudiosink client-name=gst-lt port-pattern=11 sync=false \
      d.audio_15 ! queue ! aacparse ! avdec_aac ! audioconvert ! audioresample ! jackaudiosink client-name=gst-hr port-pattern=12 sync=false \
      d.audio_16 ! queue ! aacparse ! avdec_aac ! audioconvert ! audioresample ! jackaudiosink client-name=gst-ja port-pattern=13 sync=false \
      d.audio_17 ! queue ! aacparse ! avdec_aac ! audioconvert ! audioresample ! jackaudiosink client-name=gst-sl port-pattern=14 sync=false \
      d.audio_18 ! queue ! aacparse ! avdec_aac ! audioconvert ! audioresample ! jackaudiosink client-name=gst-pl port-pattern=15 sync=false \
      d.audio_19 ! queue ! aacparse ! avdec_aac ! audioconvert ! audioresample ! jackaudiosink client-name=gst-no port-pattern=16 sync=false \
      d.audio_20 ! queue ! aacparse ! avdec_aac ! audioconvert ! audioresample ! jackaudiosink client-name=gst-lv port-pattern=17 sync=false \
      d.audio_21 ! queue ! aacparse ! avdec_aac ! audioconvert ! audioresample ! jackaudiosink client-name=gst-uk port-pattern=18 sync=false \
      d.audio_22 ! queue ! aacparse ! avdec_aac ! audioconvert ! audioresample ! jackaudiosink client-name=gst-nl port-pattern=19 sync=false \
      d.audio_23 ! queue ! aacparse ! avdec_aac ! audioconvert ! audioresample ! jackaudiosink client-name=gst-zh port-pattern=23 sync=false \
      d.audio_24 ! queue ! aacparse ! avdec_aac ! audioconvert ! audioresample ! jackaudiosink client-name=gst-am port-pattern=24 sync=false \
      d.audio_25 ! queue ! aacparse ! avdec_aac ! audioconvert ! audioresample ! jackaudiosink client-name=gst-hi port-pattern=25 sync=false \
      d.audio_26 ! queue ! aacparse ! avdec_aac ! audioconvert ! audioresample ! jackaudiosink client-name=gst-fa port-pattern=26 sync=false \
      d.audio_27 ! queue ! aacparse ! avdec_aac ! audioconvert ! audioresample ! jackaudiosink client-name=gst-ar port-pattern=27 sync=false \
      d.audio_28 ! queue ! aacparse ! avdec_aac ! audioconvert ! audioresample ! jackaudiosink client-name=gst-id port-pattern=28 sync=false \
      d.audio_29 ! queue ! aacparse ! avdec_aac ! audioconvert ! audioresample ! jackaudiosink client-name=gst-hy port-pattern=32 sync=false \
      d.audio_30 ! queue ! aacparse ! avdec_aac ! audioconvert ! audioresample ! jackaudiosink client-name=gst-da port-pattern=33 sync=false \
      d.audio_31 ! queue ! aacparse ! avdec_aac ! audioconvert ! audioresample ! jackaudiosink client-name=gst-et port-pattern=34 sync=false \
      d.audio_32 ! queue ! aacparse ! avdec_aac ! audioconvert ! audioresample ! jackaudiosink client-name=gst-el port-pattern=35 sync=false \
      d.audio_33 ! queue ! aacparse ! avdec_aac ! audioconvert ! audioresample ! jackaudiosink client-name=gst-tl port-pattern=36 sync=false \
      d.audio_34 ! queue ! aacparse ! avdec_aac ! audioconvert ! audioresample ! jackaudiosink client-name=gst-az port-pattern=37 sync=false \
      d.audio_35 ! queue ! aacparse ! avdec_aac ! fakesink sync=false \
      >"$FIFO" 2>&1 &
    GST_PID=$!
  fi

  # publish running state with the gst child pid (each loop/run)
  # (playlist prepare doesn't start playback, so skip status writes)
  if [[ "${PLAYLIST:-}" == prepare:* ]]; then
    play_state_write false "$LABEL" "$FILE" 0
    status_stop "$LABEL" "$FILE"
  else
    play_state_write true "$LABEL" "$FILE" "$GST_PID"
    status_write true "playout" "$LABEL" "$FILE" "$GST_PID"
  fi

  # heartbeat for this run and wait for gst
  start_playout_heartbeat
  set +e
  wait "$GST_PID"; GST_RC=$?
  set -e
  printf '[%s] [playout] GST exit rc=%s\n' "$(date '+%F %T')" "$GST_RC" >>"$LOG"
  stop_playout_heartbeat

  # let logger flush & exit; then remove FIFO
  wait "$LOGGER_PID" 2>/dev/null || true
  rm -f "$FIFO"; FIFO=""; LOGGER_PID=""


  # If this run was a lesson, save lesson overlay settings for future fallback
  if [[ "$LABEL" == lesson-* ]]; then
    /usr/bin/python3 - "$STATE_DIR/last_lesson.json" <<'PY'
import json, os, sys, tempfile
path = sys.argv[1]
overlay = os.environ.get('OVERLAY_PNG') or ''
enabled = str(os.environ.get('OVERLAY_ENABLED', '1')).strip().lower() not in ('0','false','no','off','')
data = {"overlay": overlay, "overlay_enabled": bool(enabled)}
d = os.path.dirname(path); os.makedirs(d, exist_ok=True)
fd, tmp = tempfile.mkstemp(dir=d, prefix='.lastlesson.', text=True)
with os.fdopen(fd, 'w') as f: json.dump(data, f)
os.replace(tmp, path)
PY
  fi


  # loop only on clean/EOS finishes
  if [ "$LOOP" -eq 1 ] && [ "$GST_RC" -eq 0 ]; then
    echo "[info] playout finished (rc=$GST_RC), looping..." >&2
    echo "[info] waiting 2 seconds for GStreamer cleanup to prevent jitter..." >&2
    sleep 2
    continue
  else
    break
  fi
done

# cleanup trap runs