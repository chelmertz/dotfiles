{ pkgs, ... }:
let
  py = pkgs.python3.withPackages (ps: [ ps.spotipy ]);
  repo = "$HOME/code/github/chelmertz/spotify";
in
{
  # Ensure the spotify data repo is cloned on new machines
  home.activation.spotifyRepo = ''
    if [ ! -d "${repo}/.git" ]; then
      mkdir -p "$(dirname "${repo}")"
      ${pkgs.git}/bin/git clone git@github.com:chelmertz/spotify.git "${repo}"
    fi
  '';

  # Wrapped scripts: source the credentials env file, then exec with
  # a python that has spotipy. These are NOT in bin.nix because they
  # need the nix-managed python, not #!/usr/bin/env python3.
  home.file.".local/bin/spotify-backup" = {
    source = pkgs.writeShellScript "spotify-backup" ''
      set -a
      . "$HOME/.config/spotify-backup/env"
      set +a
      exec ${py}/bin/python3 ${repo}/spotify-backup.py "$@"
    '';
  };

  home.file.".local/bin/spotify-like" = {
    source = pkgs.writeShellScript "spotify-like" ''
      set -a
      . "$HOME/.config/spotify-backup/env"
      set +a
      exec ${py}/bin/python3 ${repo}/spotify-like.py "$@"
    '';
  };

  home.file.".local/bin/spotify" = {
    source = pkgs.writeShellScript "spotify" ''
      exec "$HOME/.nix-profile/bin/spotify" --force-device-scale-factor=1.1 "$@"
    '';
  };

  xdg.desktopEntries.spotify = {
    name = "Spotify";
    exec = "spotify --force-device-scale-factor=1.1";
    icon = "spotify";
    terminal = false;
    categories = [
      "Audio"
      "Music"
      "Player"
    ];
  };

  systemd.user.services.spotify-backup = {
    Unit.Description = "Backup Spotify metadata to git";
    Service = {
      Type = "oneshot";
      ExecStart = "${py}/bin/python3 %h/code/github/chelmertz/spotify/spotify-backup.py";
      EnvironmentFile = "%h/.config/spotify-backup/env";
      Environment = "PATH=${pkgs.git}/bin:/usr/bin:/bin";
    };
  };

  systemd.user.timers.spotify-backup = {
    Unit.Description = "Run spotify-backup daily";
    Timer = {
      OnCalendar = "*-*-* 03:00:00";
      Persistent = true;
    };
    Install.WantedBy = [ "timers.target" ];
  };
}
