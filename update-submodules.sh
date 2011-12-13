#!/usr/bin/env bash
# quick and dirty, pretty static :)
# beware, this always fetches HEAD and merges on top of master

function init_submodules() {
	git submodule init 1> /dev/null
}

function update_submodules() {
	git submodule update 1> /dev/null
	BUNDLES_DIR="./.vim/bundle"
	for DIR in `ls $BUNDLES_DIR`
	do
		pushd "$BUNDLES_DIR/$DIR" 1> /dev/null
		git remote update 1> /dev/null
		git merge origin/master 1> /dev/null
		popd 1> /dev/null
	done
}

[[ $(init_submodules && update_submodules) -eq 0 ]] && RESULT="success" || RESULT="failure"
echo $RESULT
