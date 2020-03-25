#!/bin/sh

set -x
set -e

# bind this to F8 or such

title="emacsclient - terminal"

start_emacs() {
  gnome-terminal --title "$title" -- emacsclient -tc ~
}

pidof emacsclient >/dev/null || start_emacs
wid=$(wmctrl -l -p | grep "$title" |  cut -d' ' -f1)
test -z "$wid" && exit 1
wmctrl -i -a "$wid"
wmctrl -i -r "$wid" -b add,maximized_vert,maximized_horz
