#!/usr/bin/env bash

set -euo pipefail

if autorandr | grep -qE 'dual-home.*\(current\)'; then
    primary="DP1"
else
    primary="eDP1"
fi

self=$(basename "$0")

logger -s -t "$self" -p warning "Replacing ~/.Xresources, in script $self"
echo "! Warning: this file is overwritten by $self" > ~/.Xresources
echo "monitor.primary: \"$primary\"" >> ~/.Xresources
echo "monitor.secondary: \"eDP1\"" >> ~/.Xresources

xrdb -merge ~/.Xresources

i3-msg restart
