{ pkgs, lib, ... }:
let
  # bin/git-freshen fetches every clone under ~/code and ~/p and fast-forwards
  # what can be fast-forwarded. Its own file documents the rule it keeps (no
  # write that is not a proven fast-forward); the tests are
  # `nix flake check`'s git-freshen attribute.
  #
  # The script is wrapped with everything it forks rather than the unit pinning
  # a PATH, for the reason p-launcher.nix spells out at length: four units in
  # this repo have been broken by a PATH kept in a file the program's author
  # never opens. openssh is in the list because it is *not* in home.packages -
  # the same absence that broke spotify-backup - and a git-freshen with no ssh
  # would report all 205 checkouts as fetch failures.
  runtimeDeps = [
    pkgs.git
    pkgs.openssh
    pkgs.coreutils # timeout, readlink, false - the askpass helper
    pkgs.findutils # find, xargs
    pkgs.gnused
    pkgs.gnugrep
    pkgs.gawk
    pkgs.util-linux
  ];

  # The shebang is pinned to the store as well as the PATH being wrapped, the
  # same fix bin.nix applies to its python scripts. A wrapper only sets PATH
  # for the process once it is running; `#!/usr/bin/env bash` has to resolve
  # before that, and under a unit with no PATH it cannot. Verified with
  # `env -i`, which failed with "env: 'bash': No such file or directory" until
  # this was added.
  git-freshen = pkgs.runCommand "git-freshen" { nativeBuildInputs = [ pkgs.makeWrapper ]; } ''
    mkdir -p $out/bin
    substitute ${../bin/git-freshen} $out/bin/git-freshen \
      --replace-fail '#!/usr/bin/env bash' '#!${pkgs.bash}/bin/bash'
    chmod +x $out/bin/git-freshen
    wrapProgram $out/bin/git-freshen \
      --suffix PATH : ${lib.makeBinPath runtimeDeps}
  '';
in
{
  systemd.user.services.git-freshen = {
    Unit.Description = "git-freshen: fetch every clone, fast-forward what is safe";
    Service = {
      Type = "oneshot";
      # No Environment=PATH: the wrapper carries what the script forks.
      ExecStart = "${git-freshen}/bin/git-freshen";
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
