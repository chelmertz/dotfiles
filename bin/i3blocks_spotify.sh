#!/usr/bin/env bash

# left click
[[ "$BLOCK_BUTTON" -eq 1 ]] && playerctl --player spotify previous
# middle click
[[ "$BLOCK_BUTTON" -eq 2 ]] && playerctl --player spotify play-pause
# right click
[[ "$BLOCK_BUTTON" -eq 3 ]] && playerctl --player spotify next

# wmctrl -a Spotify does not work :/ so, we work around this by .. depending on i3-msg, yuck
# 
# scroll up = 4, scroll down = 5
[[ "$BLOCK_BUTTON" -eq 4 ]] && i3-msg "workspace number 10" >/dev/null
[[ "$BLOCK_BUTTON" -eq 5 ]] && i3-msg "workspace back_and_forth" >/dev/null

playerctl --player spotify metadata --format '{{artist}} - {{title}}'
