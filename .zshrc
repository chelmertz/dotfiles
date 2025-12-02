setopt APPEND_HISTORY # append to the history file, don't overwrite it
setopt HIST_IGNORE_ALL_DUPS # do not put duplicated command into history list
setopt HIST_SAVE_NO_DUPS # do not save duplicated command
setopt HIST_REDUCE_BLANKS # remove unnecessary blanks
setopt INC_APPEND_HISTORY_TIME # append command to history file immediately after execution
setopt EXTENDED_HISTORY # record command start time
setopt SHARE_HISTORY # save history

setopt interactive_comments # as default in bash: allows writing $ echo hello # a comment here

export HISTFILE=~/.zsh_history
export HISTSIZE=10000
export SAVEHIST=10000
setopt appendhistory

# render man pages in colors
export LESS_TERMCAP_mb=$'\e[1;32m'
export LESS_TERMCAP_md=$'\e[1;32m'
export LESS_TERMCAP_me=$'\e[0m'
export LESS_TERMCAP_se=$'\e[0m'
export LESS_TERMCAP_so=$'\e[01;33m'
export LESS_TERMCAP_ue=$'\e[0m'
export LESS_TERMCAP_us=$'\e[1;4;31m'
# the above worked in Ubuntu 22.04 but something broke "in" 24.04 and this workaround does it for me
# also see https://github.com/jedsoft/most/issues/18
export GROFF_NO_SGR=1

alias rm='rm -i'
alias cp='cp -i'
alias mv='mv -i'


# fd's colors do not match any light wezterm theme (tried 5+)
alias fd="fd --color never"
alias g=lazygit
alias ga="git add -A "
alias gba="git branch -vv --sort=-committerdate --all"
alias gc="git commit -v"
alias gca="git commit -av"
gcf() {
  git commit -a --fixup "$*"
}
gt() {
	# getting tags: https://stackoverflow.com/a/71690022
	# output format: https://stackoverflow.com/a/21444068 (minus --graph)
	git tag --format='%(objectname)^{}' | git cat-file --batch-check | awk '$2=="commit" { print $1 }' | git log --stdin --author-date-order --no-walk --oneline --pretty=format:'%C(auto)%h%d%Creset %C(cyan)(%cr)%Creset %C(green)%cn <%ce>%Creset %s'
}
gb() {
	git branch --format='%(objectname)^{}' | git cat-file --batch-check | awk '$2=="commit" { print $1 }' | git log --stdin --author-date-order --no-walk --oneline --pretty=format:'%C(auto)%h%d%Creset %C(cyan)(%cr)%Creset %C(green)%cn <%ce>%Creset %s'
}
gf() {
# which files did an author touch in a repo?
	local author
	author=$1
	git log --no-merges --author="$author" --name-only --pretty=format:"" | sort -u
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
  git remote rename origin upstream
}
alias gcaa="git commit -a --amend --no-edit --date=now"
alias gd="git diff -M -w"
alias gdd="git diff -M -w --no-ext-diff" # bypass the configured git diff display tool, which is buggy sometimes
alias gdw="git diff -M -w --word-diff --color-words"
alias gds="git diff --stat"
alias gco="git checkout "
alias gdc="git diff --cached -M -w"
alias gfa="git fetch --all"
alias gs="git status --short --branch"
alias gr="git reflog --date=iso"

# stolen from https://registerspill.thorstenball.com/p/how-i-use-git
HASH="%C(always,yellow)%h%C(always,reset)"
RELATIVE_TIME="%C(always,green)%ar%C(always,reset)"
AUTHOR="%C(always,bold blue)%an%C(always,reset)"
REFS="%C(always,red)%d%C(always,reset)"
SUBJECT="%s"

FORMAT="$HASH $RELATIVE_TIME{$AUTHOR{$REFS $SUBJECT"

glp() {
  git log --graph --pretty="tformat:$FORMAT" $* | column -t -s '{' | less -XRS --quit-if-one-screen
}
#alias glp='git log --pretty="format:%Cred%h %Cblue%d %Cgreen%s %Creset%an %ar" --graph'
alias gls="git -c core.pager='less -p^commit.*$' log -M -w --stat --pretty=fuller --show-notes"
alias gl="git -c core.pager='less -p^commit.*$' log -p -M -w --stat --pretty=fuller --show-notes"
gll() {
  git -c core.pager='less -p^commit.*$' log -p -M -w --stat --pretty=fuller --show-notes -- $(fd $*)
}
glf() {
  gl $(fd $*)
}

