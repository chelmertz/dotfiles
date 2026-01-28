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
      user = {
        name = "Calle Helmertz";
        email = "helmertz@gmail.com";
      };
      pull.rebase = true;
      push = { default = "current"; autoSetupRemote = true; };
      rebase.autoSquash = true;
      color.ui = "auto";
      log = { decorate = true; date = "human"; };
      help.autocorrect = 1;
      branch.autoSetupMerge = "simple";
      init.defaultBranch = "main";
      github.user = "chelmertz";
      rerere.enabled = true;
      diff = { tool = "meld"; algorithm = "patience"; };
      merge.tool = "meld";
      pager.log = "less -p^commit.*$";
      url."ssh://git@github.com/".insteadOf = "https://github.com/";
    };
  };
}
