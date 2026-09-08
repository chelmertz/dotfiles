#!/usr/bin/env bash

[[ "$BLOCK_BUTTON" -eq 1 ]] && xdg-open "https://calendar.google.com" &>/dev/null && i3-msg workspace number 2 &>/dev/null && wmctrl -a firefox &>/dev/null
# font awesome: f133 calendar, f017 clock. The one block that is always fg.
text=$(date "+$(printf '\uf133') %A %d %b w:%V  $(printf '\uf017') %H:%M")
echo "$text"
echo "$text"
i3blocks-color fg
