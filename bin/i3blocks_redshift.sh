#!/usr/bin/env bash

state="off"
if systemctl --user is-active --quiet redshift.service; then
	state="on"
fi

toggle() {
	if [[ "$state" = "on" ]] ; then
		systemctl --user stop redshift.service
		state="off"
	else
		systemctl --user start redshift.service
		state="on"
	fi
}

[[ "$BLOCK_BUTTON" -eq 1 ]] && toggle

# font awesome lightbulb solid f0eb
echo ""

