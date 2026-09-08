{ pkgs, lib, ... }:
let
  # Launcher for ~/p projects (F5). Source in ../p-launcher; design notes in
  # ~/p/personal/p-launcher/. Bumping a Go dependency changes vendorHash: set
  # it to lib.fakeHash, run home-manager switch, copy the "got:" hash back.
  p-launcher = pkgs.buildGoModule {
    pname = "p-launcher";
    version = "0.1.0";
    src = ../p-launcher;
    vendorHash = "sha256-JIqffDGU076jdWEEW6Uw7c1oC3ZHrr+gEqi6sfdIgAk=";
    meta.mainProgram = "p-launcher";
  };
in
{
  # xvfb: virtual X server for the screenshot golden tests
  # (p-launcher/golden_test.go, P_LAUNCHER_E2E=1).
  home.packages = [ p-launcher pkgs.xvfb ];

  # PR links: GitHub facts via gh (conditional requests) and elly's verdict,
  # refreshed on a timer so the F5 menu never touches the network.
  systemd.user.services.p-launcher-links = {
    Unit.Description = "p-launcher: refresh PR links from GitHub and elly";
    Service = {
      Type = "oneshot";
      # refresh first, then let tend decide; tend is inert until
      # `p-launcher kv set tend.enabled 1`, but its decisions land in the
      # journal either way (journalctl --user -u p-launcher-links).
      ExecStart = pkgs.writeShellScript "p-launcher-links" ''
        ${p-launcher}/bin/p-launcher links refresh
        ${p-launcher}/bin/p-launcher tend
      '';
      Environment = "PATH=${lib.makeBinPath [ pkgs.gh ]}";
    };
  };
  # Nightly dated copy of the database into ~/p/personal/p-launcher/backup
  # (7 kept); the live DB stays out of ~/p so a syncing client never touches
  # a WAL database.
  systemd.user.services.p-launcher-backup = {
    Unit.Description = "p-launcher: nightly database backup";
    Service = {
      Type = "oneshot";
      ExecStart = "${p-launcher}/bin/p-launcher backup";
    };
  };
  systemd.user.timers.p-launcher-backup = {
    Unit.Description = "p-launcher: nightly database backup";
    Timer = {
      OnCalendar = "03:30";
      Persistent = true; # runs at next boot if the laptop was off
    };
    Install.WantedBy = [ "timers.target" ];
  };

  systemd.user.timers.p-launcher-links = {
    Unit.Description = "p-launcher: refresh PR links every 10 min";
    Timer = {
      OnBootSec = "2m";
      OnUnitActiveSec = "10m";
      RandomizedDelaySec = "1m";
    };
    Install.WantedBy = [ "timers.target" ];
  };
}
