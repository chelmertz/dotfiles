#!/bin/bash

# symlink to ~/.config/awesome/autostart.sh

# from https://wiki.archlinux.org/index.php/awesome

run() {
  if ! pgrep -f "$1"; then
    "$@"&
  fi
}

run setxkbmap -layout se -option caps:escape
run copyq
run nm-applet
run xscreensaver -no-splash
run dropbox
