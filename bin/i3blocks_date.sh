#!/usr/bin/env bash

[[ "$BLOCK_BUTTON" -eq 1 ]] && xdg-open "https://calendar.google.com" &>/dev/null && i3-msg workspace number 2 &>/dev/null && wmctrl -a Google-chrome &>/dev/null
date '+%A 📅 %Y-%m-%d (week %V) 🕞 %H:%M:%S'
