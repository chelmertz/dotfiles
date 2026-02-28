{ ... }:
{
  programs.git = {
    enable = true;

    ignores = [
      # Compiled
      "*.com" "*.class" "*.dll" "*.exe" "*.o" "*.so"
      # Packages
      "*.7z" "*.dmg" "*.gz" "*.iso" "*.rar" "*.tar" "*.zip"
      # OS
      ".DS_Store*" "ehthumbs.db" "Icon?" "Thumbs.db"
      # Editors
      "*.swp" "*.swo" "*~" ".~lock.*#"
      # Tags/cache
      "tags" ".sass-cache" ".vscode" "__debug_bin"
      # Languages
      "*.pyc" "*.pyo" "*.beam" ".lein-failures" ".lein-repl-history"
      # Claude/direnv
      "**/.claude/settings.local.json" ".direnv"
    ];

    includes = [
      {
        condition = "gitdir:~/code/matchi/";
        contents.user.email = "helmertz@gmail.com";  # work email
      }
    ];

    settings = {
      branch = { autoSetupMerge = "simple"; sort = "-committerdate"; };
      color.ui = "auto";
      column.ui = "auto";
      commit.verbose = true;
      core = { fsmonitor = true; untrackedCache = true; };
      diff = { algorithm = "histogram"; colorMoved = "plain"; mnemonicPrefix = true; renames = true; tool = "meld"; };
      fetch = { all = true; prune = true; pruneTags = true; };
      github.user = "chelmertz";
      help.autocorrect = "prompt";
      init.defaultBranch = "main";
      log = { date = "human"; decorate = true; };
      merge = { conflictstyle = "zdiff3"; tool = "meld"; };
      pager.log = "less -p^commit.*$";
      pull.rebase = true;
      push = { autoSetupRemote = true; default = "current"; followTags = true; };
      rebase = { autoSquash = true; autoStash = true; updateRefs = true; };
      rerere = { autoupdate = true; enabled = true; };
      tag.sort = "version:refname";
      url."ssh://git@github.com/".insteadOf = "https://github.com/";
      user = { email = "helmertz@gmail.com"; name = "Calle Helmertz"; };
    };
  };
}
