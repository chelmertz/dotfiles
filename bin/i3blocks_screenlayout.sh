#!/usr/bin/env bash

layout=$(zenity --question --text="Choose screenlayout" --switch --extra-button "WORK" --extra-button "HOME" --extra-button "SINGLE")

case $layout in
	"HOME")
		source ~/.screenlayout/home_dual.sh
		;;
	"WORK")
		source ~/.screenlayout/work_dual.sh
		;;
	"SINGLE")
		source ~/.screenlayout/single_small.sh
		;;
	*)
		echo >&2 No layout chosen
		;;
esac

