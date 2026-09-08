#!/usr/bin/env bash

PIDFILE=/tmp/record_screen.pid

recording=false
if [ -f "$PIDFILE" ]; then
    pid=$(cat "$PIDFILE")
    if kill -0 "$pid" 2>/dev/null; then
        recording=true
    else
        rm -f "$PIDFILE"
    fi
fi

if [ -n "$BLOCK_BUTTON" ]; then
    if $recording; then
        kill -USR1 "$pid" 2>/dev/null
    else
        # i3-msg exec fully detaches from i3blocks, avoiding stalled polling
        i3-msg -q exec ~/.local/bin/record_screen
    fi
fi

# font awesome: f111 circle, f192 circle-dot
if $recording; then
    printf '\uf111 REC\n'
    printf '\uf111\n'
    i3blocks-color red
else
    printf '\uf192\n'
    printf '\uf192\n'
fi
