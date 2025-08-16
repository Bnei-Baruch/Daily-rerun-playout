#!/bin/bash
set -euo pipefail

# =================== legacy bb-status (status.json) ===================
BB_STATUS_FILE="${BB_STATUS_FILE:-/opt/backup/status.json}"
status_write () {
  # args: running mode label file pid
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

# =================== state paths / dirs ===================
STATE_DIR="${STATE_DIR:-/opt/backup/state}"
PLAY_STATE="$STATE_DIR/playout.json"
PID_FILE="$STATE_DIR/playout.pid"
LOG="/opt/backup/logs/playout_gst.log"
mkdir -p "$STATE_DIR" "$(dirname "$LOG")" "$(dirname "$BB_STATUS_FILE")" /opt/backup/latest

# =================== stale-state coercion ===================
coerce_stale_playout_state() {
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
        import tempfile, json as _j, os as _o
        fd, tmp = tempfile.mkstemp(dir=dirp, prefix=".playout.", text=True)
        with os.fdopen(fd, "w") as f: _j.dump(d, f)
        _o.replace(tmp, p)
PY
}
coerce_stale_playout_state

# =================== state writer (/v1/playout) ===================
play_state_write () {
  # args: running label file pid
  local run="$1" label="$2" file="$3" pid="$4"
  /usr/bin/python3 - "$PLAY_STATE" "$run" "$label" "$file" "$pid" <<'PY'
import sys, json, os, tempfile, time
path, running, label, file, pid = sys.argv[1], sys.argv[2]=='true', sys.argv[3], sys.argv[4], int(sys.argv[5])
try:
  with open(path,'r') as f: data=json.load(f)
except Exception:
  data={}
data.update(dict(running=running, mode="playout", label=label, file=file, pid=(pid if running else 0)))
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

# =================== heartbeat (/v1/playout) ===================
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
}

# =================== preflight PID guard ===================
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
: "${1:?usage: playout_all.sh /path/to/file.mp4  |  lesson-1..lesson-4}"
RAW="$1"
# strip common wrappers/quotes/whitespace (macOS Bash 3.2 safe)
case "$RAW" in *$'\r') RAW="${RAW%$'\r'}";; esac
while :; do case "$RAW" in *" ") RAW="${RAW% }";; *$'\t') RAW="${RAW%$'\t'}";; *\}) RAW="${RAW%\}}";; *\]) RAW="${RAW%\]}";; *\") RAW="${RAW%\"}";; *\') RAW="${RAW%\'}";; *) break;; esac; done
while :; do case "$RAW" in " "*) RAW="${RAW# }";; $'\t'*) RAW="${RAW#$'\t'}";; \{*) RAW="${RAW#\{}";; [[]*) RAW="${RAW#[[]}" ;; \"*) RAW="${RAW#\"}";; \'*) RAW="${RAW#\'}";; *) break;; esac; done

case "$RAW" in
  lesson-[1-4]) FILE="/opt/backup/latest/$RAW.mp4" ;;
  *)            FILE="$RAW" ;;
esac
[ -f "$FILE" ] || { echo "file not found: $FILE" >&2; exit 2; }
LABEL="${LABEL:-$(basename "${FILE%.*}")}"
OVERLAY_PNG="${OVERLAY_PNG:-/Users/shidur/rerun.png}"

# =================== tools ===================
GST="${GST:-/opt/homebrew/bin/gst-launch-1.0}"
[ -x "$GST" ] || GST="/usr/local/bin/gst-launch-1.0"
command -v "$GST" >/dev/null 2>&1 || { echo "gst-launch-1.0 not found" >&2; exit 127; }

# =================== cleanup/trap ===================
cleanup() {
  local rc=${GST_RC:-$?}
  if [ -n "${GST_PID:-}" ] && kill -0 "$GST_PID" 2>/dev/null; then
    kill -TERM "$GST_PID" 2>/dev/null || true
    wait "$GST_PID" 2>/dev/null || true
  fi
  stop_playout_heartbeat
  rm -f "$PID_FILE"
  play_state_write false "$LABEL" "$FILE" 0
  status_stop "$LABEL" "$FILE"
  case "${rc:-0}" in 255|143) rc=0;; esac   # normalize SIGTERM/255 as success
  exit $rc
}

# =================== run ===================
preflight_pid_guard
status_start "$LABEL" "$FILE"

# launch pipeline
"$GST" -e -vvv \
  filesrc location="$FILE" ! qtdemux name=d \
 d.video_0 ! queue ! h264parse ! vtdec ! videoconvert \
  ! gdkpixbufoverlay location="$OVERLAY_PNG" alpha=1.0 offset-x=0 offset-y=0 \
  ! interlace field-pattern=2:2 top-field-first=true \
  ! decklinkvideosink mode=11 \
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
  2>>"$LOG" &

GST_PID=$!

# arm trap only after we have a child
trap cleanup EXIT INT TERM

echo "$GST_PID" > "$PID_FILE"
# write state with the REAL pid
play_state_write true "$LABEL" "$FILE" "$GST_PID"
# refresh legacy with real pid
status_write true "playout" "$LABEL" "$FILE" "$GST_PID"

# start heartbeat and wait
start_playout_heartbeat

set +e
wait "$GST_PID"
GST_RC=$?
set -e
