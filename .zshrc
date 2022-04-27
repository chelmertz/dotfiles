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
alias gcaa="git commit -a --amend --no-edit"
alias gd="git diff -M -w"
alias gdw="git diff -M -w --word-diff --color-words"
alias gds="git diff --stat"
alias gco="git checkout "
alias gdc="git diff --cached -M -w"
alias gfa="git fetch --all"
alias gs="git status"
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

alias cds="cd ~/code"
alias cdd="cd ~/code/github/chelmertz/dotfiles"

alias rg="ripgrep.rg"
x() {
case $1 in
  http*|*html)
    xdg-open $1
    wmctrl -a "Google chrome"
    ;;
  *)
    xdg-open $1
    ;;
esac
}
alias bat="bat"
#alias bat="bat --theme=Coldark-Cold" #useful when terminal bg is light
batt() {
  fd "$*" | xargs bat
}

caps_turn_off() {
  python3 -c 'from ctypes import *; X11 = cdll.LoadLibrary("libX11.so.6"); display = X11.XOpenDisplay(None); X11.XkbLockModifiers(display, c_uint(0x0100), c_uint(2), c_uint(0)); X11.XCloseDisplay(display)'
}

alias docker-ps='docker ps --format "{{.Status}}\t{{.Names}}\t{{.Ports}}" -a'

compdef g='git'

# zsh on gnome-terminal doesn't support ctrl+left/right by default..?
bindkey ";5C" forward-word
bindkey ";5D" backward-word

EDITOR="nvim"
VISUAL="nvim"
PATH=${PATH}:~/bin:/usr/local/go/bin:~/.local/bin:~/go/bin:~/.emacs.d/bin:~/.cargo/bin
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

test -f ~/.shrc-local && source ~/.shrc-local

# `xset q` for viewing keypress rates
# lower the initial keypress delay:
xset r rate 200 25

[ -f ~/code/github/rupa/z/z.sh ] && source ~/code/github/rupa/z/z.sh

source ~/code/github/junegunn/fzf/shell/key-bindings.zsh
source ~/code/github/junegunn/fzf/shell/completion.zsh

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
