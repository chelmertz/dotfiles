#!/usr/bin/env bash

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
		source ~/.screenlayout/home_dual.sh
		sleep 2
		;;
	"laptop only")
		source ~/.screenlayout/single_small.sh
		;;
	"external only")
		source ~/.screenlayout/single_big.sh
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

