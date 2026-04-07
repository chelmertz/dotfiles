#!/usr/bin/env bash
set -euo pipefail

cmd=$(echo -e "lock screen\nshutdown\nsuspend (low power)\nreboot\nlog out" | rofi -p 'Session action' -dmenu)

lock() {
    wallpaper=$(find ~/Dropbox/wallpapers/ -name '*.png' 2>/dev/null | shuf -n 1)
    if [[ -n "$wallpaper" ]]; then
        i3lock -i "$wallpaper" --ignore-empty-password --show-failed-attempts
    else
        i3lock -c 000000 --ignore-empty-password --show-failed-attempts
    fi
}

case $cmd in
    lock*)
        lock
        ;;
    shutdown)
        poweroff
        ;;
    suspend*)
        lock
        systemctl suspend
        ;;
    reboot)
        reboot
        ;;
    "log out")
        i3-msg 'exit'
        ;;
esac
