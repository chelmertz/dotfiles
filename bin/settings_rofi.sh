#!/usr/bin/env bash

set -euo pipefail

if [ -z "$@" ]; then
    printf "autorandr\ngnome-control-center\npavucontrol\nalsamixer\narandr"
else
    case $1 in
        autorandr)
            det=$(autorandr | grep detected)
            if [ $? -eq 0 ]; then
                coproc (autorandr -l $(echo "$det" | cut -d ' ' -f1))
            fi
            ;;
        alsamixer)
            gnome-terminal -- alsamixer
            ;;
        gnome-control-center | pavucontrol | arandr)
            # see https://github.com/davatorium/rofi/issues/857
            # for hint about coproc
            coproc ("$1")
            ;;
    esac
fi
