#!/bin/sh

# bind this to F4 or such

pidof chrome >/dev/null || nohup /usr/bin/x-www-browser > /dev/null &
wmctrl -x -a google-chrome
wmctrl -r google-chrome -b add,maximized_vert,maximized_horz
