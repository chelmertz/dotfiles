#!/bin/sh

current=$(gsettings get org.gnome.desktop.interface color-scheme)
if [ $? -ne 0 ]; then echo "Could not fetch current color-scheme" >&2; exit 1; fi
echo "Current color-scheme: $current"

case "$current" in
	"'prefer-dark'") new_color_scheme="prefer-light" ;;
	"'prefer-light'"|"'default'") new_color_scheme="prefer-dark" ;;
esac

echo "New color-scheme: $new_color_scheme"

if ! $(gsettings set org.gnome.desktop.interface color-scheme "$new_color_scheme"); then
	echo "Could not set new color-scheme" >&2
	exit 1
fi
