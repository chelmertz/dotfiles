{ pkgs, ... }:
{
  home.username = "ch";
  home.homeDirectory = "/home/ch";
  home.stateVersion = "24.05";

  home.packages = with pkgs; [
    arandr
    asciinema
    autorandr
    bat
    brightnessctl
    cloc
    copyq
    entr
    fd
    figlet
    flameshot
    font-awesome
    fzf
    gh
    git
    git-filter-repo
    gleam
    gopls
    graphviz
    gron
    html-tidy
    htop
    i3blocks
    jq
    lazydocker
    lazygit
    libreoffice
    litecli
    meld
    nmap
    pandoc
    pavucontrol
    peek
    pgcli
    php
    playerctl
    redshift
    ripdrag
    ripgrep
    rofi
    runme
    screenkey
    shellcheck
    shfmt
    sqlite
    sqlitebrowser
    tree
    uni
    visidata
    vlc
    w3m
    wmctrl
    xcape
    xclip
    xdotool
    xsel
    yq
    yt-dlp
    zizmor
  ];

  programs.home-manager.enable = true;
}