gtodo() {
	# https://stackoverflow.com/a/25042219 with one more sed, to allow ctrl+click in vscode's terminal
	git grep -l TODO | xargs -n1 git blame -f -n -w | grep "$(git config user.name)" | grep TODO | sed "s/.\{9\}//" | sed "s/(.*)[[:space:]]*//" | sed "s/ \+/:/"
}

# create an easy target to compare/rollback to after difficult rebase/merge
# etc and the reflog contains too many similar entries
alias gpre="git tag -d prebase 2>/dev/null; git tag prebase; git log -n1 prebase"
alias gp="git pull"

alias l="ls --group-directories-first -alhX --hyperlink=auto"
# order by time modified
alias ll="ls --group-directories-first -alhXtr --hyperlink=auto"
alias l1="ls -1"
# -i smart case search (case insensitive unless uppercase is input)
# -R color ascii
alias less='less -iR'

lc() {
	# pass a dir or just default to current dir
	local dir="${1:-.}"
	# find needs a trailing slash of dirs, add it with sed if not present
	#
	# redirect xargs stderr to hide "terminated by signal 13" error
	#
	# less: highlight headers from "head" and colorize the search result
	# "S" with blac"k" background and "m"agenta (pink) foreground
	find $(echo "$dir" | sed -e 's#[^/]$#&/#') -type f | xargs head -n99 2>/dev/null | less -p '==> .* <==' --use-color --color=Skm
}

grony() {
	# like jq but for yml files
	yq eval -o=p $*
}

e() {
	$EDITOR $*
}

alias cdd="cd ~/code/github/chelmertz/dotfiles"

# show hidden files by default, case in point: ./github/workflows/*
# smart case = ignore case if input is lowercase, otherwise follow casing
alias rg="snap run ripgrep.rg --hidden --smart-case --glob='!**/.git/**'"
x() {
case $1 in
  http*|*html)
    xdg-open $1 2>/dev/null # Chrome outputs some error that I can't act on
    wmctrl -a "Firefox"
    ;;
  *md) #markdown
    treemd $1
    ;;
  *)
    xdg-open $1
    ;;
esac
}
alias bat="bat --theme=base16"
batt() {
  fd "$*" | xargs bat
}

alias docker-ps='docker ps --format "{{.Status}}\t{{.Names}}\t{{.Ports}}" -a'

compdef g='git'
compdef _files pbcopy

# zsh on gnome-terminal doesn't support ctrl+left/right by default..?
bindkey ";5C" forward-word
bindkey ";5D" backward-word
bindkey "^ " autosuggest-accept # ctrl+space to accept, instead of right arrow

EDITOR="vim"
VISUAL="vim"
PATH=${PATH}:~/bin:/usr/local/go/bin:~/.local/bin:~/go/bin:~/.emacs.d/bin:~/.cargo/bin:~/.yarn/bin
TZ='Europe/Stockholm'
XDG_CONFIG_HOME=~/.config
export EDITOR VISUAL PATH TZ XDG_CONFIG_HOME

# create and cd into a temp dir
cdt() {
  cd $(mktemp -d)
}

mk() {
  mkdir -p "$1" && cd "$1"
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
      *.tar.gz|*.tar|*.tgz)
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

test -f ~/.shrc-local && source ~/.shrc-local

# `xset q` for viewing keypress rates
# lower the initial keypress delay:
xset r rate 200 25
# disable power saving for external monitors (this is *so* the wrong place to do this)
# see https://wiki.archlinux.org/title/Display_Power_Management_Signaling
xset s off -dpms

[ -f ~/.fzf.zsh ] && source ~/.fzf.zsh

[ -f ~/code/github/rupa/z/z.sh ] && source ~/code/github/rupa/z/z.sh

