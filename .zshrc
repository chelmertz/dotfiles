# Path to your oh-my-zsh configuration.
ZSH=$HOME/.oh-my-zsh

# Set name of the theme to load.
# Look in ~/.oh-my-zsh/themes/
# Optionally, if you set this to "random", it'll load a random theme each
# time that oh-my-zsh is loaded.
ZSH_THEME="minimal"

# Set to this to use case-sensitive completion
# CASE_SENSITIVE="true"

# Comment this out to disable weekly auto-update checks
# DISABLE_AUTO_UPDATE="true"

# Uncomment following line if you want to disable colors in ls
# DISABLE_LS_COLORS="true"

# Uncomment following line if you want to disable autosetting terminal title.
# DISABLE_AUTO_TITLE="true"

# Uncomment following line if you want red dots to be displayed while waiting for completion
COMPLETION_WAITING_DOTS="true"

# Which plugins would you like to load? (plugins can be found in ~/.oh-my-zsh/plugins/*)
# Example format: plugins=(rails git textmate ruby lighthouse)
plugins=(brew git ant)

# Directories
alias c="cd"
alias cdd="cd ~/.config/dotfiles"
alias cds="cd ~/Sites"
alias l="ls -alh"

# Using programs
alias untar="tar xvf"
alias untargz="tar xvfz"
alias untarbz="tar yxf"

# Apps
alias v="vim"
alias m="make && ./a.out"

# Using git
alias ga="git add"
alias gb="git branch"
alias gc="git commit -s -v"
alias gca="git commit -am"
alias gd="git diff"
alias gdc="git diff --cached"
alias gl="git log"
alias glp="git log --pretty=oneline --decorate"
alias go="git checkout"
alias gs="git status"

source $ZSH/oh-my-zsh.sh
if [ -f $HOME/.zsh-local ]; then
	source $HOME/.zsh-local
fi

# Customize to your needs...
export PATH=/usr/local/bin:/usr/bin:/bin:/usr/sbin:/sbin:/usr/X11/bin:/opt/local/bin:/usr/local/git/bin
