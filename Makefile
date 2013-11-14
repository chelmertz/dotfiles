BREWS = git \
https://raw.github.com/adamv/homebrew-alt/master/duplicates/vim.rb \
wget \
ack \
zsh \
node \
tree \
gtypist \
wkhtmltopdf

usage:
	@echo "Available commands:"
	@$(MAKE) --print-data-base --question | sed -n -e '/^Makefile/d' -e 's/^\([A-Za-z0-9_-]*\):.*/\1/p' | sort

install: brew-install
	$(MAKE) update
	ln -s /Users/chelmertz/.config/dotfiles/.gitignore_global ~/.gitignore_global
	git config --global core.excludesfile = ~/.gitignore_global

brew-install:
	brew upgrade
	for formula in $(BREWS); do \
		brew install $$formula ; \
	done;

# when mac's terminal takes forever to load, run this
faster:
	sudo rm -f /private/var/log/asl/*.asl

update:
	git pull --rebase
	git submodule foreach --recursive git pull origin master
	git submodule foreach --recursive git submodule update --init --recursive
	brew info && brew upgrade && brew update
