#!/usr/bin/env bash
set -euo pipefail

cmd=$(echo -e "lock screen\nshutdown\nsuspend (low power)\nreboot\nlog out" | rofi -p 'Session action' -dmenu)

lock() {
    i3lock -c 000000 --ignore-empty-password --show-failed-attempts
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
