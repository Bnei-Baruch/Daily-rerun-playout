
#!/bin/bash
set -euo pipefail

# =================== Status helpers ===================
BB_STATUS_FILE="${BB_STATUS_FILE:-/opt/backup/status.json}"

status_write () {
  # args: running mode label file pid
  local running="$1" mode="$2" label="$3" file="$4" pid="$5"
  local ts; ts="$(date +%s)"
  local dir; dir="$(dirname "$BB_STATUS_FILE")"
  mkdir -p "$dir"
  [[ -f "$BB_STATUS_FILE" ]] || echo '{"running":false}' > "$BB_STATUS_FILE"

  # last chance: if file is empty but OUT is set, use it
  if [[ -z "${file:-}" && -n "${OUT:-}" ]]; then file="$OUT"; fi

  /usr/bin/python3 - "$BB_STATUS_FILE" "$running" "$mode" "$label" "$file" "$pid" "$ts" <<'PYST'
import json, os, sys, tempfile
path, running, mode, label, file, pid, ts = sys.argv[1], sys.argv[2]=='true', sys.argv[3], sys.argv[4], sys.argv[5], int(sys.argv[6]), int(sys.argv[7])
try:
    with open(path, 'r') as f: data = json.load(f)
except Exception:
    data = {}
if not file:
    file = os.environ.get('OUT','')
data.update(dict(running=running, mode=mode, label=label, file=file, pid=pid))
if running:
    data['started_at'] = ts
    if 'ended_at' in data: del data['ended_at']
else:
    data['ended_at'] = ts
d = os.path.dirname(path); os.makedirs(d, exist_ok=True)
fd, tmp = tempfile.mkstemp(dir=d, prefix='.status.', text=True)
with os.fdopen(fd, 'w') as f: json.dump(data, f)
os.replace(tmp, path)
PYST
}

status_start () { status_write true  "capture" "$1" "$2" "$$"; }
status_stop  () { status_write false "capture" "$1" "$2" 0; }

# =================== Sane defaults ===================
OUT_DIR="${OUT_DIR:-/opt/backup}"
LOG_DIR="${LOG_DIR:-/opt/backup/logs}"
STATE_DIR="${STATE_DIR:-/opt/backup/state}"
PID_FILE="$STATE_DIR/capture.pid"
CAP_STATE="$STATE_DIR/capture.json"

# ensure directories exist before any checks
mkdir -p "$OUT_DIR" "$LOG_DIR" "$STATE_DIR"

# --- coerce stale capture state (if PID not alive, flip to stopped) ---
coerce_stale_capture_state() {
  /usr/bin/python3 - "$CAP_STATE" <<'PY'
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
        os.kill(pid, 0)  # check process alive
    except OSError:
        d["running"] = False
        d["pid"] = 0
        d["ended_at"] = int(time.time())
        dirp = os.path.dirname(p); os.makedirs(dirp, exist_ok=True)
        fd, tmp = tempfile.mkstemp(dir=dirp, prefix=".capture.", text=True)
        with os.fdopen(fd, "w") as f: json.dump(d, f)
        os.replace(tmp, p)
PY
}
coerce_stale_capture_state
# --- end coerce ---

# ---- write /opt/backup/state/capture.json for /v1/capture ----
cap_state_write() {
  # args: running(bool) pid(int)
  local running="$1" pid="$2"
    /usr/bin/python3 - "$CAP_STATE" "$running" "$BASE_LABEL" "$OUT" "$pid" <<'PY'
import json, os, sys, tempfile, time
p, run, label, file, pid = sys.argv[1], sys.argv[2]=="true", sys.argv[3], sys.argv[4], int(sys.argv[5])
try:
    with open(p) as f: d = json.load(f)
except Exception:
    d = {}
d.update(dict(running=run, mode="capture", label=label, file=file, pid=(pid if run else 0)))
t = int(time.time())
if run:
    # new run: set started_at to now and clear any stale ended_at
    d["started_at"] = t
    if "ended_at" in d:
        del d["ended_at"]
else:
    d["ended_at"] = t
dir = os.path.dirname(p); os.makedirs(dir, exist_ok=True)
fd, tmp = tempfile.mkstemp(dir=dir, prefix=".capture.", text=True)
with os.fdopen(fd, "w") as f: json.dump(d, f)
os.replace(tmp, p)
PY
}

