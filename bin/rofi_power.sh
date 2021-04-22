#!/usr/bin/env bash
set -euo pipefail

cmd=$(echo -e "lock screen\nshutdown\nreboot\nlog out" | rofi -p 'Session action' -dmenu)

case $cmd in
    "lock screen")
        i3lock -c 000000
        ;;
    shutdown)
        poweroff
        ;;
    reboot)
        reboot
        ;;
    "log out")
        i3-msg 'exit'
        ;;
esac
