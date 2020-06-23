#!/usr/bin/env bash
set -euo pipefail

snippets="snippets:$HOME/code/github/chelmertz/rofi-blah-blah/rofi-blah-blah"
emoji="emoji:$HOME/code/github/nkoehring/rofiemoji/rofiemoji.sh"

modi="run,ssh,$emoji,$snippets"

exec rofi -sidebar-mode -show combi -combi-modi "$modi" -matching fuzzy -modi "$modi" -theme-str '#prompt {enabled: false; }'
