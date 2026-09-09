# tau: ThinkPad X1 Carbon Gen 12, NixOS. This is the layer Ubuntu provided on
# gamma — the X session, keyd, Docker, printing, audio, and the desktop apps
# that came from apt, PPAs and .deb repositories. Everything user-level stays
# in home.nix and is applied separately with `home-manager switch`.
#
# The design notes for this file are outside the repository, at
# ~/p/m/new-laptop/docs/tau-nixos-design.md, because they inventory the
# machine and this repository is public.
{ config, lib, pkgs, ... }:
{
  # ── Boot ────────────────────────────────────────────────────────────────
  # Secure Boot must be OFF in firmware. Ubuntu booted through a signed shim;
  # systemd-boot has no signed loader without lanzaboote, which is out of
  # scope for this baseline.
  boot.loader.systemd-boot.enable = true;
  boot.loader.efi.canTouchEfiVariables = true;
  boot.initrd.systemd.enable = true;

  # ── Identity ────────────────────────────────────────────────────────────
  # home-manager resolves homeConfigurations."ch@tau" from this name. Change it
  # and a bare `home-manager switch` falls back to "ch", gamma's Ubuntu
  # configuration, with no error.
  networking.hostName = "tau";

  time.timeZone = "Europe/Stockholm";
  i18n.defaultLocale = "en_US.UTF-8";
  # gamma's /etc/default/locale: English messages, Swedish everything else.
  i18n.extraLocaleSettings = {
    LC_ADDRESS = "sv_SE.UTF-8";
    LC_IDENTIFICATION = "sv_SE.UTF-8";
    LC_MEASUREMENT = "sv_SE.UTF-8";
    LC_MONETARY = "sv_SE.UTF-8";
    LC_NAME = "sv_SE.UTF-8";
    LC_NUMERIC = "sv_SE.UTF-8";
    LC_PAPER = "sv_SE.UTF-8";
    LC_TELEPHONE = "sv_SE.UTF-8";
    LC_TIME = "sv_SE.UTF-8";
  };
  console.keyMap = "sv-latin1";

  # The LUKS passphrase is typed in the initrd, and there the Swedish layout
  # was not being applied: nixpkgs puts systemd-vconsole-setup.service,
  # loadkeys and the sv-latin1 keymap into the initrd, but nothing starts the
  # unit. Its trigger upstream is systemd's 90-vconsole.rules, which is not
  # among the 14 udev rules the initrd ships, and every other unit only orders
  # itself After= it. Without this the prompt takes the kernel's built-in US
  # layout, so any passphrase containing a character that moves between the
  # two layouts would be untypeable at boot.
  boot.initrd.systemd.services.systemd-vconsole-setup.wantedBy = [ "sysinit.target" ];

  # ── Storage ─────────────────────────────────────────────────────────────
  # A file, not a partition: resizing is editing this number. Sized for memory
  # pressure only, since hibernate is out of scope and it need not hold RAM.
  swapDevices = [
    {
      device = "/var/lib/swapfile";
      size = 8 * 1024;
    }
  ];

  # ── X session ───────────────────────────────────────────────────────────
  # gamma runs exactly this: gdm3 starting the i3 xsession. GDM rather than
  # lightdm because it is what gamma has and it handles fingerprint login.
  services.xserver.enable = true;
  services.xserver.windowManager.i3.enable = true;
  services.displayManager.gdm.enable = true;
  services.displayManager.defaultSession = "none+i3";
  # .i3/config also runs `setxkbmap -layout se`, which covers keyboards that
  # appear after the session starts. This covers the greeter.
  services.xserver.xkb.layout = "se";

  # GDM starts the NixOS `none+i3` session, whose script reads no shell rc and
  # no ~/.profile, so nothing sourced home-manager's session variables: i3 ran
  # with TERMINAL, XCURSOR_THEME and the gsettings schema path unset, and
  # without ~/.local/bin on PATH. Every bare-name call to a script there failed
  # from inside the session — rofi's modi, mod+1..8 project switching, the bar
  # scripts — while the same command worked fine in a terminal, because a login
  # shell does source them. Ubuntu papered over this by having GDM read
  # ~/.profile, which its libglib/desktop packages populate.
  services.xserver.displayManager.sessionCommands = ''
    if [ -f "$HOME/.nix-profile/etc/profile.d/hm-session-vars.sh" ]; then
      . "$HOME/.nix-profile/etc/profile.d/hm-session-vars.sh"
    fi
  '';
  services.libinput.enable = true;

  # nixos-hardware's 12th-gen module hardcodes "TPPS/2 Synaptics TrackPoint",
  # but this machine (21KC005XMX) reports "TPPS/2 Elan TrackPoint" in
  # /proc/bus/input/devices. The module builds a udev rule matching
  # ATTR{name}, so with the wrong name every trackpoint setting silently does
  # nothing — including emulateWheel, which is middle-button scrolling.
  # nixpkgs' own option documentation notes newer ThinkPads use the Elan name.
  hardware.trackpoint.device = lib.mkForce "TPPS/2 Elan TrackPoint";

  # keyd remaps below evdev, so keylog (keylogger/) sees the real Esc and
  # Ctrl rather than the synthesised ones an X-level remap produces. This must
  # stay equal to keyd/default.conf, gamma's copy of the same rule.
  services.keyd = {
    enable = true;
    keyboards.default = {
      ids = [ "*" ];
      settings.main.capslock = "overload(control, esc)";
    };
  };

  # ── Audio, Bluetooth, printing ──────────────────────────────────────────
  services.pipewire = {
    enable = true;
    alsa.enable = true;
    alsa.support32Bit = true;
    # .i3/config binds the volume keys to pactl and several i3blocks scripts
    # call it, so the PulseAudio interface has to be present.
    pulse.enable = true;
  };

  hardware.bluetooth = {
    enable = true;
    powerOnBoot = true;
  };

  # HP Color LaserJet MFP M281fdw, found over the network.
  services.printing = {
    enable = true;
    drivers = [ pkgs.hplip ];
  };
  services.avahi = {
    enable = true;
    nssmdns4 = true;
    openFirewall = true;
  };

  # ── Login ───────────────────────────────────────────────────────────────
  # Wires fingerprint into GDM and sudo, which is what gamma's gdm-fingerprint
  # PAM file did. Enrolment is imperative: fprintd-enroll, after first boot.
  services.fprintd.enable = true;

  # The D-Bus secret service. Ubuntu supplied it through its GNOME session, and
  # without it there is nowhere for libsecret clients to keep anything: `gh`
  # stores its token here (gamma's reports "(keyring)"), so on a machine
  # without it gh either refuses or falls back to plaintext in hosts.yml.
  #
  # This one line is enough for GDM too. It puts pam_gnome_keyring into the
  # `login` stack, and /etc/pam.d/gdm-password is nothing but a substack of
  # `login`, so the keyring unlocks on a graphical login as well; setting
  # enableGnomeKeyring on gdm-password as well produces a byte-identical
  # system and was removed.
  #
  # A fingerprint login cannot unlock it: the reader yields no password to
  # derive the key from, so that path prompts separately.
  services.gnome.gnome-keyring.enable = true;

  # ── Network ─────────────────────────────────────────────────────────────
  networking.networkmanager.enable = true;
  networking.firewall = {
    enable = true;
    allowedTCPPorts = [ 22 ];
  };

  # Needed for the nixos-anywhere install and for rsync from gamma. Keys only:
  # this laptop leaves the house.
  services.openssh = {
    enable = true;
    settings = {
      PasswordAuthentication = false;
      KbdInteractiveAuthentication = false;
      PermitRootLogin = "prohibit-password";
    };
  };

  virtualisation.docker.enable = true;

  # ── Users ───────────────────────────────────────────────────────────────
  users.users.ch = {
    isNormalUser = true;
    shell = pkgs.zsh;
    # gamma's groups minus lpadmin, lxd and sambashare, which were unused.
    # video is for brightnessctl without sudo, input for keylog reading
    # /dev/input/event*.
    extraGroups = [
      "wheel"
      "docker"
      "video"
      "input"
      "networkmanager"
    ];
    # This repository is public, so no credential appears in it. The file is
    # root-owned and 0600, seeded during the install with nixos-anywhere's
    # --extra-files; generate it with `mkpasswd -m yescrypt`. If it is missing
    # at boot the account is locked rather than open, which is the right way
    # to fail.
    hashedPasswordFile = "/etc/local/passwd-ch";
  };
  # No authorizedKeys here either. sshd reads ~/.ssh/authorized_keys by
  # default, and ~/.ssh arrives with the data migration, which tau initiates
  # as a pull from gamma and so needs no inbound access first. Root gets no
  # keys at all: nixos-anywhere authenticates against the installer, not the
  # installed system.

  # Registers zsh as a login shell. The configuration itself is home-manager's.
  programs.zsh.enable = true;

  # ── Nix ─────────────────────────────────────────────────────────────────
  # On gamma these live in /etc/nix/nix.conf, root-owned, because
  # home-manager's nix.settings writes the *user* nix.conf, which the daemon
  # ignores for all of them. Here they are just options.
  nix.settings = {
    experimental-features = [
      "nix-command"
      "flakes"
    ];
    # root is trusted unconditionally by NixOS; naming it here only produced a
    # duplicate entry in the generated nix.conf.
    trusted-users = [ "ch" ];
    auto-optimise-store = true;
    # gamma set 8 on 16 threads. The Gen 12 has fewer; revisit after `lscpu`.
    max-jobs = 6;
  };
  # The user profile keeps its own gc timer in home.nix with the same policy.
  nix.gc = {
    automatic = true;
    dates = "weekly";
    randomizedDelaySec = "45min";
    options = "--delete-older-than 14d";
  };

  # ── Foreign package formats ─────────────────────────────────────────────
  # An AppImage expects an FHS root and will not start on NixOS without this.
  # binfmt makes ./foo.AppImage work directly, as it does elsewhere.
  programs.appimage = {
    enable = true;
    binfmt = true;
  };
  # hishtory, maestro, bun and rustup install prebuilt, dynamically linked
  # binaries into $HOME. Without a loader at /lib64/ld-linux-x86-64.so.2 they
  # fail to start; this provides one.
  programs.nix-ld.enable = true;

  # ── Applications Ubuntu supplied ────────────────────────────────────────
  programs.firefox.enable = true;
  programs._1password.enable = true;
  programs._1password-gui = {
    enable = true;
    polkitPolicyOwners = [ "ch" ];
  };
  programs.steam.enable = true;

  environment.systemPackages = with pkgs; [
    # From a .deb repository and a PPA respectively.
    code-cursor
    emacs

    # i3 session glue. Ubuntu pulled these in as dependencies of its i3
    # metapackage and home-manager installs none of them, but .i3/config calls
    # every one by name. polkit_gnome is deliberately absent: home.nix appends
    # its exec as an absolute store path, so the agent comes from the
    # home-manager closure and a copy here would be dead weight.
    i3lock
    xss-lock
    networkmanagerapplet
    xorg.xset
    xorg.setxkbmap
    # For pactl only; the server is pipewire.
    pulseaudio

    # bin/rofi_timer.sh plays bell.oga and complete.oga from this theme.
    sound-theme-freedesktop

    docker-compose
    git

    # The schemas themselves; the session variable above points at them.
    gsettings-desktop-schemas
  ];

  # gsettings schemas, at the system level. home-manager can only prepend to
  # XDG_DATA_DIRS from its own session-vars file, and the NixOS session wrapper
  # sets XDG_DATA_DIRS again afterwards, dropping it: the running session had
  # TERMINAL from that file but no schema path, so `gsettings get` answered
  # "No schemas installed" and the i3blocks light/dark toggle silently failed.
  # Declared here it is part of the session environment before any wrapper runs.
  # nixpkgs nests schemas one level deeper than the share/glib-2.0/schemas that
  # XDG_DATA_DIRS is searched for, hence the long path.
  environment.sessionVariables.XDG_DATA_DIRS = [
    "${pkgs.gsettings-desktop-schemas}/share/gsettings-schemas/${pkgs.gsettings-desktop-schemas.name}"
  ];

  # nix/fonts.nix installs the coding and UI faces into the user profile but no
  # colour emoji, which rofimoji needs, and no metric-compatible fallbacks,
  # which LibreOffice and the web expect.
  fonts.packages = with pkgs; [
    noto-fonts-color-emoji
    dejavu_fonts
    liberation_ttf
  ];

  hardware.graphics.enable = true;
  services.fwupd.enable = true;

  # The first release installed on this machine. Never bump it on an existing
  # install: it selects state-format compatibility, not package versions.
  system.stateVersion = "26.05";
}
