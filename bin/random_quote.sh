#!/bin/sh
cut -d'#' -f1 ~/Dropbox/quotes.txt | sed -e 's/^ *//g' -e 's/ *$//g' -e '/^$/d' | shuf -n1
