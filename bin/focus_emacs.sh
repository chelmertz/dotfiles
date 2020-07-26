#!/bin/sh

set -xe

title="Doom Emacs"

wid=$(wmctrl -l -p | grep "$title" |  cut -d' ' -f1)
test -z "$wid" && exec /home/ch/.emacs.d/bin/doom run
wmctrl -i -a "$wid" # -a = focus window
