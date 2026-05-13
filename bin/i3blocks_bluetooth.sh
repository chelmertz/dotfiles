#!/usr/bin/env bash

MAC=00:1B:66:0E:08:E0

connected() {
	bluetoothctl info "$MAC" 2>/dev/null | grep -q 'Connected: yes'
}

if [[ "$BLOCK_BUTTON" -eq 1 ]]; then
	if connected; then
		bluetoothctl disconnect "$MAC" >/dev/null
	else
		bluetoothctl connect "$MAC" >/dev/null
	fi
fi

# font awesome bluetooth f293
# i3blocks: line 1 = full_text, line 2 = short_text, line 3 = color
printf '\n'
printf '\n'
if connected; then
	echo "#3399ff"
else
	echo "#888888"
fi
