{ pkgs, lib, ... }:
{
  # Powerlevel10k config file
  home.file.".p10k.zsh".source = ./p10k.zsh;

  programs.zsh = {
    enable = true;

    plugins = [
      {
        name = "powerlevel10k";
        src = pkgs.zsh-powerlevel10k;
        file = "share/zsh-powerlevel10k/powerlevel10k.zsh-theme";
      }
      {
        name = "zsh-z";
        src = pkgs.zsh-z;
        file = "share/zsh-z/zsh-z.plugin.zsh";
      }
    ];

    history = {
      append = true;
      extended = true;
      ignoreDups = true;
      ignoreAllDups = true;
      share = true;
      path = "$HOME/.zsh_history";
      size = 10000;
      save = 10000;
    };

    autosuggestion = {
      enable = true;
      highlight = "fg=#888888";  # grey that should be visible on both light/dark
    };
    syntaxHighlighting.enable = true;

    shellAliases = {
      rm = "rm -i";
      cp = "cp -i";
      mv = "mv -i";

      # fd: no colors (don't match light themes), show hidden but exclude .git
      fd = "fd --color never --hidden --exclude '.git/'";
      g = "lazygit";
      ga = "git add -A ";
      gba = "git branch -vv --sort=-committerdate --all";
      gc = "git commit -v";
      gca = "git commit -av";
      gcaa = "git commit -a --amend --no-edit --date=now";
      gd = "git diff -M -w";
      gdd = "git diff -M -w --no-ext-diff";
      gdw = "git diff -M -w --word-diff --color-words";
      gds = "git diff --stat";
      gco = "git checkout ";
      gdc = "git diff --cached -M -w";
      gfa = "git fetch --all";
      gs = "git status --short --branch";
      gr = "git reflog --date=iso";
      gpre = "git tag -d prebase 2>/dev/null; git tag prebase; git log -n1 prebase";
      gp = "git pull";
      gls = "git log -M -w --stat --pretty=fuller --show-notes";
      gl = "git log -p -M -w --stat --pretty=fuller --show-notes";

      l = "ls --group-directories-first -alhX --hyperlink=auto";
      ll = "ls --group-directories-first -alhXtr --hyperlink=auto";
      l1 = "ls -1";
      less = "less -iR --use-color";

      cdd = "cd ~/code/github/chelmertz/dotfiles";
      rg = "rg --hidden --smart-case --glob='!**/.git/**'";
      bat = "bat --theme=base16";

      docker-ps = ''docker ps --format "{{.Status}}\t{{.Names}}\t{{.Ports}}" -a'';

      ml = "matchi-cli links";
      n = "navi";
    };

    sessionVariables = {
      EDITOR = "vim";
      VISUAL = "vim";
      TZ = "Europe/Stockholm";
      XDG_CONFIG_HOME = "$HOME/.config";
      GROFF_NO_SGR = "1";

      # navi cheatsheets
      NAVI_PATH = "$HOME/code/github/chelmertz/dotfiles/cheats";

      # sdkman
      SDKMAN_DIR = "$HOME/.sdkman";

      # bun
      BUN_INSTALL = "$HOME/.bun";

      # nvm
      NVM_DIR = "$HOME/.config/nvm";

      # matchi
      MATCHI_GIT_REPO_PATH = "$HOME/code/matchi";
    };

    # Additional setopt commands not covered by history.*
    setOptions = [
      "HIST_SAVE_NO_DUPS"
      "HIST_REDUCE_BLANKS"
      "INC_APPEND_HISTORY_TIME"
      "interactive_comments"
    ];

    initContent = lib.mkMerge [
      # Powerlevel10k instant prompt - must be at the very top
      (lib.mkBefore ''
        if [[ -r "''${XDG_CACHE_HOME:-$HOME/.cache}/p10k-instant-prompt-''${(%):-%n}.zsh" ]]; then
          source "''${XDG_CACHE_HOME:-$HOME/.cache}/p10k-instant-prompt-''${(%):-%n}.zsh"
        fi
      '')

      ''
      # PATH additions
      export PATH="$HOME/.nix-profile/bin:$HOME/.hishtory:$HOME/.maestro/bin:$BUN_INSTALL/bin:$HOME/bin:/usr/local/go/bin:$HOME/.local/bin:$HOME/go/bin:$HOME/.emacs.d/bin:$HOME/.cargo/bin:$HOME/.yarn/bin:$PATH"

      # render man pages in colors
      export LESS_TERMCAP_mb=$'\e[1;32m'
      export LESS_TERMCAP_md=$'\e[1;32m'
      export LESS_TERMCAP_me=$'\e[0m'
      export LESS_TERMCAP_se=$'\e[0m'
      export LESS_TERMCAP_so=$'\e[01;33m'
      export LESS_TERMCAP_ue=$'\e[0m'
      export LESS_TERMCAP_us=$'\e[1;4;31m'

      # Git functions
      gcf() {
        git commit -a --fixup "$*"
      }

      gt() {
        git tag --format='%(objectname)^{}' | git cat-file --batch-check | awk '$2=="commit" { print $1 }' | git log --stdin --author-date-order --no-walk --oneline --pretty=format:'%C(auto)%h%d%Creset %C(cyan)(%cr)%Creset %C(green)%cn <%ce>%Creset %s'
      }

      gb() {
        git branch --format='%(objectname)^{}' | git cat-file --batch-check | awk '$2=="commit" { print $1 }' | git -c pager.log=less log --stdin --author-date-order --no-walk --oneline --pretty=format:'%C(auto)%h%d%Creset %C(cyan)(%cr)%Creset %C(green)%cn <%ce>%Creset %s'
      }

      gf() {
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

      # Git log format
      HASH="%C(always,yellow)%h%C(always,reset)"
      RELATIVE_TIME="%C(always,green)%ar%C(always,reset)"
      AUTHOR="%C(always,bold blue)%an%C(always,reset)"
      REFS="%C(always,red)%d%C(always,reset)"
      SUBJECT="%s"
      FORMAT="$HASH $RELATIVE_TIME{$AUTHOR{$REFS $SUBJECT"

      glp() {
        git log --graph --pretty="tformat:$FORMAT" $* | column -t -s '{' | less -XRS --quit-if-one-screen
      }

      gll() {
        git -c core.pager='less -p^commit.*$' log -p -M -w --stat --pretty=fuller --show-notes -- $(fd $*)
      }

      glf() {
        gl $(fd $*)
      }

      gtodo() {
        git grep -l TODO | xargs -n1 git blame -f -n -w | grep "$(git config user.name)" | grep TODO | sed "s/.\{9\}//" | sed "s/(.*)[[:space:]]*//" | sed "s/ \+/:/"
      }

      # File/directory utilities
      lc() {
        local dir="''${1:-.}"
        find $(echo "$dir" | sed -e 's#[^/]$#&/#') -type f | xargs head -n99 2>/dev/null | less -p '==> .* <==' --use-color --color=Skm
      }

      grony() {
        yq eval -o=p $*
      }

      e() {
        $EDITOR $*
      }

      cdt() {
        cd $(mktemp -d)
      }

      mk() {
        mkdir -p "$1" && cd "$1"
      }

      key() {
        xev -event keyboard | egrep -o 'keycode.*\)'
      }

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

      x() {
        case $1 in
          http*|*html)
            xdg-open $1 2>/dev/null
            wmctrl -a "Firefox"
            ;;
          *md)
            treemd $1
            ;;
          *)
            xdg-open $1
            ;;
        esac
      }

      batt() {
        fd "$*" | xargs bat
      }

      treewc() {
        python3 -c "
import os,sys
d=sys.argv[1] if len(sys.argv)>1 else '.'
def t(d,p='''):
 for i,e in enumerate(sorted(os.listdir(d))):
  f,l=os.path.join(d,e),i==len(os.listdir(d))-1
  c='└── 'if l else'├── '
  if os.path.isdir(f):print(f'{p}{c}{e}/');t(f,p+('    'if l else'│   '))
  else:print(f'{p}{c}{e} ({sum(1 for _ in open(f,errors=\"ignore\"))} lines)')
print(d+'/');t(d)
" "$1"
      }

      only_in_first() {
        grep -Fxvf <(tr -d '\r' < "$1") <(tr -d '\r' < "$2")
      }

      # z with fzf integration (z plugin loaded via programs.zsh.plugins)
      unalias z 2> /dev/null
      z() {
        [ $# -gt 0 ] && zshz "$*" && return
        cd "$(zshz -l 2>&1 | fzf --height 40% --nth 2.. --reverse --inline-info +s --tac --query "''${*##-* }" | sed 's/^[0-9,.]* *//')"
      }

      # pkill via fzf
      pk() {
        local pid
        if [[ "''${UID}" != "0" ]]; then
          pid=$(ps -f -u ''${UID} | sed 1d | fzf -m | awk '{print $2}')
        else
          pid=$(ps -ef | sed 1d | fzf -m | awk '{print $2}')
        fi

        if [[ "x$pid" != "x" ]]; then
          echo $pid | xargs kill "-''${1:-9}"
        fi
        zle reset-prompt
      }

      vf() {
        vim $(fzf)
      }

      # Keybinds
      bindkey -e  # emacs mode: enables Ctrl+A, Ctrl+E, etc.
      bindkey ";5C" forward-word
      bindkey ";5D" backward-word
      bindkey "^ " autosuggest-accept

      # Ctrl+Z to toggle fg
      function Resume {
        [[ "$(jobs | wc -l)" == "0" ]] && return
        fg
        zle push-input
        BUFFER=""
        zle accept-line
      }
      zle -N Resume
      bindkey "^Z" Resume

      # Edit current command in vim with Ctrl+X
      autoload -U edit-command-line
      zle -N edit-command-line
      bindkey "^X" edit-command-line

      # Completions
      compdef g='git'
      compdef _files pbcopy

      # gh completions
      source ~/code/github/chelmertz/dotfiles/zsh-scripts/_gh
      compdef _gh gh

      # jj (jujutsu) completions
      source <(jj util completion zsh)

      # bun completions
      [ -s "/home/ch/.bun/_bun" ] && source "/home/ch/.bun/_bun"

      # sdkman
      [[ -s "$HOME/.sdkman/bin/sdkman-init.sh" ]] && source "$HOME/.sdkman/bin/sdkman-init.sh"

      # nvm (lazy-loaded for faster shell startup)
      _nvm_lazy_load() {
        unset -f nvm node npm npx
        [ -s "$NVM_DIR/nvm.sh" ] && \. "$NVM_DIR/nvm.sh"
        [ -s "$NVM_DIR/bash_completion" ] && \. "$NVM_DIR/bash_completion"
      }
      nvm() { _nvm_lazy_load && nvm "$@"; }
      node() { _nvm_lazy_load && node "$@"; }
      npm() { _nvm_lazy_load && npm "$@"; }
      npx() { _nvm_lazy_load && npx "$@"; }

      # Linuxbrew
      eval "$(/home/linuxbrew/.linuxbrew/bin/brew shellenv)"

      # matchi
      test -f ~/.zsh-matchi && source ~/.zsh-matchi

      # hishtory
      test -f /home/ch/.hishtory/config.zsh && source /home/ch/.hishtory/config.zsh

      # Powerlevel10k config (theme loaded via plugins)
      [[ ! -f ~/.p10k.zsh ]] || source ~/.p10k.zsh

      # Private/local config
      test -f ~/.shrc-local && source ~/.shrc-local
    ''
    ];
  };

  # fzf with zsh integration
  programs.fzf.enableZshIntegration = true;
}
