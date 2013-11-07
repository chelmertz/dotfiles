# Path to your oh-my-zsh configuration.
export ZSH=$HOME/.oh-my-zsh

# Set name of the theme to load.
# Look in ~/.oh-my-zsh/themes/
# Optionally, if you set this to "random", it'll load a random theme each
# time that oh-my-zsh is loaded.
ZSH_THEME="minimal"

# Set to this to use case-sensitive completion
# CASE_SENSITIVE="true"

# Comment this out to disable weekly auto-update checks
DISABLE_AUTO_UPDATE="true"

# Uncomment following line if you want to disable colors in ls
# DISABLE_LS_COLORS="true"

# Uncomment following line if you want to disable autosetting terminal title.
# DISABLE_AUTO_TITLE="true"

# Uncomment following line if you want red dots to be displayed while waiting for completion
COMPLETION_WAITING_DOTS="true"

# Which plugins would you like to load? (plugins can be found in ~/.oh-my-zsh/plugins/*)
# Example format: plugins=(rails git textmate ruby lighthouse)
plugins=(git)

source $ZSH/oh-my-zsh.sh

# Directories
alias c="cd"
alias cdd="cd ~/.config/dotfiles"
alias cds="cd ~/Sites"
# brew install coreutils:
alias l="/usr/local/Cellar/coreutils/8.21/bin/gls --color -alhs --group-directories-first"

alias v="mvim"
function treel () {
	if [ $# -eq 0 ]; then
		eval "tree | less"
	else
		eval "tree '$@' | less"
	fi
}

# Using git
alias ga="git add"
alias gb="git branch"
alias gc="git commit -sv"
alias gca="git commit -asv"
alias gcaa="git commit -asv --amend"
alias gd="git diff"
alias gdc="git diff --cached"
alias gl="git log -p -M -w" # show full diff (p), ignore annoying move file full diffs (-M), ignore whitespace changes (-w)
alias gls="git log --stat -M"
alias glp='git log --pretty="format:%Cred%h %Cblue%d %Cgreen%s %Creset%an %ar" --graph --all'
alias gco="git checkout"
alias gs="git status"
function review() {
	git push origin $(git rev-parse --abbrev-ref):refs/for/$(git rev-parse --abbrev-ref)
}

# Make mac's terminal faster
alias faster="test -d /private/var/log/asl && sudo rm -f /private/var/log/asl/*.asl"

export PATH=/usr/local/bin:/usr/bin:/bin:/usr/sbin:/sbin:/usr/X11/bin:/opt/local/bin:/usr/local/git/bin:~/bin:~/.local/bin:/usr/local/share/npm/bin
export NODE_PATH=/usr/local/lib/node:/usr/local/share/npm/lib/node_modules
export LANG=en_US.UTF-8
export LC_ALL=en_US.UTF-8

export NOSE_REDNOSE=1

if [ -f $HOME/.zsh-local ]; then
	source $HOME/.zsh-local
fi

PATH=$PATH:$HOME/.rvm/bin # Add RVM to PATH for scripting
export EDITOR="mvim -f"

export LESS_TERMCAP_mb=$'\E[01;31m'
export LESS_TERMCAP_md=$'\E[01;31m'
export LESS_TERMCAP_me=$'\E[0m'
export LESS_TERMCAP_se=$'\E[0m'
export LESS_TERMCAP_so=$'\E[01;44;33m'
export LESS_TERMCAP_ue=$'\E[0m'
export LESS_TERMCAP_us=$'\E[01;32m'
