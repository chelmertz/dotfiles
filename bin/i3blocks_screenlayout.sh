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
		# i3blocks runs this with no arguments for BOTH a click and an ordinary
		# status refresh; only a click sets BLOCK_BUTTON. Without that guard the
		# picker opened by itself whenever i3blocks started or restarted, and
		# the block rendered whatever rofi's cancelled output left behind.
		if [ -z "${BLOCK_BUTTON:-}" ]; then
			exit 0
		fi
		layout=$(echo -e "both\nlaptop only\nexternal only\nreconnect" | rofi -dmenu -p "Choose screen layout" -l 4)
		;;
esac

# The external output's name is not stable. gamma's is DP-1; on tau the same
# Dell came up as DP-3 straight into a USB-C port and as DP-1 through the dock.
# Hardcoding it meant "both" listed the live output among the ones to switch
# off, so picking "both" on tau blanked the monitor it was meant to enable.
# Its mode is read from the EDID's preferred mode rather than assumed, which
# also covers the home LG and the office Dell being different panels.
laptop="eDP-1"
query=$(xrandr --query)
external=$(printf '%s\n' "$query" | awk '/ connected/ && $1 !~ /^eDP/ {print $1; exit}')
# The laptop panel's mode and the external's are both read rather than
# assumed. gamma and tau happen to share 1920x1200, but the arithmetic below
# needs the numbers anyway, and 3440x240 was hardcoded here for exactly one
# monitor: 240 is 1440 - 1200, the offset that aligns the two bottom edges.
# The office Dell, the home LG and the panel are three different heights.
preferred_mode() {
	printf '%s\n' "$query" | awk -v o="$1" '
		$1==o {p=1; next}
		p && /^[A-Za-z0-9-]+ (connected|disconnected)/ {exit}
		p && /\+/ {print $1; exit}
	'
}
laptop_mode=$(preferred_mode "$laptop")
# A closed lid leaves eDP-1 with no preferred mode; the panel's native one is
# the only sensible guess left.
laptop_mode=${laptop_mode:-1920x1200}
external_mode=$(preferred_mode "$external")
# Every other non-eDP output, so a stale CRTC cannot survive the switch. This
# is what leaves a phantom output behind on unplug when it is skipped.
off_args=""
off_specs=""
for out in $(printf '%s\n' "$query" | awk '/(dis)?connected/ && $1 !~ /^eDP/ {print $1}'); do
	[ "$out" = "$external" ] && continue
	off_args="$off_args --output $out --off"
	off_specs="$off_specs $out:off"
done

case $layout in
	"both")
		if [ -z "$external" ]; then
			echo >&2 "screenlayout: no external output connected"
			exit 1
		fi
		# Laptop to the right of the external with their bottom edges aligned,
		# which is the arrangement gamma's matchi-dell-laptop profile used.
		ext_w=${external_mode%x*}
		ext_h=${external_mode#*x}
		laptop_h=${laptop_mode#*x}
		# Whichever panel is shorter drops by the difference, so the alignment
		# holds both ways round: the office Dell is taller than the panel, a
		# 1080p external is shorter.
		tallest=$(( ext_h > laptop_h ? ext_h : laptop_h ))
		laptop_y=$(( tallest - laptop_h ))
		ext_y=$(( tallest - ext_h ))
		xrandr --output "$laptop" --mode "$laptop_mode" --pos "${ext_w}x${laptop_y}" --rotate normal \
			--output "$external" --primary --mode "$external_mode" --pos "0x${ext_y}" --rotate normal \
			$off_args
		check_layout "$laptop:$laptop_mode" "$external:$external_mode" $off_specs
		sleep 2
		;;
	"laptop only")
		xrandr --output "$laptop" --primary --mode "$laptop_mode" --pos 0x0 --rotate normal \
			${external:+--output "$external" --off} $off_args
		;;
	"external only")
		if [ -z "$external" ]; then
			echo >&2 "screenlayout: no external output connected"
			exit 1
		fi
		xrandr --output "$laptop" --off \
			--output "$external" --primary --mode "$external_mode" --pos 0x0 --rotate normal \
			$off_args
		check_layout "$laptop:off" "$external:$external_mode" $off_specs
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
