#!/bin/bash
set -euo pipefail

STATE_DIR="${STATE_DIR:-/opt/backup/state}" 
LOG_DIR="${LOG_DIR:-/opt/backup/logs}"
PLAYLIST_DIR="${PLAYLIST_DIR:-/opt/backup/playlist}"
LOG_FILE="$LOG_DIR/playlist_prepare.log"
mkdir -p "$STATE_DIR" "$LOG_DIR"

usage() {
  echo "usage: $0 --playlist url1,url2 [--label name]" >&2
  exit 1
}

PLAYLIST=""
LABEL="playlist-prepare"
while [[ $# -gt 0 ]]; do
  case "$1" in
    --playlist) PLAYLIST="$2"; shift 2;;
    --label) LABEL="$2"; shift 2;;
    -h|--help) usage;;
    --) shift; break;;
    -*) echo "unknown option: $1" >&2; usage;;
    *) break;;
  esac
done

[[ -n "$PLAYLIST" ]] || usage

trim_ws() {
  local s="$1"
  s="${s#"${s%%[!$'\t\r\n ']*}"}"
  s="${s%"${s##*[!$'\t\r\n ']}"}"
  printf '%s' "$s"
}

IFS=',' read -ra URLS_RAW <<<"$PLAYLIST"
URLS=()
for idx in "${!URLS_RAW[@]}"; do
  url="$(trim_ws "${URLS_RAW[$idx]}")"
  if [[ -n "$url" ]]; then
    URLS+=("$url")
  else
    echo "[warn] skipping empty url at index $idx" >&2
  fi
done

[[ ${#URLS[@]} -gt 0 ]] || { echo "[error] no playlist URLs provided" >&2; exit 1; }

PLAYLIST_META="$PLAYLIST_DIR/playlist_meta.json"
mkdir -p "$PLAYLIST_DIR"
rm -f "$PLAYLIST_DIR"/*
echo '{}' >"$PLAYLIST_META"

resolve_bin() {
  local prefer="$1" fallback1="$2" fallback2="$3"
  if command -v "$prefer" >/dev/null 2>&1; then
    printf '%s' "$prefer"
  elif [[ -x "$fallback1" ]]; then
    printf '%s' "$fallback1"
  elif [[ -x "$fallback2" ]]; then
    printf '%s' "$fallback2"
  else
    printf '%s' "$prefer"
  fi
}

FFMPEG_BIN="$(resolve_bin "${FFMPEG:-ffmpeg}" "${FFMPEG_PATH:-/opt/homebrew/bin/ffmpeg}" "${FFMPEG_PATH:-/usr/local/bin/ffmpeg}")"
FFPROBE_BIN="$(resolve_bin "${FFPROBE:-ffprobe}" "${FFPROBE_PATH:-/opt/homebrew/bin/ffprobe}" "${FFPROBE_PATH:-/usr/local/bin/ffprobe}")"

printf '[%s] [playlist-prepare] START label=%s urls=%s\n' "$(date '+%F %T')" "$LABEL" "${URLS[*]}" >>"$LOG_FILE"

SUCCESS=()
for i in "${!URLS[@]}"; do
  idx=$((i+1))
  url="${URLS[$i]}"
  output="$PLAYLIST_DIR/url_${idx}.mp4"
  echo "[info] downloading URL $idx/${#URLS[@]}: $url" >&2
  if curl -L -o "$output" "$url"; then
    echo "[info] download successful: $output" >&2
    SUCCESS+=("$output")
    png_out="$PLAYLIST_DIR/last_frame-${idx}.png"
    if command -v "$FFMPEG_BIN" >/dev/null 2>&1; then
      echo "[info] capturing last frame to: $png_out" >&2
      "$FFMPEG_BIN" -y -sseof -1 -i "$output" -frames:v 1 -qscale:v 2 "$png_out" >/dev/null 2>&1 || \
        echo "[warn] ffmpeg last-frame capture failed for $output" >&2
    else
      echo "[warn] ffmpeg not found; skipping last-frame capture" >&2
    fi
  else
    echo "[error] download failed for: $url" >&2
    continue
  fi

  final_url="$(curl -Ls -o /dev/null -w '%{url_effective}' "$url" 2>/dev/null || echo "$url")"
  original_name="$(basename "${final_url%%\?*}")"
  [[ -n "$original_name" ]] || original_name="unknown"
  /usr/bin/python3 - "$PLAYLIST_META" "$idx" "$original_name" "$final_url" <<'PY'
import json, os, sys, tempfile
path, idx, name, final_url = sys.argv[1], sys.argv[2], sys.argv[3], sys.argv[4]
try:
    with open(path) as f:
        d = json.load(f)
except Exception:
    d = {}
entry = d.get(str(idx)) or {}
entry.update({"original_name": name, "final_url": final_url})
d[str(idx)] = entry
fd, tmp = tempfile.mkstemp(dir=os.path.dirname(path) or '.', prefix='.meta.', text=True)
with os.fdopen(fd, 'w') as f: json.dump(d, f)
os.replace(tmp, path)
PY

  if command -v "$FFPROBE_BIN" >/dev/null 2>&1; then
    dur="$($FFPROBE_BIN -v error -show_entries format=duration -of default=noprint_wrappers=1:nokey=1 "$output" 2>/dev/null | head -n1)"
    if [[ -n "$dur" ]]; then
      /usr/bin/python3 - "$PLAYLIST_META" "$idx" "$dur" <<'PY'
import json, os, sys, tempfile
path, idx, dur = sys.argv[1], sys.argv[2], sys.argv[3]
try:
    with open(path) as f:
        d = json.load(f)
except Exception:
    d = {}
entry = d.get(str(idx)) or {}
try:
    seconds = float(dur)
except Exception:
    seconds = 0.0
sec = max(0, int(round(seconds)))
h = sec // 3600
m = (sec % 3600) // 60
s = sec % 60
entry.update({"duration_hms": f"{h:02d}:{m:02d}:{s:02d}"})
d[str(idx)] = entry
fd, tmp = tempfile.mkstemp(dir=os.path.dirname(path) or '.', prefix='.meta.', text=True)
with os.fdopen(fd, 'w') as f: json.dump(d, f)
os.replace(tmp, path)
PY
    fi
  fi
done

if [[ ${#SUCCESS[@]} -eq 0 ]]; then
  printf '[%s] [playlist-prepare] STOP label=%s rc=1\n' "$(date '+%F %T')" "$LABEL" >>"$LOG_FILE"
  exit 1
fi

printf '[%s] [playlist-prepare] STOP label=%s rc=0 files=%d\n' "$(date '+%F %T')" "$LABEL" "${#SUCCESS[@]}" >>"$LOG_FILE"

/usr/bin/python3 - "$STATE_DIR/playlist_prep.json" "$LABEL" "$PLAYLIST_DIR" "${#SUCCESS[@]}" <<'PY'
import json, os, sys, tempfile, time
path, label, playlist_dir, count = sys.argv[1], sys.argv[2], sys.argv[3], int(sys.argv[4])
try:
    with open(path) as f:
        d = json.load(f)
except Exception:
    d = {}
d.update({
    "label": label,
    "playlist_dir": playlist_dir,
    "file_count": count,
    "prepared_at": int(time.time())
})
fd, tmp = tempfile.mkstemp(dir=os.path.dirname(path) or '.', prefix='.prep.', text=True)
with os.fdopen(fd, 'w') as f: json.dump(d, f)
os.replace(tmp, path)
PY

exit 0
