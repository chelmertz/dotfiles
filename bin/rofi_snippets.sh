#!/usr/bin/env bash
set -euo pipefail

modi="snippets:$HOME/code/github/chelmertz/rofi-blah-blah/rofi-blah-blah"

exec rofi -show snippets -matching fuzzy -modi "$modi" -theme-str '#prompt {enabled: false; }'
