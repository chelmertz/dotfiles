#!/usr/bin/env bash

set -euo pipefail

if [ -z "$@" ]; then
    printf "gnome-control-center\npavucontrol"
else
    case $1 in
        gnome-control-center | pavucontrol)
            # see https://github.com/davatorium/rofi/issues/857
            # for hint about coproc
            coproc ("$1")
            ;;
    esac
fi
