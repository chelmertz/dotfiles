#!/usr/bin/env bash

status=$(playerctl --player spotify status)

[ $? -ne 0 ] && exit 0
[ "Stopped" = "$status" ] && exit 0

if [[ "$BLOCK_BUTTON" -eq 1 ]]; then
	playerctl --player spotify previous
	[ "Paused" = "$status" ] && playerctl --player spotify play-pause
elif [[ "$BLOCK_BUTTON" -eq 2 ]]; then
	playerctl --player spotify play-pause
elif [[ "$BLOCK_BUTTON" -eq 3 ]]; then
	playerctl --player spotify next
	[ "Paused" = "$status" ] && playerctl --player spotify play-pause
elif [[ "$BLOCK_BUTTON" -eq 4 ]]; then
	spotify-like toggle &>/dev/null
fi

# font awesome: f28b = pause
[ "Paused" = "$status" ] && printf "\uf28b "

# font awesome: f004 = heart (liked)
[ "$(spotify-like check 2>/dev/null)" = "1" ] && printf "\uf004 "

playerctl --player spotify metadata --format '{{artist}} - {{title}}'
