#!/usr/bin/env bash
set -euo pipefail

modi="snippets:$HOME/snippets.sh" 

exec rofi -show snippets -matching fuzzy -modi "$modi" -theme-str '#prompt {enabled: false; }'
