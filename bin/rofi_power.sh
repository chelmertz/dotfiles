#!/usr/bin/env bash
set -euo pipefail

cmd=$(echo -e "lock screen\nshutdown\nsuspend (low power)\nhibernate (memory -> swap, power off)\nreboot\nlog out" | rofi -p 'Session action' -dmenu)

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
    hibernate*)
        systemctl hibernate
        ;;
    reboot)
        reboot
        ;;
    "log out")
        i3-msg 'exit'
        ;;
esac
