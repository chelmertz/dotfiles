#!/usr/bin/env bash
# i3blocks toggle for keylog capture — click to start a 30-min session, click
# again to stop. Mirrors i3blocks_recording.sh.

# binary is `keylog` or (if installed via `go install`) `keylogger`
KEYLOG="$(command -v keylog || command -v keylogger || echo "$HOME/go/bin/keylogger")"
PIDFILE="$HOME/.local/share/keylog/keylog.pid"

capturing=false
if [ -f "$PIDFILE" ]; then
    pid=$(cut -d' ' -f1 "$PIDFILE")
    if kill -0 "$pid" 2>/dev/null; then
        capturing=true
    fi
fi

if [ -n "$BLOCK_BUTTON" ]; then
    if $capturing; then
        "$KEYLOG" stop
    else
        # i3-msg exec fully detaches from i3blocks, avoiding stalled polling
        i3-msg -q exec "$KEYLOG start --duration 30m"
    fi
fi

# font awesome: f11c keyboard
if $capturing; then
    printf '\uf11c REC\n'
    printf '\uf11c\n'
    i3blocks-color red
else
    printf '\uf11c\n'
    printf '\uf11c\n'
fi
