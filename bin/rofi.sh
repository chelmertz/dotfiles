#!/usr/bin/env bash
set -euo pipefail

settings="settings:settings_rofi.sh"
emoji="emoji:rofimoji --action copy"

modi="run,$settings,$emoji,ssh,combi"

exec rofi -sidebar-mode -show combi -combi-modi "$modi" -matching fuzzy -modi "$modi" -theme-str '#prompt {enabled: false; }'
