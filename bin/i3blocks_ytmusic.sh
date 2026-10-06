#!/usr/bin/env bash

# The YouTube Music app window is the only Chromium on this machine, so its
# MPRIS player (chromium.instance<pid>) is the one `--player chromium` matches.
status=$(playerctl --player chromium status)

[ $? -ne 0 ] && exit 0
[ "Stopped" = "$status" ] && exit 0

if [[ "$BLOCK_BUTTON" -eq 1 ]]; then
	playerctl --player chromium previous
	[ "Paused" = "$status" ] && playerctl --player chromium play-pause
elif [[ "$BLOCK_BUTTON" -eq 2 ]]; then
	playerctl --player chromium play-pause
elif [[ "$BLOCK_BUTTON" -eq 3 ]]; then
	playerctl --player chromium next
	[ "Paused" = "$status" ] && playerctl --player chromium play-pause
elif [[ "$BLOCK_BUTTON" -eq 4 || "$BLOCK_BUTTON" -eq 5 ]]; then
	# liking happens by hand in the app
	ytmusic &>/dev/null
fi

prefix=""
# font awesome: f28b = pause
[ "Paused" = "$status" ] && prefix+=$(printf ' ')

text="$prefix$(playerctl --player chromium metadata --format '{{artist}} - {{title}}')"
echo "$text"
echo "$text"
# a paused track is dimmed, like a toggle that is off
[ "Paused" = "$status" ] && i3blocks-color muted
exit 0
