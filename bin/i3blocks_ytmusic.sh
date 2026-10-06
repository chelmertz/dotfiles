#!/usr/bin/env bash

# The YouTube Music app window is the only Chromium on this machine, so its
# MPRIS player (chromium.instance<pid>) is the one `--player chromium` matches.
status=$(playerctl --player chromium status)

[ $? -ne 0 ] && exit 0
[ "Stopped" = "$status" ] && exit 0

# Chromium keeps reporting the old status for ~0.1s after a skip, then Stopped
# with no metadata for ~0.4s while it changes track. Printing in that gap
# empties the block until the next interval, so wait for the gap to open
# (it never does when previous only restarts the track) and then to close.
skip() {
	playerctl --player chromium "$1"
	for _ in {1..10}; do
		sleep 0.05
		[ "Stopped" = "$(playerctl --player chromium status 2>/dev/null)" ] && break
	done
	for _ in {1..40}; do
		status=$(playerctl --player chromium status 2>/dev/null)
		[[ -n "$status" && "Stopped" != "$status" ]] && break
		sleep 0.05
	done
	# play, not play-pause: the app may already have started the new track
	[ "Paused" = "$status" ] && playerctl --player chromium play && status=Playing
}

if [[ "$BLOCK_BUTTON" -eq 1 ]]; then
	skip previous
elif [[ "$BLOCK_BUTTON" -eq 2 ]]; then
	playerctl --player chromium play-pause
elif [[ "$BLOCK_BUTTON" -eq 3 ]]; then
	skip next
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
