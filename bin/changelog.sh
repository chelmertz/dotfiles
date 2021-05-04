#!/bin/sh
git fetch --all 1>/dev/null 2>&1
last_tag=$(git tag --sort=-v:refname | head -n1)
default_remote_ref=$(git symbolic-ref refs/remotes/origin/HEAD | sed 's|refs/remotes/||')
echo "comparing latest tag ($last_tag) with $default_remote_ref" 1>&2
git log --no-merges --pretty=tformat:%s "$last_tag".."$default_remote_ref" | sed -E -e '/^(cleanup|test):/d' -e 's/^/- /'
