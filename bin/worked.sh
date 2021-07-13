#!/usr/bin/env bash
set -euo pipefail

out=$(sqlite3 -separator '' ~/code/github/chelmertz/track_hours/work.sqlite3 "select 'Work: ' || cast(count(*)/60 as text) || ' h ', cast(count(*)%60 as text) || ' min' from minutes where year = strftime('%Y', 'now', 'localtime') and month = strftime('%m', 'now', 'localtime') and day = strftime('%d', 'now', 'localtime')")
if [ "$out" != "Work: 0 h 0 min" ]; then
    echo "$out"
fi
