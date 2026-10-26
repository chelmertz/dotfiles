#!/usr/bin/env bash
set -euo pipefail

settings="settings:settings_rofi.sh"
emoji="emoji:rofimoji_copy.sh"

modi="run,$settings,$emoji,combi"

exec rofi -sidebar-mode -show combi -combi-modi "$modi" -matching fuzzy -sort -modi "$modi" -theme-str '#prompt {enabled: false; }'
