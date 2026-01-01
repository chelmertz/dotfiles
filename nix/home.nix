{ pkgs, ... }:
{
  home.username = "ch";
  home.homeDirectory = "/home/ch";
  home.stateVersion = "24.05";

  home.packages = with pkgs; [
    # Already migrated
    bat
    cloc
    fd
    fzf
    gh
    git
    gron
    jq
    libreoffice
    meld
    peek
    ripgrep
    screenkey
    shellcheck
    tree
    vlc
    w3m

    # CLI tools
    htop
    entr
    sqlite
    nmap
    pandoc
    graphviz
    visidata
    asciinema
    figlet
    html-tidy
    redshift

    # GUI tools
    copyq
    pavucontrol
    arandr
    autorandr
    sqlitebrowser
    rofi
    flameshot

    # X11/i3 tools
    i3blocks
    xcape
    xclip
    xsel
    xdotool
    wmctrl
    brightnessctl
    playerctl

    # pip replacements
    litecli
    pgcli
    git-filter-repo

    # go replacements
    lazygit
    yq
    lazydocker
    gopls
    shfmt
    uni
    runme

    # cargo + misc
    ripdrag
    php
    font-awesome
  ];

  programs.home-manager.enable = true;
}
