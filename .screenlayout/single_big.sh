#!/bin/sh
. "$(dirname "$0")/_check.sh"
xrandr --output eDP-1 --off --output DP-1 --primary --mode 3440x1440 --pos 0x0 --rotate normal --output DP-2 --off --output DP-3 --off
check_layout eDP-1:off DP-1:3440x1440 DP-2:off DP-3:off
