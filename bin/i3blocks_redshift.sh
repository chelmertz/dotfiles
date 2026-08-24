#!/usr/bin/env bash

state="off"
if systemctl --user is-active --quiet redshift.service; then
	state="on"
fi

# Toggle through the same oneshot units the timers use, logging under each
# unit's own identifier, so clicks and timer runs share one journal stream:
#   journalctl --user -t redshift-disable
log() {
	printf 'i3blocks clicked: starting %s.service\n' "$1" |
		systemd-cat --identifier="$1"
}

toggle() {
	if [[ "$state" = "on" ]] ; then
		log redshift-disable
		systemctl --user start redshift-disable.service
		state="off"
	else
		log redshift-ensure
		systemctl --user start redshift-ensure.service
		state="on"
	fi
}

[[ "$BLOCK_BUTTON" -eq 1 ]] && toggle

# font awesome lightbulb solid f0eb
# i3blocks: line 1 = full_text, line 2 = short_text, line 3 = color
printf '\uf0eb\n'
printf '\uf0eb\n'
if [[ "$state" = "on" ]]; then
	echo "#e05030"
else
	echo "#888888"
fi
