{ pkgs, ... }:
let
  # git-freshen fetches every clone under ~/code and ~/p, fast-forwards what
  # can be fast-forwarded and removes finished worktrees. Its package comment
  # documents the rule it keeps (no write that is not a proven fast-forward);
  # the tests are its Go tests, run by the build, plus `nix flake check`'s
  # git-freshen attribute.
  git-freshen = pkgs.callPackage ../git-freshen/package.nix { };
in
{
  # On PATH as git-freshen, so `git freshen` reaches it too.
  home.packages = [ git-freshen ];

  systemd.user.services.git-freshen = {
    Unit.Description = "git-freshen: fetch every clone, fast-forward what is safe, prune finished worktrees";
    Service = {
      Type = "oneshot";
      # No Environment=PATH: the wrapper carries what the binary forks.
      ExecStart = "${pkgs.lib.getExe git-freshen}";
      # The records go to stdout and the tally to stderr, so the journal holds
      # both and `journalctl --user -u git-freshen` is the run log. Records are
      # tab-separated and sorted, so two runs can be diffed.
      StandardOutput = "journal";
    };
  };

  systemd.user.timers.git-freshen = {
    Unit.Description = "git-freshen: hourly";
    Timer = {
      # 5m after boot rather than immediately: the ssh agent this needs is
      # gcr-ssh-agent, and a keyring that has not been unlocked yet would turn
      # every fetch into a failure. SSH_AUTH_SOCK reaches the unit through the
      # systemd user environment, which is already how it is set here.
      OnBootSec = "5m";
      OnUnitActiveSec = "1h";
      # 205 checkouts is 205 connections to github and gitlab in a burst; the
      # jitter keeps successive runs from arriving on the same second.
      RandomizedDelaySec = "5m";
      Persistent = true;
    };
    Install.WantedBy = [ "timers.target" ];
  };
}
