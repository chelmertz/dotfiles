#!/usr/bin/env bash
set -euo pipefail

modi="run,ssh,emoji:$HOME/code/github/nkoehring/rofiemoji/rofiemoji.sh,snippets:$HOME/snippets.sh" 

exec rofi -sidebar-mode -show combi -combi-modi "$modi" -matching fuzzy -modi "$modi" -theme-str '#prompt {enabled: false; }'