# auto complete z via fzf
# https://github.com/junegunn/fzf/wiki/Examples#integration-with-z
unalias z 2> /dev/null
z() {
  [ $# -gt 0 ] && _z "$*" && return
  cd "$(_z -l 2>&1 | fzf --height 40% --nth 2.. --reverse --inline-info +s --tac --query "${*##-* }" | sed 's/^[0-9,.]* *//')"
}

# pkill via fzf's fuzzy auto completion
# via https://github.com/gessen/zsh-fzf-kill/blob/master/fzf-kill.plugin.zsh
pk() {
  local pid
  if [[ "${UID}" != "0" ]]; then
    pid=$(ps -f -u ${UID} | sed 1d | fzf -m | awk '{print $2}')
  else
    pid=$(ps -ef | sed 1d | fzf -m | awk '{print $2}')
  fi

  if [[ "x$pid" != "x" ]]; then
    echo $pid | xargs kill "-${1:-9}"
  fi
  zle reset-prompt
}

# Enable Powerlevel10k instant prompt. Should stay close to the top of ~/.zshrc.
# Initialization code that may require console input (password prompts, [y/n]
# confirmations, etc.) must go above this block; everything else may go below.
if [[ -r "${XDG_CACHE_HOME:-$HOME/.cache}/p10k-instant-prompt-${(%):-%n}.zsh" ]]; then
  source "${XDG_CACHE_HOME:-$HOME/.cache}/p10k-instant-prompt-${(%):-%n}.zsh"
fi


#THIS MUST BE AT THE END OF THE FILE FOR SDKMAN TO WORK!!!
export SDKMAN_DIR="$HOME/.sdkman"
[[ -s "$HOME/.sdkman/bin/sdkman-init.sh" ]] && source "$HOME/.sdkman/bin/sdkman-init.sh"
source ~/powerlevel10k/powerlevel10k.zsh-theme

# To customize prompt, run `p10k configure` or edit ~/.p10k.zsh.
[[ ! -f ~/.p10k.zsh ]] || source ~/.p10k.zsh
source ~/github/romkatv/powerlevel10k/powerlevel10k.zsh-theme
source ~/code/github/zsh-users/zsh-autosuggestions/zsh-autosuggestions.zsh

source ~/code/github/chelmertz/dotfiles/zsh-scripts/_gh
compdef _gh gh

# bun completions
[ -s "/home/ch/.bun/_bun" ] && source "/home/ch/.bun/_bun"

# bun
export BUN_INSTALL="$HOME/.bun"
export PATH="$BUN_INSTALL/bin:$PATH"

# jj (jujutsu, vcs alternative to git)
autoload -U compinit
compinit
source <(jj util completion zsh)

# alias (ish) ctrl+z to fg, so that ctrl+z toggles a program's suspendedness
# https://www.reddit.com/r/vim/comments/9bm3x0/ctrlz_binding/
function Resume {
	# avoid the unnecessary error messages when nothing is suspended
	[[ "$(jobs | wc -l)" == "0" ]] && return
	fg
	zle push-input
	BUFFER=""
	zle accept-line
}
zle -N Resume
bindkey "^Z" Resume

# edit current command in vim
autoload -U edit-command-line # -U to not expand my aliases inside the widget iiuc
zle -N edit-command-line # -N to define a new zle widget
bindkey "^X" edit-command-line # ctrl+x
# some useful bindkey commands
# bindkey -l for listing keymaps
# bindkey -M main for listing keybinds for a keymap
# zle -al for listing all commands

# nvm (node version manager)
export NVM_DIR="$HOME/.config/nvm"
[ -s "$NVM_DIR/nvm.sh" ] && \. "$NVM_DIR/nvm.sh"  # This loads nvm
[ -s "$NVM_DIR/bash_completion" ] && \. "$NVM_DIR/bash_completion"  # This loads nvm bash_completion

source ~/.zsh-matchi
alias ml="matchi-cli links"
export MATCHI_GIT_REPO_PATH=~/code/matchi

# Hishtory Config:
export PATH="$PATH:/home/ch/.hishtory"
source /home/ch/.hishtory/config.zsh
export PATH=$PATH:$HOME/.maestro/bin

eval "$(/home/linuxbrew/.linuxbrew/bin/brew shellenv)"

