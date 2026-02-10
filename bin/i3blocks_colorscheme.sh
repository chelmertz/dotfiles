#!/usr/bin/env bash

current=$(gsettings get org.gnome.desktop.interface color-scheme 2>/dev/null)

# Toggle on click
if [[ "$BLOCK_BUTTON" -eq 1 ]]; then
	~/.local/bin/color-scheme >/dev/null 2>&1
	# Re-read after toggle
	current=$(gsettings get org.gnome.desktop.interface color-scheme 2>/dev/null)
fi

# font awesome: sun f185, moon f186
# i3blocks: line 1 = full_text, line 2 = short_text, line 3 = color
case "$current" in
	*dark*)
		printf '\uf186\n'
		printf '\uf186\n'
		echo "#b0c4de"
		;;
	*)
		printf '\uf185\n'
		printf '\uf185\n'
		echo "#ffcc00"
		;;
esac
