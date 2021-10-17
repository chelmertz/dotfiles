#!/usr/bin/env bash

[[ "$BLOCK_BUTTON" -eq 1 ]] && xdg-open "https://calendar.google.com" &>/dev/null && i3-msg workspace number 2 && wmctrl -a Google-chrome
date '+%A 📅 %Y-%m-%d (week %V) 🕞 %H:%M:%S'
