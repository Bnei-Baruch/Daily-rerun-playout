#!/usr/bin/env bash
set -euo pipefail
COMMENT="${1:-manual-run}"
FILENAME="${2:-capture_$(date +%Y%m%d_%H%M%S)}"

TEMPLATE="configs/sdi_dvs_38ch_ffmpeg_bb.template"
if [[ ! -f $TEMPLATE ]]; then
  echo "Template not found: $TEMPLATE" >&2
  exit 1
fi

# Substitute placeholders and execute
CMD=$(sed \
  -e "s|<COMMENT_OR_RUNID>|${COMMENT}|g" \
  -e "s|<FILENAME>|${FILENAME}|g" \
  "$TEMPLATE")

echo "==> Running capture with COMMENT='${COMMENT}' FILENAME='${FILENAME}'"
eval "$CMD"
