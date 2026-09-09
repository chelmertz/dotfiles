#!/usr/bin/env bash

set -euo pipefail

if [ -z "$@" ]; then
    printf "pavucontrol\nalsamixer\narandr"
else
    case $1 in
        alsamixer)
            # gnome-terminal is Ubuntu's; tau has no GNOME. $TERMINAL is set to
            # ghostty in home.nix and works on both hosts.
            coproc ("${TERMINAL:-ghostty}" -e alsamixer)
            ;;
        pavucontrol | arandr)
            # see https://github.com/davatorium/rofi/issues/857
            # for hint about coproc
            coproc ("$1")
            ;;
    esac
fi
