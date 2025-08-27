#!/usr/bin/env bash

layout=$(zenity --question --text="Choose screenlayout" --switch --extra-button "LARGE EXTERNAL" --extra-button "SINGLE" --extra-button "SINGLE-BIG")

case $layout in
	LARGE*)
		source ~/.screenlayout/home_dual.sh
		# could busy-loop but..
		sleep 2

		currently_focused_workspace=$(i3-msg -t get_workspaces | jq -r '.[] | select (.focused == true).name')
		i3-msg "workspace 8, exec i3-sensible-terminal"

		if xrandr | grep -E "^DP-1 connected"; then
			# if-case just to make sure the external monitor name's
			# matches what's hardcoded below
			for i in $(seq 1 10 | grep -v 8); do
				i3-msg "workspace $i, move workspace to output DP-1"
			done
			# treat "8" as the "scratchpad for the smaller, laptop
			# display" workspace

			# works if workspace 8 is non-empty, otherwise rely on
			# catch-all
			i3-msg "workspace 8, focus"
			i3-msg "workspace 8, exec i3-sensible-terminal"
			# reload i3, otherwise cursor can get stuck with wrong DPI
			i3-msg "reload"
			# re-select the previously focused workspace
			i3-msg "workspace $currently_focused_workspace, focus"
			# if the previously focused workspace was empty, it
			# will be reintroduced on the laptop, so we'd need to
			# move it away again. the focus will follow
			i3-msg "workspace $currently_focused_workspace, move workspace to output DP-1"
		fi
		;;
	"SINGLE")
		source ~/.screenlayout/single_small.sh
		;;
	"SINGLE-BIG")
		source ~/.screenlayout/single_big.sh
		;;
	*)
		echo >&2 No layout chosen
		;;
esac

