#!/bin/sh

# bind this to F8 or such

pidof emacs26 >/dev/null || nohup emacs26 > /dev/null &
wmctrl -x -a Emacs
wmctrl -r Emacs -b add,maximized_vert,maximized_horz
