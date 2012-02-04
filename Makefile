BREWS = git \
https://raw.github.com/adamv/homebrew-alt/master/duplicates/vim.rb \
wget \
ack \
zsh \
node \
tree \
wkhtmltopdf

SYMLINKS = bla

usage:
	@echo "Available commands:"
	@$(MAKE) --print-data-base --question | sed -n -e '/^Makefile/d' -e 's/^\([A-Za-z0-9_-]*\):.*/\1/p' | sort

install: brew-install
	$(MAKE) update

brew-install:
	brew upgrade
	for formula in $(BREWS); do \
		brew install $$formula ; \
	done;

symlinks:


update:
	git pull
	git submodule foreach git pull origin master 1> /dev/null
	brew info && brew upgrade && brew update
