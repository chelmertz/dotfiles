#!/bin/sh
. "$(dirname "$0")/_check.sh"
xrandr --output eDP-1 --mode 1920x1200 --pos 3440x240 --rotate normal --output DP-1 --primary --mode 3440x1440 --pos 0x0 --rotate normal --output DP-2 --off --output DP-3 --off
check_layout eDP-1:1920x1200 DP-1:3440x1440 DP-2:off DP-3:off
