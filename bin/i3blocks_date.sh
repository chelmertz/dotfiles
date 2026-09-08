#!/usr/bin/env bash

[[ "$BLOCK_BUTTON" -eq 1 ]] && xdg-open "https://calendar.google.com" &>/dev/null && i3-msg workspace number 2 &>/dev/null && wmctrl -a firefox &>/dev/null
# like the macOS clock: no icons for self-describing data
date '+%a %-d %b  W%V  %H:%M'
