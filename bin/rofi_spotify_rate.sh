#!/usr/bin/env bash
set -euo pipefail

current=$(spotify-like rate-check 2>/dev/null || echo 0)

# Menu order: 1,2,3,4,0 — pre-select current rating
case "$current" in
    1) sel=0; label="★" ;; 2) sel=1; label="★★" ;; 3) sel=2; label="★★★" ;; 4) sel=3; label="★★★★" ;; *) sel=4; label="☆" ;;
esac

choice=$(printf '★\n★★\n★★★\n★★★★\n☆' | rofi -dmenu -p "Rate ($label)" -selected-row "$sel" -a "$sel" \
    -theme-str 'window { location: north east; anchor: north east; width: 200px; }') || exit 0

case "$choice" in
    '★') level=1 ;; '★★') level=2 ;; '★★★') level=3 ;; '★★★★') level=4 ;; '☆') level=0 ;; *) exit 0 ;;
esac

spotify-like rate-set "$level" &>/dev/null
pkill -SIGRTMIN+10 i3blocks
