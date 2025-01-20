#!/usr/bin/env bash

state="off"
if [[ -n $(pidof redshift) ]]; then
	state="on"
fi

toggle() {
	if [[ "$state" = "on" ]] ; then
		killall redshift
		state="off"
	else
		bgstart redshift 2>/dev/null
		state="on"
	fi
}

[[ "$BLOCK_BUTTON" -eq 1 ]] && toggle

# font awesome lightbulb solid f0eb
echo ""

