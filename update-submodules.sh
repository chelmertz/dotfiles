# quick and dirty, pretty static :)
BUNDLES_DIR="./.vim/bundle"
for DIR in `ls $BUNDLES_DIR`
do
	pushd "$BUNDLES_DIR/$DIR"
	git remote update
	git merge origin/master
	popd
done
