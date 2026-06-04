#!/usr/bin/env bash

# Post-apply verification. Compares xrandr state against what was requested,
# reports mismatches on stderr + notify-send. Call right after the xrandr line.
# Usage: check_layout DP-1:3440x1440 eDP-1:1920x1200 DP-2:off DP-3:off
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
		printf 'screenlayout %s: post-check failed\n' "$layout" >&2
		printf '%b' "$issues" >&2
		if command -v notify-send >/dev/null 2>&1; then
			body=$(printf '%b' "$issues")
			notify-send -u critical -- "screenlayout: $layout failed" "$body"
		fi
		return 1
	fi
	return 0
}

case $1 in
	auto)
		monitors=$(xrandr --listmonitors | grep Monitors: | cut -d' ' -f 2)
		if [ "$monitors" -gt 1 ]; then
			layout="both"
		else
			layout="laptop only"
		fi
		;;
	*)
		layout=$(echo -e "both\nlaptop only\nexternal only\nreconnect" | rofi -dmenu -p "Choose screen layout" -l 4)
		;;
esac

case $layout in
	"both")
		xrandr --output eDP-1 --mode 1920x1200 --pos 3440x240 --rotate normal --output DP-1 --primary --mode 3440x1440 --pos 0x0 --rotate normal --output DP-2 --off --output DP-3 --off
		check_layout eDP-1:1920x1200 DP-1:3440x1440 DP-2:off DP-3:off
		sleep 2
		;;
	"laptop only")
		xrandr --output eDP-1 --primary --mode 1920x1200 --pos 0x0 --rotate normal --output DP-1 --off --output DP-2 --off --output DP-3 --off
		;;
	"external only")
		xrandr --output eDP-1 --off --output DP-1 --primary --mode 3440x1440 --pos 0x0 --rotate normal --output DP-2 --off --output DP-3 --off
		check_layout eDP-1:off DP-1:3440x1440 DP-2:off DP-3:off
		;;
	"reconnect")
		reconnect-monitor
		;;
	*)
		echo >&2 No layout chosen
		exit 1
		;;
esac

xset r rate 200 25
i3-project restore
