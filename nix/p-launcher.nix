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
  home.packages = [ p-launcher ];

  # PR links: GitHub facts via gh (conditional requests) and elly's verdict,
  # refreshed on a timer so the F5 menu never touches the network.
  systemd.user.services.p-launcher-links = {
    Unit.Description = "p-launcher: refresh PR links from GitHub and elly";
    Service = {
      Type = "oneshot";
      ExecStart = "${p-launcher}/bin/p-launcher links refresh";
      Environment = "PATH=${lib.makeBinPath [ pkgs.gh ]}";
    };
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
