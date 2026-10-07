# Daily restic backups of $HOME to two repositories: the VPS (offsite,
# reachable from anywhere) and mediabox's /data disk (at home, LAN only).
# Both are encrypted client-side; the password is in 1Password (personal
# account, Private vault, "restic backup (tau)") with a 0600 copy on disk,
# because an unattended run cannot wait for a fingerprint.
#
# Coverage is "all of home, minus what is rebuildable or not ours to keep":
# work material (~/code/matchi, ~/p/m, cloud credentials) is excluded so it
# never spreads into personal storage. Each run writes its result for
# node-exporter, and rules.yml alerts on a stale or failing repository.
{
  config,
  lib,
  pkgs,
  ...
}:
let
  home = config.home.homeDirectory;
  firefox = "${home}/.mozilla/firefox";
  # The profile manager calls it "Personal"; profiles.ini still says "work".
  # The profile Firefox opens by default, dggz6zoo.matchi, is the work one.
  personalProfile = "BJJLqXtQ.Profile 1";

  firefoxTabs = pkgs.runCommand "firefox-tabs" { } ''
    substitute ${../bin/firefox-tabs} $out \
      --replace-fail '#!/usr/bin/env python3' '#!${pkgs.python3}/bin/python3'
    chmod +x $out
  '';

  metrics = pkgs.writeShellApplication {
    name = "restic-metrics";
    runtimeInputs = [
      pkgs.restic
      pkgs.jq
      pkgs.coreutils
      pkgs.gnused
    ];
    text = builtins.readFile ../bin/restic-metrics;
  };

  # Before each run: list every git clone under ~/code and ~/p/personal so the
  # backup skips them (they live on their remotes), and save the Personal
  # profile's open tabs as plain URLs. A blanket "skip anything with .git"
  # rule would also drop the archived repositories in ~/sync, which exist
  # nowhere else, so the clones are named rather than matched.
  prepare = ''
    find ${home}/code ${home}/p/personal -name .git -prune -printf '%h\n' \
      > "$RUNTIME_DIRECTORY/git-clones" 2>/dev/null || true
    mkdir -p ${home}/.local/state/firefox-tabs
    ${firefoxTabs} "${firefox}/${personalProfile}" \
      > ${home}/.local/state/firefox-tabs/personal.txt || true
  '';

  exclude = [
    # Rebuildable: caches, package stores, containers.
    "${home}/.cache"
    "${home}/go/pkg"
    "${home}/.cargo/registry"
    "${home}/.cargo/git"
    "${home}/.gradle"
    "${home}/.npm"
    "${home}/.m2"
    "${home}/.rustup"
    "${home}/.local/share/docker"
    "${home}/.local/share/containers"
    "${home}/.local/share/pnpm"
    "${home}/.local/share/flatpak"
    "${home}/.local/share/Trash"
    "${home}/.local/share/prometheus"
    # Slack re-downloads everything from its servers, and most of it is the
    # work workspace. Flatpak apps keep their own caches (Sober's is 705 MB).
    "${home}/.config/Slack"
    "${home}/.var/app/*/cache"
    "${home}/.dropbox-dist"

    # Build output, wherever it sits. Cargo's target/ and other tools that
    # write a CACHEDIR.TAG are caught by --exclude-caches instead.
    "node_modules"
    ".venv"
    "__pycache__"
    "elm-stuff"
    ".direnv"
    ".terraform"
    ".pytest_cache"
    ".mypy_cache"
    ".next"

    # Work: never copied into personal storage.
    "${home}/code/matchi"
    "${home}/p/m"
    "${home}/.aws"
    "${home}/.kube"
    "${home}/.docker"
    # ~/p's own repository tracks m/ as well.
    "${home}/p/.git"

    # Steam: games re-download. Keep the per-game Proton prefixes, where
    # Windows games save, and the account's own userdata. Native games save
    # under ~/.local/share or ~/.config, which are backed up anyway.
    "${home}/.local/share/Steam/*"
    "!${home}/.local/share/Steam/userdata"
    "!${home}/.local/share/Steam/steamapps"
    "${home}/.local/share/Steam/steamapps/*"
    "!${home}/.local/share/Steam/steamapps/compatdata"

    # Claude Code: transcripts mix work and personal and expire after 30 days
    # anyway. Keep skills, settings and the memory of non-work projects.
    "${home}/.claude/*"
    "!${home}/.claude/skills"
    "!${home}/.claude/settings.json"
    "!${home}/.claude/projects"
    "${home}/.claude/projects/*/*"
    "!${home}/.claude/projects/*/memory"
    "${home}/.claude/projects/*matchi*"
    "${home}/.claude/projects/*-home-ch-p-m-*"

    # Browsers: profiles are mostly website storage and logged-in sessions.
    # From Firefox keep the extensions and their settings (every profile),
    # and the Personal profile's bookmark backups; its tabs are saved as
    # plain URLs by the prepare step. Chromium-family profiles are dropped.
    "${home}/.config/chromium"
    "${home}/.config/google-chrome"
    "${home}/.local/share/ytmusic-chromium"
    "${firefox}/*/*"
    "!${firefox}/*/extensions"
    "!${firefox}/*/extensions.json"
    "!${firefox}/*/extension-settings.json"
    "!${firefox}/*/extension-preferences.json"
    "!${firefox}/*/browser-extension-data"
    # storage.sync: where Vimium, Dark Reader, Stylus and Refined GitHub keep
    # their settings. Firefox Sync carries it too, but only while signed in.
    "!${firefox}/*/storage-sync-v2.sqlite*"
    "!${firefox}/*/storage"
    "${firefox}/*/storage/*"
    "!${firefox}/*/storage/default"
    "${firefox}/*/storage/default/*"
    "!${firefox}/*/storage/default/moz-extension*"
    "!${firefox}/${personalProfile}/bookmarkbackups"
  ];

  common = name: {
    passwordFile = "${home}/.config/restic/password";
    initialize = true;
    inhibitsSleep = true;
    paths = [ home ];
    inherit exclude;
    extraBackupArgs = [
      "--exclude-caches"
      "--exclude-file=$RUNTIME_DIRECTORY/git-clones"
      "--one-file-system"
    ];
    pruneOpts = [
      "--keep-daily 7"
      "--keep-weekly 8"
      "--keep-monthly 12"
    ];
    runCheck = true;
    backupPrepareCommand = prepare;
    backupCleanupCommand = "${lib.getExe metrics} ${name}";
    timerConfig = {
      OnCalendar = "daily";
      Persistent = true;
      RandomizedDelaySec = "1h";
    };
  };
in
lib.mkIf config.dotfiles.nixos {
  services.restic = {
    enable = true;
    backups = {
      vps = common "vps" // {
        repository = "sftp:root@45.142.177.125:/data/restic/tau";
        # Reading back a slice of the stored data each day re-reads the whole
        # repository about every three months, without pulling gigabytes over
        # the VPS link daily.
        checkOpts = [ "--read-data-subset=1%" ];
      };
      mediabox = common "mediabox" // {
        repository = "sftp:ch@mediabox.local:/data/restic/tau";
        checkOpts = [ "--read-data-subset=3%" ];
      };
    };
  };

  # mediabox is only reachable at home. Away from it the run is skipped, not
  # failed, so SystemdUserUnitFailed does not fire every day of a trip; the
  # staleness alert in rules.yml covers a mediabox that is never reached.
  systemd.user.services.restic-backups-mediabox.Service.ExecCondition =
    "${pkgs.openssh}/bin/ssh -oBatchMode=yes -oConnectTimeout=5 ch@mediabox.local true";
}
