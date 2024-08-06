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

alias rm='rm -i'
alias cp='cp -i'
alias mv='mv -i'

alias ga="git add -A "
alias gb="git branch -vv --sort=-committerdate "
alias gba="git branch -vv --sort=-committerdate --all"
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
alias gcaa="git commit -a --amend --no-edit --date=now"
alias gd="git diff -M -w"
alias gdd="git diff -M -w --no-ext-diff" # bypass the configured git diff display tool, which is buggy sometimes
alias gdw="git diff -M -w --word-diff --color-words"
alias gds="git diff --stat"
alias gco="git checkout "
alias gdc="git diff --cached -M -w"
alias gfa="git fetch --all"
alias gs="git status --short"
alias gr="git reflog --date=iso"
alias glp='git log --pretty="format:%Cred%h %Cblue%d %Cgreen%s %Creset%an %ar" --graph'
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

alias cdd="cd ~/code/github/chelmertz/dotfiles"

alias rg="ripgrep.rg"
x() {
case $1 in
  http*|*html)
    xdg-open $1 2>/dev/null # Chrome outputs some error that I can't act on
    wmctrl -a "Google chrome"
    ;;
  *md)
    pandoc $1 | lynx -stdin
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

# zsh on gnome-terminal doesn't support ctrl+left/right by default..?
bindkey ";5C" forward-word
bindkey ";5D" backward-word

EDITOR="vim"
VISUAL="vim"
PATH=${PATH}:~/bin:/usr/local/go/bin:~/.local/bin:~/go/bin:~/.emacs.d/bin:~/.cargo/bin:~/.yarn/bin
TZ='Europe/Stockholm'
XDG_CONFIG_HOME=~/.config
# i3-sensible-terminal relies on $TERMINAL, kgx is gnome-console,
# successor to gnome-terminal
TERMINAL=kgx
export EDITOR TERMINAL VISUAL PATH TZ XDG_CONFIG_HOME

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

[ -f ~/code/github/rupa/z/z.sh ] && source ~/code/github/rupa/z/z.sh

source ~/code/github/junegunn/fzf/shell/key-bindings.zsh
source ~/code/github/junegunn/fzf/shell/completion.zsh

# auto complete z via fzf
# https://github.com/junegunn/fzf/wiki/Examples#integration-with-z
unalias z 2> /dev/null
z() {
  [ $# -gt 0 ] && _z "$*" && return
  cd "$(_z -l 2>&1 | fzf --height 40% --nth 2.. --reverse --inline-info +s --tac --query "${*##-* }" | sed 's/^[0-9,.]* *//')"
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
source <(jj util completion --zsh)
