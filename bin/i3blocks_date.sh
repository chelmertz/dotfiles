#!/usr/bin/env bash

[[ "$BLOCK_BUTTON" -eq 1 ]] && xdg-open "https://calendar.google.com" && wmctrl -a Firefox
date '+%A 📅 %Y-%m-%d (week %V) 🕞 %H:%M:%S'
