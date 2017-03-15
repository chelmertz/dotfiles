#!/bin/sh

pandoc --toc \
	--reference-links \
	--chapter \
	--number-sections \
	--variable colorlinks \
	--from markdown+abbreviations+inline_notes+footnotes \
	--to latex \
	--output "$HOME/til.pdf" \
	"$HOME/Dropbox/tagspaces/til.md"
