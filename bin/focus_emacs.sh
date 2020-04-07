#!/bin/sh

set -x
set -e

title="emacsclient - terminal"

start_emacs() {
    gnome-terminal --title "$title" -- emacsclient -tc ~/Dropbox/orgzly/
}

pidof emacsclient >/dev/null || start_emacs
wid=$(wmctrl -l -p | grep "$title" |  cut -d' ' -f1)
test -z "$wid" && exit 1
wmctrl -i -a "$wid"
wmctrl -i -r "$wid" -b add,maximized_vert,maximized_horz
