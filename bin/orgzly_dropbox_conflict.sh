#!/usr/bin/env bash

set -euo pipefail

file=$1
test -f "$file"
mv $file{,.bak}
while [ ! -f "$file" ]; do
    echo "Force push $file from orgzly"
    sleep 10
done
meld $file{,.bak}
if diff $file{,.bak} ; then
    echo "Successfully fixed conflict, now force pull $file from orgzly"
    echo "Not cleaning up $file.bak, to protect against an accidental force push-before-pull from orgzly"
else
    echo "Fix the diff properly between $file and $file.bak"
    exit 1
fi

