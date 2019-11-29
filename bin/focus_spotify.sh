#!/bin/sh

# bind this to F7 or such

pidof spotify >/dev/null || nohup spotify > /dev/null &
wmctrl -x -a spotify
wmctrl -r spotify -b add,maximized_vert,maximized_horz
