# don't put duplicate lines in the history. See bash(1) for more options
# ... or force ignoredups and ignorespace
HISTCONTROL=ignoredups:ignorespace

# append to the history file, don't overwrite it
shopt -s histappend

# for setting history length see HISTSIZE and HISTFILESIZE in bash(1)
HISTSIZE=10000
HISTFILESIZE=20000

# check the window size after each command and, if necessary,
# update the values of LINES and COLUMNS.
shopt -s checkwinsize

# make less more friendly for non-text input files, see lesspipe(1)
[ -x /usr/bin/lesspipe ] && eval "$(SHELL=/bin/sh lesspipe)"

[[ $- != *i* ]] && return
_git_cur_branch() {
	git rev-parse --abbrev-ref HEAD 2>/dev/null
}
[[ $'\n'${match_lhs} == *$'\n'"TERM "${safe_term}* ]] && use_color=true
if ${use_color} ; then
	# create a red "sad" smile if the previous command had returned an error
	gitmark='$(_git_cur_branch)'
	# Enable colors for ls, etc. Prefer ~/.dir_colors #64489
	if type -P dircolors >/dev/null ; then
		if [[ -f ~/.dir_colors ]] ; then
			eval $(dircolors -b ~/.dir_colors)
		elif [[ -f /etc/DIR_COLORS ]] ; then
			eval $(dircolors -b /etc/DIR_COLORS)
		fi
	fi
	if [[ ${EUID} == 0 ]] ; then
		PS1="\[\033[01;31m\]\h\[\033[00;33m\] \W \[\033[00;31m\]$gitmark\[\033[01;34m\]\$\[\033[00m\] "
	else
		PS1="\[\033[01;32m\]\u @ \h\[\033[00;33m\] \w \[\033[00;31m\]$gitmark\[\033[01;34m\]\$\[\033[00m\] "
	fi
	alias grep="grep --colour=auto"
else
	# create a "sad" smile if the previous command had returned an error
	gitmark='$(_git_cur_branch)'
	if [[ ${EUID} == 0 ]] ; then
		# show root@ when we do not have colors
		PS1="\h \W $gitmark\$ "
	else
		PS1="\u @ \h \w $gitmark\$ "
	fi
fi
PS2="> "
PS3="> "
PS4="+ "
# Try to keep environment pollution down, EPA loves us.
unset use_color safe_term match_lhs sadness

PROMPT_COMMAND='echo -ne "\033]0;Terminal - ${PWD##*/}\007"'

# enable color support of ls and also add handy aliases
if [ -x /usr/bin/dircolors ]; then
    test -r ~/.dircolors && eval "$(dircolors -b ~/.dircolors)" || eval "$(dircolors -b)"
    alias ls='ls --color=auto'

    alias grep='grep --color=auto'
    alias fgrep='fgrep --color=auto'
    alias egrep='egrep --color=auto'
fi

# enable programmable completion features (you don't need to enable
# this, if it's already enabled in /etc/bash.bashrc and /etc/profile
# sources /etc/bash.bashrc).
if [ -f /etc/bash_completion ] && ! shopt -oq posix; then
    . /etc/bash_completion
fi

alias rm='rm -i'
alias cp='cp -i'
alias mv='mv -i'

alias ga="git add -A "
alias gb="git branch "
alias gba="git branch --all"
alias gc="git commit -sv"
alias gca="git commit -asv"
alias gcaa="git commit -asv --amend"
alias gd="git diff -M -w"
alias gco="git checkout "
alias gdc="git diff --cached"
alias gs="git status"
alias gl="git log -p -M -w --stat --pretty=fuller --show-notes"
alias glp='git log --pretty="format:%Cred%h %Cblue%d %Cgreen%s %Creset%an %ar" --graph --all'

alias l="ls --group-directories-first -alhX"

alias ag='ag -U'
alias scrot='scrot "%Y-%m-%d_$wx$h.png" -e "mv $f ~/Pictures/screenshot/"'
alias v="gvim"
alias cds="cd ~/code"
alias cdd="cd ~/.config/dotfiles"
EDITOR=vim
VISUAL=vim
PATH=${PATH}:~/bin:~/.local/bin
export EDITOR VISUAL PATH

# start clojure with clj from a command line, or clj myfile.clj arg1 arg2
# even better: lein repl
function clj() {
	# from http://en.wikibooks.org/wiki/Learning_Clojure/Installation
	dldir="/home/chelmertz/Downloads/cloj/clojure-1.6.0"
	if [ $# -eq 0 ]; then
		java -server -cp .:${dldir}/clojure-1.6.0.jar clojure.main
	else
		java -server -cp .:${dldir}/clojure-1.6.0.jar clojure.main $1 -- "$@"
	fi
}

test -f ~/.bashrc-local && source ~/.bashrc-local
