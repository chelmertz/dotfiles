{ pkgs, ... }:
{
  # Kodi and RetroArch on the media box rewrite their own configs on exit, so
  # they cannot be declared in that machine's NixOS flake the way everything
  # else is. This pulls them into chelmertz/mediaserver on a timer instead, so a
  # working configuration stays recoverable and changes show up in git.
  #
  # The script lives in its own repo, matching how spotify.nix does it.
  systemd.user.services.mediabox-settings = {
    Unit.Description = "Snapshot mediabox Kodi/RetroArch settings into git";
    Service = {
      Type = "oneshot";
      ExecStart = "%h/code/github/chelmertz/mediaserver/scripts/pull-settings.sh --commit";
      Environment = "PATH=${pkgs.lib.makeBinPath [
        pkgs.bash
        pkgs.coreutils
        pkgs.git
        pkgs.gnused
        pkgs.gnugrep
        pkgs.openssh
        pkgs.rsync
      ]}:/usr/bin:/bin";
    };
  };

  systemd.user.timers.mediabox-settings = {
    Unit.Description = "Snapshot mediabox settings every 3 hours";
    Timer = {
      # The box is not on continuously, so a single nightly window would often
      # be missed entirely. Persistent catches up after the laptop sleeps.
      OnCalendar = "*-*-* 00/3:17:00";
      Persistent = true;
      RandomizedDelaySec = "5m";
    };
    Install.WantedBy = [ "timers.target" ];
  };
}
