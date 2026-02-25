#!/usr/bin/env bash

case $1 in
	auto)
		monitors=$(xrandr --listmonitors | grep Monitors: | cut -d' ' -f 2)
		if [ "$monitors" -gt 1 ]; then
			layout="LARGE EXTERNAL"
		else
			layout=SINGLE
		fi
		;;
	*)
		layout=$(zenity --question --text="Choose screenlayout" --switch --extra-button "LARGE EXTERNAL" --extra-button "SINGLE" --extra-button "SINGLE-BIG" --extra-button "Reconnect monitors")
		;;
esac

case $layout in
	LARGE*)
		source ~/.screenlayout/home_dual.sh
		sleep 2
		;;
	"SINGLE")
		source ~/.screenlayout/single_small.sh
		;;
	"SINGLE-BIG")
		source ~/.screenlayout/single_big.sh
		;;
	"Reconnect monitors")
		reconnect-monitor
		;;
	*)
		echo >&2 No layout chosen
		exit 1
		;;
esac

xset r rate 200 25
i3-project restore

