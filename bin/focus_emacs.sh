#!/bin/sh

# bind this to F8 or such

pidof emacs >/dev/null || nohup emacs > /dev/null &
wmctrl -x -a Emacs -b add,maximized_vert,maximized_horz