# --- heartbeat writer for /v1/capture ---
cap_hb_tick() {
  /usr/bin/python3 - "$CAP_STATE" <<'PY'
import json, os, sys, time, tempfile
p = sys.argv[1]
try:
    with open(p) as f: d = json.load(f)
except Exception:
    d = {}
d["heartbeat"] = int(time.time())
dirp = os.path.dirname(p); os.makedirs(dirp, exist_ok=True)
fd, tmp = tempfile.mkstemp(dir=dirp, prefix=".capture.", text=True)
with os.fdopen(fd, "w") as f: json.dump(d, f)
os.replace(tmp, p)
PY
}

start_capture_heartbeat() {  # call after FF_PID is set
  (
    while kill -0 "$FF_PID" 2>/dev/null; do
      cap_hb_tick
      sleep 2
    done
  ) &
  HB_PID=$!
}
stop_capture_heartbeat() {
  if [ -n "${HB_PID:-}" ] && kill -0 "$HB_PID" 2>/dev/null; then
    kill -TERM "$HB_PID" 2>/dev/null || true
    wait "$HB_PID" 2>/dev/null || true
  fi
}
# --- end heartbeat ---

# --- preflight PID guard (prevent double-start) ---
preflight_pid_guard() {
  if [ -f "$PID_FILE" ]; then
    local oldpid
    oldpid="$(cat "$PID_FILE" 2>/dev/null || echo 0)"
    if [ "${oldpid:-0}" -gt 0 ] && kill -0 "$oldpid" 2>/dev/null; then
      echo "capture already running (pid $oldpid)" >&2
      exit 1
    else
      rm -f "$PID_FILE"
    fi
  fi
}
# --- end guard ---

FFMPEG="${FFMPEG:-/usr/local/bin/ffmpeg-bb}"
if ! command -v "$FFMPEG" >/dev/null 2>&1; then
  FFMPEG="$(command -v ffmpeg || true)"
fi
[[ -n "$FFMPEG" ]] || { echo "ffmpeg not found" >&2; exit 127; }

# =================== Args ===================
DUR=""
LABEL="capture"
OUTFILE=""

usage(){ echo "usage: $0 [-d seconds] [-r label] [-o /path/file.mp4]"; exit 1; }

while [[ $# -gt 0 ]]; do
  case "$1" in
    -d|--duration) DUR="$2"; shift 2;;
    -r|--label)    LABEL="$2"; shift 2;;
    -o|--out)      OUTFILE="$2"; shift 2;;
    -h|--help)     usage;;
    *) echo "unknown arg: $1" >&2; usage;;
  esac
done

now(){ date +%s; }

# =================== Names/paths ===================
TS="$(date +%Y%m%d_%H%M%S)"
BASE_LABEL="${LABEL:-capture}"
OUT="${OUTFILE:-$OUT_DIR/${BASE_LABEL}_${TS}.mp4}"
PROG_FILE="$LOG_DIR/${BASE_LABEL}.progress.log"
mkdir -p "$(dirname "$OUT")" "$(dirname "$PROG_FILE")"

echo ">>> Capturing to: $OUT"
echo ">>> Progress:     $PROG_FILE"


# =================== FFmpeg ===================
# Audio: DVS 64ch (big-endian) -> 36 output tracks (0: stereo, then many mono, last stereo)
COMMON_INPUTS=(
  -hide_banner -y
  -progress "$PROG_FILE"
  -thread_queue_size 512 -big_endian 1 -f audiotoolbox -i "AUDINATE-DVS-UID"
  -thread_queue_size 512 -format_code Hi50 -f decklink -i "UltraStudio Recorder 3G"
)

MAPS=( -map 1:v )
for _ in $(seq 1 36); do MAPS+=( -map 0:a ); done

