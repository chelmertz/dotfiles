#!/usr/bin/env bash
# One block per part (day|week|clock), so the gaps between them are the bar's
# own 14px block unit instead of literal spaces, which sat between the 5px
# label unit and the 14px block unit and read as a third rhythm. The format
# lives here because i3blocks eats % in a command= line.

[[ "$BLOCK_BUTTON" -eq 1 ]] && xdg-open "https://calendar.google.com" &>/dev/null && i3-msg workspace number 2 &>/dev/null && wmctrl -a firefox &>/dev/null

case "$1" in
	day)   date '+%a %-d %b' ;;
	week)  date '+W%V' ;;
	clock) date '+%H:%M' ;;
	*)     echo "usage: ${0##*/} day|week|clock" >&2; exit 1 ;;
esac
