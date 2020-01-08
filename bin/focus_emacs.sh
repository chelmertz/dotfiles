#!/bin/sh

set -x
set -e

# bind this to F8 or such

pidof emacs >/dev/null || ( (nohup emacs >/dev/null &) && sleep 1)
pid=$(pidof emacs)
wid=$(wmctrl -l -p | grep emacs | grep "$pid" | cut -d' ' -f1)
wmctrl -i -a "$wid"
wmctrl -i -r "$wid" -b add,maximized_vert,maximized_horz