FILTERS=(
  -g 12
  -filter:1  "pan=stereo|c0=c0|c1=c1,volume=9dB"
  -filter:2  "pan=mono|c0=c2,volume=9dB"
  -filter:3  "pan=mono|c0=c3,volume=9dB"
  -filter:4  "pan=mono|c0=c4,volume=9dB"
  -filter:5  "pan=mono|c0=c5,volume=9dB"
  -filter:6  "pan=mono|c0=c6,volume=9dB"
  -filter:7  "pan=mono|c0=c7,volume=9dB"
  -filter:8  "pan=mono|c0=c8,volume=9dB"
  -filter:9  "pan=mono|c0=c9,volume=9dB"
  -filter:10 "pan=mono|c0=c10,volume=9dB"
  -filter:11 "pan=mono|c0=c11,volume=9dB"
  -filter:12 "pan=mono|c0=c12,volume=9dB"
  -filter:13 "pan=mono|c0=c13,volume=9dB"
  -filter:14 "pan=mono|c0=c14,volume=9dB"
  -filter:15 "pan=mono|c0=c15,volume=9dB"
  -filter:16 "pan=mono|c0=c16,volume=9dB"
  -filter:17 "pan=mono|c0=c17,volume=9dB"
  -filter:18 "pan=mono|c0=c18,volume=9dB"
  -filter:19 "pan=mono|c0=c19,volume=9dB"
  -filter:20 "pan=mono|c0=c20,volume=9dB"
  -filter:21 "pan=mono|c0=c21,volume=9dB"
  -filter:22 "pan=mono|c0=c22,volume=9dB"
  -filter:23 "pan=mono|c0=c23,volume=9dB"
  -filter:24 "pan=mono|c0=c24,volume=9dB"
  -filter:25 "pan=mono|c0=c25,volume=9dB"
  -filter:26 "pan=mono|c0=c26,volume=9dB"
  -filter:27 "pan=mono|c0=c27,volume=9dB"
  -filter:28 "pan=mono|c0=c28,volume=9dB"
  -filter:29 "pan=mono|c0=c29,volume=9dB"
  # 30/31 reserved for stereo below
  -filter:30 "pan=mono|c0=c32,volume=9dB"
  -filter:31 "pan=mono|c0=c33,volume=9dB"
  -filter:32 "pan=mono|c0=c34,volume=9dB"
  -filter:33 "pan=mono|c0=c35,volume=9dB"
  -filter:34 "pan=mono|c0=c36,volume=9dB"
  -filter:35 "pan=mono|c0=c37,volume=9dB"
  -filter:36 "pan=stereo|c0=c30|c1=c31,volume=9dB"
)

META=(
  -metadata "network=archiveb"
  -metadata "comment=c$(date +%s)"
  -metadata:s:a:0  language=heb
  -metadata:s:a:1  language=eng
  -metadata:s:a:2  language=rus
  -metadata:s:a:3  language=spa
  -metadata:s:a:4  language=fre
  -metadata:s:a:5  language=ita
  -metadata:s:a:6  language=ger
  -metadata:s:a:7  language=tur
  -metadata:s:a:8  language=por
  -metadata:s:a:9  language=bul
  -metadata:s:a:10 language=geo
  -metadata:s:a:11 language=ron
  -metadata:s:a:12 language=hun
  -metadata:s:a:13 language=swe
  -metadata:s:a:14 language=lit
  -metadata:s:a:15 language=hrv
  -metadata:s:a:16 language=jpn
  -metadata:s:a:17 language=slv
  -metadata:s:a:18 language=pol
  -metadata:s:a:19 language=nor
  -metadata:s:a:20 language=lav
  -metadata:s:a:21 language=ukr
  -metadata:s:a:22 language=dut
  -metadata:s:a:23 language=chi
  -metadata:s:a:24 language=amh
  -metadata:s:a:25 language=hin
  -metadata:s:a:26 language=per
  -metadata:s:a:27 language=ara
  -metadata:s:a:28 language=ind
  -metadata:s:a:29 language=arm
  -metadata:s:a:30 language=dan
  -metadata:s:a:31 language=est
  -metadata:s:a:32 language=gre
  -metadata:s:a:33 language=tgl
  -metadata:s:a:34 language=aze
  -metadata:s:a:35 language=src
)

