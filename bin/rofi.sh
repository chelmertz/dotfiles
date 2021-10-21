#!/usr/bin/env bash
set -euo pipefail

settings="settings:settings_rofi.sh"
snippets="snippets:$HOME/code/github/chelmertz/rofi-blah-blah/rofi-blah-blah"
emoji="emoji:$HOME/code/github/nkoehring/rofiemoji/rofiemoji.sh"

modi="run,$settings,$emoji,$snippets,ssh"

exec rofi -sidebar-mode -show combi -combi-modi "$modi" -matching fuzzy -modi "$modi" -theme-str '#prompt {enabled: false; }'
