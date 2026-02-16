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
# i3blocks: line 1 = full_text, line 2 = short_text, line 3 = color
printf '\uf0eb\n'
printf '\uf0eb\n'
if [[ "$state" = "on" ]]; then
	echo "#e05030"
else
	echo "#888888"
fi
