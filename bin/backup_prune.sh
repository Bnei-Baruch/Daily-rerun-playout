#!/bin/bash
set -euo pipefail

OUT_DIR="${OUT_DIR:-/opt/backup}"
LOG_DIR="${LOG_DIR:-/opt/backup/logs}"
LATEST_DIR="$OUT_DIR/latest"
KEEP_DAYS="${KEEP_DAYS:-2}"
DRY_RUN="${DRY_RUN:-0}"

echo
echo "Prune starting..."
echo "  OUT_DIR=$OUT_DIR  LOG_DIR=$LOG_DIR  KEEP_DAYS=$KEEP_DAYS  DRY_RUN=$DRY_RUN"

mkdir -p "$OUT_DIR" "$LOG_DIR" "$LATEST_DIR"

# ------------------ build PROTECTED list from latest/*.mp4 symlinks ------------------
PROTECTED_LIST=""
if [ -d "$LATEST_DIR" ]; then
  # iterate all items (some systems may not have *.mp4 symlinks only)
  for link in "$LATEST_DIR"/*; do
    [ -L "$link" ] || continue
    # resolve to an absolute real path using Python (portable + handles relative)
    target="$(/usr/bin/env python3 - <<'PY' "$link"
import os,sys
p=sys.argv[1]
try:
  print(os.path.realpath(p))
except Exception:
  pass
PY
)"
    if [ -n "${target:-}" ] && [ -e "$target" ]; then
      PROTECTED_LIST="${PROTECTED_LIST}${target}"$'\n'
    fi
  done
fi

if [ -n "${PROTECTED_LIST//$'\n'/}" ]; then
  echo "Protected targets from latest/:"
  # print unique lines
  printf "%s" "$PROTECTED_LIST" | awk 'NF && !seen[$0]++ {print "  - "$0}'
else
  echo "  No latest/ symlink targets found."
fi

is_protected() {
  # arg: file path; return 0 if protected, 1 otherwise
  local f="$1"
  if [ -z "$PROTECTED_LIST" ]; then return 1; fi
  # exact match on full path
  printf "%s" "$PROTECTED_LIST" | awk -v x="$f" 'BEGIN{rc=1} $0==x{rc=0} END{exit rc}'
}

# ------------------ delete old MP4s ------------------
echo "Scanning MP4s in $OUT_DIR ..."
# find older than KEEP_DAYS (mtime +N means strictly older than N*24h)
# keep within OUT_DIR only (not following symlinks)
find "$OUT_DIR" -type f -name '*.mp4' -mtime +"$KEEP_DAYS" -print | while IFS= read -r f; do
  if is_protected "$f"; then
    echo "  SKIP (protected by latest/): $f"
    continue
  fi
  if [ "$DRY_RUN" = "1" ]; then
    echo "  WOULD DELETE: $f"
  else
    echo "  DELETE: $f"
    rm -f -- "$f"
  fi
done

# ------------------ delete old logs ------------------
echo "Scanning logs in $LOG_DIR ..."
find "$LOG_DIR" -type f -mtime +"$KEEP_DAYS" -print | while IFS= read -r f; do
  if [ "$DRY_RUN" = "1" ]; then
    echo "  WOULD DELETE: $f"
  else
    echo "  DELETE: $f"
    rm -f -- "$f"
  fi
done

# ------------------ clean broken symlinks in latest/ ------------------
echo "Cleaning broken symlinks in $LATEST_DIR..."
if [ -d "$LATEST_DIR" ]; then
  # list symlinks and check if their real target exists; if not, remove the symlink
  find "$LATEST_DIR" -type l -print | while IFS= read -r l; do
    real="$(
      /usr/bin/env python3 - <<'PY' "$l"
import os,sys
p=sys.argv[1]
try:
  print(os.path.realpath(p))
except Exception:
  pass
PY
    )"
    if [ -z "${real:-}" ] || [ ! -e "$real" ]; then
      if [ "$DRY_RUN" = "1" ]; then
        echo "  WOULD REMOVE BROKEN: $l -> ${real:-UNKNOWN}"
      else
        echo "  REMOVE BROKEN: $l -> ${real:-UNKNOWN}"
        rm -f -- "$l"
      fi
    fi
  done
fi

echo "Prune done."
