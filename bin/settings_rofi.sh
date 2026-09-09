#!/usr/bin/env bash

set -euo pipefail

if [ -z "$@" ]; then
    printf "gnome-control-center\npavucontrol\nalsamixer\narandr"
else
    case $1 in
        alsamixer)
            # gnome-terminal is Ubuntu's; tau has no GNOME. $TERMINAL is set to
            # ghostty in home.nix and works on both hosts.
            coproc ("${TERMINAL:-ghostty}" -e alsamixer)
            ;;
        gnome-control-center | pavucontrol | arandr)
            # see https://github.com/davatorium/rofi/issues/857
            # for hint about coproc
            coproc ("$1")
            ;;
    esac
fi
