#!/usr/bin/env bash
set -euo pipefail

cmd=$(echo -e "Lock screen\nShutdown\nReboot" | rofi -p 'Sesssion action' -dmenu)

case $cmd in
    "Lock screen")
        i3lock
        ;;
    Shutdown)
        poweroff
        ;;
    Reboot)
        reboot
        ;;
esac
