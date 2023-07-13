#!/usr/bin/env bash

status=$(playerctl --player spotify status)

[ $? -ne 0 ] && exit 0
[ "Stopped" = "$status" ] && exit 0

if [[ "$BLOCK_BUTTON" -eq 1 ]]; then
	# left click
	playerctl --player spotify previous
	[ "Paused" = "$status" ] && playerctl --player spotify play-pause
elif [[ "$BLOCK_BUTTON" -eq 2 ]]; then
	# middle click
	playerctl --player spotify play-pause
elif [[ "$BLOCK_BUTTON" -eq 3 ]]; then
	# right click
	playerctl --player spotify next
	[ "Paused" = "$status" ] && playerctl --player spotify play-pause
fi

# wmctrl -a Spotify does not work :/ so, we work around this by .. depending on i3-msg, yuck
# 
# scroll up = 4, scroll down = 5
[[ "$BLOCK_BUTTON" -eq 4 ]] && i3-msg "workspace number 10" >/dev/null
[[ "$BLOCK_BUTTON" -eq 5 ]] && i3-msg "workspace back_and_forth" >/dev/null

# font awesome: f28b
[ "Paused" = "$status" ] && echo -n " "
playerctl --player spotify metadata --format '{{artist}} - {{title}}'
