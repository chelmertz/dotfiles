# quick and dirty, pretty static :)
# beware, this always fetches HEAD and merges on top of master
BUNDLES_DIR="./.vim/bundle"
for DIR in `ls $BUNDLES_DIR`
do
	pushd "$BUNDLES_DIR/$DIR"
	git remote update
	git merge origin/master
	popd
done
