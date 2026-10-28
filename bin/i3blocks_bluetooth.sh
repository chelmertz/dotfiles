#!/usr/bin/env bash

MAC=00:1B:66:0E:08:E0

connected() {
	bluetoothctl info "$MAC" 2>/dev/null | grep -q 'Connected: yes'
}

if [[ "$BLOCK_BUTTON" -eq 1 ]]; then
	# The radio is off unless in use; see hardware.bluetooth in tau's configuration.nix.
	if connected; then
		bluetoothctl disconnect "$MAC" >/dev/null
		bluetoothctl power off >/dev/null
	else
		bluetoothctl power on >/dev/null
		bluetoothctl connect "$MAC" >/dev/null || bluetoothctl power off >/dev/null
	fi
fi

# font awesome bluetooth f293
# i3blocks: line 1 = full_text, line 2 = short_text, line 3 = color
printf '\n'
printf '\n'
connected || i3blocks-color muted
exit 0
