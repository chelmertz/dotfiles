{ config, pkgs, lib, ... }:
let
  # Launcher for ~/p projects (F5). Source in ../p-launcher; design notes in
  # ~/p/personal/p-launcher/. Bumping a Go dependency changes vendorHash: set
  # it to lib.fakeHash, run home-manager switch, copy the "got:" hash back.
  # Everything p-launcher forks by bare name. It carries them itself rather
  # than each systemd unit pinning a PATH that has to be kept in step with the
  # code: four units in this repo have now been broken by that pattern, and the
  # list lives where the program's author never looks. --suffix, not --prefix,
  # so an interactive session's own PATH still wins and this is only the
  # fallback - which is exactly the case a unit with no PATH hits.
  #
  # Scope: everything reachable from a systemd unit, where PATH is whatever
  # the unit says and nothing else. gh for `links refresh`; ghostty, zsh and
  # claude-code because `tend` forks ghostty, which runs
  # `zsh -ic "claude; exec zsh"` inheriting this PATH; libnotify because that
  # is how a failure is reported, and its absence once swallowed the message
  # about a missing ghostty; claude again for the daily brief. git and i3 are
  # cheap and forked by the open path.
  #
  # Deliberately absent: rofi, firefox, xdg-open, xclip, copyq and ImageMagick's
  # `import`. Every one of them is reached only from the F5 menu, the clipboard
  # verb or the E2E goldens - all of which run in the user's own session with a
  # full PATH. Adding firefox in particular would put a browser in this
  # package's closure to guard a path no unit can take.
  runtimeDeps = [
    pkgs.gh
    config.programs.ghostty.package
    pkgs.zsh
    pkgs.claude-code
    pkgs.libnotify
    pkgs.git
    pkgs.i3
  ];

  p-launcher = pkgs.buildGoModule {
    pname = "p-launcher";
    version = "0.1.0";
    src = ../p-launcher;
    vendorHash = "sha256-JIqffDGU076jdWEEW6Uw7c1oC3ZHrr+gEqi6sfdIgAk=";
    nativeBuildInputs = [ pkgs.makeWrapper ];
    postInstall = ''
      wrapProgram $out/bin/p-launcher \
        --suffix PATH : ${lib.makeBinPath runtimeDeps}
    '';
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
      # No PATH pinned: the binary is wrapped with what it forks (see
      # runtimeDeps above). It used to name gh only, and tend died on the first
      # PR it decided to act on because ghostty was not there - with the
      # notification meant to report that failing too, for the same reason.
      # The unit exits 0 until the conditional path is reached, so it hid for a
      # day. Listing dependencies where the program's author never looks is
      # what produced that; the program carries them now.
    };
  };
  # Daily brief: one read-only headless Claude run over the PR queue, stored
  # and rendered to brief.html. Needs claude and gh on PATH; it is skipped
  # silently when the machine has no elly data yet. The `brief…` row in the
  # F5 menu opens whatever this last wrote.
  systemd.user.services.p-launcher-brief = {
    Unit.Description = "p-launcher: daily PR brief";
    Service = {
      Type = "oneshot";
      # PATH comes from the wrapper, as for p-launcher-links above.
      ExecStart = "${p-launcher}/bin/p-launcher brief";
    };
  };
  systemd.user.timers.p-launcher-brief = {
    Unit.Description = "p-launcher: daily PR brief";
    Timer = {
      # a workday morning read; Persistent catches a machine that was asleep
      OnCalendar = "Mon..Fri 08:30";
      Persistent = true;
      RandomizedDelaySec = "5m";
    };
    Install.WantedBy = [ "timers.target" ];
  };

  # Nightly dated copy of the database into ~/p/personal/p-launcher/backup
  # (7 kept); the live DB stays out of ~/p so a syncing client never touches
  # a WAL database. When ~/.config/p-launcher/restic.env exists (hand-placed,
  # never versioned: RESTIC_REPOSITORY, RESTIC_PASSWORD, AWS_ACCESS_KEY_ID,
  # AWS_SECRET_ACCESS_KEY, the same values the vps repo keeps in sops), the
  # copy also goes to the shared restic repo on Backblaze B2, tagged and
  # host-scoped so forget/prune never touches the vps snapshots.
  systemd.user.services.p-launcher-backup = {
    Unit.Description = "p-launcher: nightly database backup";
    Service = {
      Type = "oneshot";
      EnvironmentFile = "-%h/.config/p-launcher/restic.env";
      Environment = "PATH=${lib.makeBinPath [ pkgs.restic pkgs.coreutils pkgs.hostname ]}";
      ExecStart = pkgs.writeShellScript "p-launcher-backup" ''
        set -euo pipefail
        dir=$(${p-launcher}/bin/p-launcher backup)
        [ -n "''${RESTIC_REPOSITORY:-}" ] || exit 0
        export RESTIC_CACHE_DIR="''${XDG_CACHE_HOME:-$HOME/.cache}/restic"
        restic cat config >/dev/null 2>&1 || restic init
        restic backup --tag p-launcher --host "$(hostname)" "$(dirname "$dir")"
        restic forget --tag p-launcher --host "$(hostname)" --keep-daily 7 --keep-weekly 4 --keep-monthly 6 --prune
      '';
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
      # Counterpart: linksStaleAfter in p-launcher/session_brief.go, which
      # must stay above this interval or the session brief calls the timer
      # broken between ticks. TestLinksTimerIntervalMatchesStaleness asserts
      # against this literal.
      OnUnitActiveSec = "10m";
      RandomizedDelaySec = "1m";
    };
    Install.WantedBy = [ "timers.target" ];
  };
}
