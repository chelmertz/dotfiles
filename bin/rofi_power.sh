#!/usr/bin/env bash
set -euo pipefail

cmd=$(echo -e "lock screen\nshutdown\nreboot" | rofi -p 'Session action' -dmenu)

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
esac
