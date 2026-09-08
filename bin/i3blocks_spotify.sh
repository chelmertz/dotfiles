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
elif [[ "$BLOCK_BUTTON" -eq 5 ]]; then
	rofi_spotify_rate.sh &
fi

prefix=""
# font awesome: f28b = pause
[ "Paused" = "$status" ] && prefix+=$(printf '\uf28b ')
# font awesome: f004 = heart (liked)
[ "$(spotify-like check 2>/dev/null)" = "1" ] && prefix+=$(printf '\uf004 ')

text="$prefix$(playerctl --player spotify metadata --format '{{artist}} - {{title}}')"
echo "$text"
echo "$text"
# a playing track is a live value; paused stays in the bar's muted colour
[ "Playing" = "$status" ] && i3blocks-color fg
exit 0
