#!/usr/bin/env bash
DATA_DIR="/home/runner/workspace/data"
LIMIT_GB=50
USED_GB=$(du -sBG "$DATA_DIR" 2>/dev/null | cut -f1 | tr -d 'G' || echo 0)
AVAIL_GB=$(df -BG "$DATA_DIR" 2>/dev/null | awk 'NR==2{print $4}' | tr -d 'G' || echo 0)
echo "Storage report — $(date)"
echo "  Data directory : $DATA_DIR"
echo "  Used           : ${USED_GB} GB"
echo "  Available      : ${AVAIL_GB} GB"
echo "  Limit          : ${LIMIT_GB} GB"
if [[ "${AVAIL_GB}" -lt 5 ]]; then
  echo "  WARNING: Less than 5 GB remaining — node may stop soon!"
fi
