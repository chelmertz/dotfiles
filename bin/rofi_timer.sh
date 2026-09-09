#!/usr/bin/env bash
# The freedesktop sound theme lives under /usr/share on Ubuntu and under
# /run/current-system/sw/share on NixOS. Take whichever exists.
# Seeded rather than left unset: `set -u` below would abort the timer at the
# moment it fires if neither directory existed.
SOUNDS=/usr/share/sounds/freedesktop/stereo
for d in "$SOUNDS" /run/current-system/sw/share/sounds/freedesktop/stereo; do
    [ -d "$d" ] && SOUNDS="$d" && break
done

set -euo pipefail
#set -x

seconds=$(echo -e "10\n30\n60\n1500" | rofi -dmenu -width 20 -lines 4 -p "Set a timer for X seconds")
initial=$seconds

format_seconds() {
    date +%T -d "1970-01-01 + $1 seconds"
}

[ "$seconds" -lt 1 ] && exit 1
an_id="$RANDOM"
timer_name=${1:-$(echo "default" | rofi -dmenu -width 20 -l 1 -p "Name your timer")}

# TODO dynamic icon/colored circle, representing time left
d() {
    secs=$1
    urg=normal
    if [ "$seconds" -lt 6 ]; then
        urg=critical
        paplay "$SOUNDS/bell.oga"
    fi
    sec_string=$(format_seconds "$secs")
    code=$(dunstify --appname "Timer" --urgency="$urg" --timeout 1000 --block --replace="$an_id" "$timer_name $sec_string")
    if [ "$code" = 2 ]; then
        dunstify --close="$an_id"
        exit
    fi
}

d "$seconds"

while [[ $seconds -gt 0 ]]; do
    #sleep 1
    seconds=$((seconds - 1))
    if [ "$seconds" -eq 0 ]; then
        dunstify --appname "Timer" --replace="$an_id" --urgency=critical "Timer '$timer_name' done, $initial seconds passed" "$(date)"
        paplay "$SOUNDS/complete.oga"
        if [ "$timer_name" != "default" ]; then
            spd-say "$timer_name"
        fi
    else
        d $seconds
    fi
done

