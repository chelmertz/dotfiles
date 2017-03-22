# don't put duplicate lines in the history. See bash(1) for more options
export HISTCONTROL=erasedups:ignorespace

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

[[ $PS1 && -f /usr/share/bash-completion/bash_completion ]] && \
    . /usr/share/bash-completion/bash_completion


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
alias gdc="git diff --cached -M"
alias gs="git status"
alias gl="git log -p -M -w --stat --pretty=fuller --show-notes"
alias glp='git log --pretty="format:%Cred%h %Cblue%d %Cgreen%s %Creset%an %ar" --graph --all'

alias l="ls --group-directories-first -alhX"
alias less='less -i' # smart case search (case insensitive unless uppercase is input)

alias til='nvim ~/Dropbox/tagspaces/til.md'
alias tilv='~/.config/dotfiles/bin/til.sh && evince ~/til.pdf'
alias ag='ag -U'
alias scrot='scrot "%Y-%m-%d_$wx$h.png"'
alias v="vim"
alias cds="cd ~/code"
alias cdd="cd ~/.config/dotfiles"
EDITOR=nvim
VISUAL=nvim
PATH=~/.cabal/bin:/usr/java/jdk1.8.0_121/jre/bin:${PATH}:~/bin:~/.local/bin
TZ='Europe/Stockholm'
export EDITOR VISUAL PATH TZ

test -f ~/.bashrc-local && source ~/.bashrc-local

# `xset q` for viewing keypress rates
# lower the initial keypress delay:
xset r rate 200 25

[ -f ~/.fzf.bash ] && source ~/.fzf.bash
