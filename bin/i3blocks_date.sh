#!/usr/bin/env bash

[[ "$BLOCK_BUTTON" -eq 1 ]] && xdg-open "https://calendar.google.com" &>/dev/null && i3-msg workspace number 2 &>/dev/null && wmctrl -a firefox &>/dev/null
date '+📅 %A %d %b w:%V 🕞 %H:%M'