VCODEC=( -c:v h264_videotoolbox -realtime 1 -b:v 25000k -profile:v high )
ACODEC=( -c:a libfdk_aac )
APER=(
  -ac:a:0 2  -b:a:0 256k
  -ac:a:1 1  -b:a:1 128k
  -ac:a:2 1  -b:a:2 128k
  -ac:a:3 1  -b:a:3 128k
  -ac:a:4 1  -b:a:4 128k
  -ac:a:5 1  -b:a:5 128k
  -ac:a:6 1  -b:a:6 128k
  -ac:a:7 1  -b:a:7 128k
  -ac:a:8 1  -b:a:8 128k
  -ac:a:9 1  -b:a:9 128k
  -ac:a:10 1 -b:a:10 128k
  -ac:a:11 1 -b:a:11 128k
  -ac:a:12 1 -b:a:12 128k
  -ac:a:13 1 -b:a:13 128k
  -ac:a:14 1 -b:a:14 128k
  -ac:a:15 1 -b:a:15 128k
  -ac:a:16 1 -b:a:16 128k
  -ac:a:17 1 -b:a:17 128k
  -ac:a:18 1 -b:a:18 128k
  -ac:a:19 1 -b:a:19 128k
  -ac:a:20 1 -b:a:20 128k
  -ac:a:21 1 -b:a:21 128k
  -ac:a:22 1 -b:a:22 128k
  -ac:a:23 1 -b:a:23 128k
  -ac:a:24 1 -b:a:24 128k
  -ac:a:25 1 -b:a:25 128k
  -ac:a:26 1 -b:a:26 128k
  -ac:a:27 1 -b:a:27 128k
  -ac:a:28 1 -b:a:28 128k
  -ac:a:29 1 -b:a:29 128k
  -ac:a:30 1 -b:a:30 128k
  -ac:a:31 1 -b:a:31 128k
  -ac:a:32 1 -b:a:32 128k
  -ac:a:33 1 -b:a:33 128k
  -ac:a:34 1 -b:a:34 128k
  -ac:a:35 2 -b:a:35 256k
)

TARG=( -f mp4 "$OUT" )
[[ -n "${DUR:-}" ]] && TARG=( -t "$DUR" "${TARG[@]}" )

finish() {
  rc=${FF_RC:-$?}                # use recorded child exit if present
  if [ -n "${FF_PID:-}" ] && kill -0 "$FF_PID" 2>/dev/null; then
    kill -TERM "$FF_PID" 2>/dev/null || true
    wait "$FF_PID" 2>/dev/null || true
  fi
  stop_capture_heartbeat
  cap_state_write false 0
  status_stop "$BASE_LABEL" "$OUT"
  rm -f "$PID_FILE"
  mkdir -p "$OUT_DIR/latest"
  ln -sfn "$OUT" "$OUT_DIR/latest/${BASE_LABEL}.mp4"

  # ffmpeg often returns 255/143 when terminated; treat that as success
  case "${rc:-0}" in 255|143) rc=0;; esac
  exit $rc
}

# ensure not already running
preflight_pid_guard

# Go! (run encoder in background, record real PID, clean up on exit)
"$FFMPEG" \
  "${COMMON_INPUTS[@]}" \
  "${MAPS[@]}" \
  "${FILTERS[@]}" \
  "${META[@]}" \
  "${VCODEC[@]}" \
  "${ACODEC[@]}" \
  "${APER[@]}" \
  "${TARG[@]}" &
FF_PID=$!

# arm trap only after we actually have a child to clean up
trap finish EXIT INT TERM

# write the *real* encoder PID and update status
echo "$FF_PID" > "$PID_FILE"
status_write true "capture" "$BASE_LABEL" "$OUT" "$FF_PID"
cap_state_write true "$FF_PID"

# start heartbeat
start_capture_heartbeat

# wait for ffmpeg to finish and remember its exit code for the trap
set +e
wait "$FF_PID"
FF_RC=$?
set -e
