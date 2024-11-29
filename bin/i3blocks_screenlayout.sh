#!/usr/bin/env bash

layout=$(zenity --question --text="Choose screenlayout" --switch --extra-button "work" --extra-button "home" --extra-button "single")

case $layout in
	"home")
		source ~/.screenlayout/home_dual.sh
		;;
	"work")
		source ~/.screenlayout/work_dual.sh
		;;
	"single")
		source ~/.screenlayout/single_small.sh
		;;
	*)
		echo >&2 No layout chosen
		;;
esac

