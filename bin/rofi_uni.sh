#!/usr/bin/env bash
set -euo pipefail

uni -q p all | rofi -dmenu -font mono\ 30  -theme-str '#prompt {enabled: false; }' | grep -o "^'.'"  | tr -d "'\n" | xsel -bi

coproc (xdotool key ctrl+v)
