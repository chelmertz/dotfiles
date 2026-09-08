#!/bin/sh
# Temperature only. wttr's condition is an emoji and its plain-text form (%x)
# is unreadable; the curl lives here because i3blocks eats % in command= lines.
# echo adds the newline the custom format lacks; i3blocks drops a final line
# without one.
echo "$(curl -sf 'wttr.in/Partille?M&format=%t')"
