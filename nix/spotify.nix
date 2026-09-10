{ pkgs, ... }:
let
  py = pkgs.python3.withPackages (ps: [ ps.spotipy ]);
  repo = "$HOME/code/github/chelmertz/spotify";
in
{
  # Ensure the spotify data repo is cloned on new machines. Two things this
  # has to survive on a fresh machine. git needs an ssh binary
  # for the git@ transport and the activation script's PATH has none on NixOS,
  # where there is no /usr/bin/ssh to fall back on; and a machine that has not
  # received its GitHub key yet cannot authenticate at all. Neither is a reason
  # to abort the whole activation, which is what an unguarded clone did.
  home.activation.spotifyRepo = ''
    if [ ! -d "${repo}/.git" ]; then
      mkdir -p "$(dirname "${repo}")"
      if ! GIT_SSH_COMMAND=${pkgs.openssh}/bin/ssh \
           ${pkgs.git}/bin/git clone git@github.com:chelmertz/spotify.git "${repo}"; then
        echo "spotifyRepo: clone failed, leaving it for later (no GitHub key yet?)"
      fi
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
      # openssh, because the last thing the script does is `git push` to a
      # git@ remote, and git forks ssh by name. The activation script above
      # learned this already; the unit did not, so every firing on tau since
      # the machine was built died with "cannot run ssh: No such file or
      # directory" after a full fetch of the account. gamma never noticed:
      # /usr/bin/ssh exists there.
      Environment = "PATH=${pkgs.git}/bin:${pkgs.openssh}/bin:/usr/bin:/bin";
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
