#!/bin/sh

# bind this to F4 or such
process=firefox
wmclass=Firefox

pidof $process >/dev/null || nohup /usr/bin/x-www-browser > /dev/null &
wmctrl -x -a $wmclass
wmctrl -r $wmclass -b add,maximized_vert,maximized_horz
