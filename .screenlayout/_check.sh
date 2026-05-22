#!/bin/sh
# Sourced by layout scripts after their xrandr call.
# Usage:  check_layout DP-1:3440x1440 eDP-1:1920x1200 DP-2:off DP-3:off

check_layout() {
    xrandr_status=$?
    query=$(xrandr --query)
    issues=""

    for spec in "$@"; do
        out=${spec%%:*}
        want=${spec#*:}

        block=$(printf '%s\n' "$query" | awk -v o="$out" '
            $1==o {p=1; print; next}
            p && /^[A-Za-z0-9-]+ (connected|disconnected)/ {p=0}
            p {print}
        ')
        state=$(printf '%s\n' "$block" | awk 'NR==1 {print $2}')
        active=$(printf '%s\n' "$block" | awk '/\*/ {print $1; exit}')
        edid_has_want=$(printf '%s\n' "$block" | awk -v m="$want" '$1==m {f=1} END{print f+0}')

        case "$want" in
            off)
                [ -n "$active" ] && issues="${issues}- ${out}: should be off but is active at ${active}\n"
                ;;
            *)
                if [ -z "$block" ]; then
                    issues="${issues}- ${out}: output unknown to xrandr (different machine?).\n"
                elif [ "$state" = "disconnected" ]; then
                    issues="${issues}- ${out}: not detected. Check the cable.\n"
                elif [ -z "$active" ]; then
                    issues="${issues}- ${out}: connected but no active mode. Likely hotplug/EDID glitch — unplug the cable from the monitor for ~5s and replug, then re-run.\n"
                elif [ "$active" != "$want" ]; then
                    if [ "$edid_has_want" = "0" ]; then
                        issues="${issues}- ${out}: at ${active}, wanted ${want}. Monitor's EDID doesn't expose ${want} — hotplug race? Replug cable or reboot with cable attached.\n"
                    else
                        issues="${issues}- ${out}: at ${active}, wanted ${want}.\n"
                    fi
                fi
                ;;
        esac
    done

    if [ "$xrandr_status" -ne 0 ] && [ -z "$issues" ]; then
        issues="- xrandr exited ${xrandr_status} with no obvious mismatch (see its stderr above).\n"
    fi

    if [ -n "$issues" ]; then
        script=${0##*/}
        printf 'screenlayout %s: post-check failed\n' "$script" >&2
        printf '%b' "$issues" >&2
        if command -v notify-send >/dev/null 2>&1; then
            body=$(printf '%b' "$issues")
            notify-send -u critical -- "screenlayout: $script failed" "$body"
        fi
        return 1
    fi
    return 0
}
