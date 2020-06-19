#!/usr/bin/env bash
set -euo pipefail

modi="emoji:$HOME/code/github/nkoehring/rofiemoji/rofiemoji.sh"

# the default -font (too small), -columns (only 1) and -lines (too few)
# makes it hard to differentiate emoji
exec rofi -font mono\ 30 -columns 4 -lines 10 -show emoji -matching fuzzy -modi "$modi" -theme-str '#prompt {enabled: false; }'
