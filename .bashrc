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

# render man pages in colors
export LESS_TERMCAP_mb=$'\e[1;32m'
export LESS_TERMCAP_md=$'\e[1;32m'
export LESS_TERMCAP_me=$'\e[0m'
export LESS_TERMCAP_se=$'\e[0m'
export LESS_TERMCAP_so=$'\e[01;33m'
export LESS_TERMCAP_ue=$'\e[0m'
export LESS_TERMCAP_us=$'\e[1;4;31m'

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
alias gb="git branch -vv --sort=-committerdate "
alias gba="git branch --all --sort=-committerdate "
alias gc="git commit -v"
alias gca="git commit -av"
gcf() {
  git commit -a --fixup "$*"
}
git-clone-github() {
  clone_url="$1"
  user=$(echo "$clone_url" | cut -d / -f4)
  project=$(echo "$clone_url" | cut -d / -f5 | sed 's/\.git$//')
  user_dir=~/code/github/"$user"
  mkdir -p "$user_dir"
  target_dir="$user_dir"/"$project"
  git clone "$clone_url" "$target_dir"
  cd "$target_dir"
}
alias gcaa="git commit -a --amend --no-edit"
alias gd="git diff -M -w"
alias gdw="git diff -M -w --word-diff --color-words"
alias gco="git checkout "
alias gdc="git diff --cached -M -w"
alias gfa="git fetch --all"
alias gs="git status"
alias glp='git log --pretty="format:%Cred%h %Cblue%d %Cgreen%s %Creset%an %ar" --graph'
alias gls="git -c core.pager='less -p^commit.*$' log -M -w --stat --pretty=fuller --show-notes"
alias gl="git -c core.pager='less -p^commit.*$' log -p -M -w --stat --pretty=fuller --show-notes"
gll() {
  git -c core.pager='less -p^commit.*$' log -p -M -w --stat --pretty=fuller --show-notes -- $(fd $*)
}

# create an easy target to compare/rollback to after difficult rebase/merge
# etc and the reflog contains too many similar entries
alias gpre="git tag -d prebase 2>/dev/null; git tag prebase; git log -n1 prebase"
alias gp="git pull"

alias l="ls --group-directories-first -alhX --hyperlink=auto"
# order by time modified
alias ll="ls --group-directories-first -alhXtr --hyperlink=auto"
alias l1="ls -1"
alias less='less -i' # smart case search (case insensitive unless uppercase is input)

alias cds="cd ~/code"
alias cdd="cd ~/code/github/chelmertz/dotfiles"

alias rg="ripgrep.rg"
alias x="xdg-open"
alias bat="bat"
#alias bat="bat --theme=Coldark-Cold" #useful when terminal bg is light
batt() {
  fd "$*" | xargs bat
}

alias docker-ps='docker ps --format "{{.Status}}\t{{.Names}}\t{{.Ports}}" -a'

# VLC bugs out otherwise, on my Ubuntu..
alias vlc='vlc -V x11'

. /usr/share/bash-completion/completions/git
__git_complete gco _git_checkout
__git_complete gl _git_log
__git_complete glp _git_log
__git_complete gls _git_log
__git_complete gd _git_diff
__git_complete gb _git_branch

EDITOR="emacsclient -tc"
VISUAL="emacsclient -tc"
PATH=~/.cabal/bin:/usr/java/jdk1.8.0_121/jre/bin:${PATH}:~/bin:/usr/local/go/bin:~/.local/bin:~/go/bin:~/.emacs.d/bin
TZ='Europe/Stockholm'
XDG_CONFIG_HOME=~/.config
export EDITOR VISUAL PATH TZ XDG_CONFIG_HOME

# create and cd into a temp dir
cdt() {
  cd $(mktemp -d)
}

# get only keycode and keysym when pressing a button, xev is very noisy otherwise
key() {
  xev -event keyboard  | egrep -o 'keycode.*\)'
}

# unzips any archive into a newly created temporary dir
unz() {
  archive="$1"
  case "$archive" in
    http*)
      curl -O "$archive"
  esac
  cwd=$(pwd)
  if [ -f "$cwd/$archive" ]; then
    tmpdir=$(mktemp -d)
    cd "$tmpdir"
    mv "$cwd/$archive" "$tmpdir"
    case "$archive" in
      *.tar.gz|*.tar)
        tar -xf "$archive";;
      *.zip)
        unzip -q "$archive";;
    esac
    ls -alsh
  else
    echo "Invalid archive: $archive"
    return 1
  fi
}

test -f ~/.bashrc-local && source ~/.bashrc-local

# `xset q` for viewing keypress rates
# lower the initial keypress delay:
xset r rate 200 25

[ -f ~/code/github/rupa/z/z.sh ] && source ~/code/github/rupa/z/z.sh

# OPAM configuration
. ~/.opam/opam-init/init.sh >/dev/null 2>/dev/null || true

source "$HOME/.cargo/env"


source ~/code/github/junegunn/fzf/shell/key-bindings.bash
source ~/code/github/junegunn/fzf/shell/completion.bash
