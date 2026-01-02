{ pkgs, ... }:
{
  home.username = "ch";
  home.homeDirectory = "/home/ch";
  home.stateVersion = "24.05";

  # for standard packages, without any custom configuration; otherwise, remove from this list and do "program.myprogram = { enable = true; .. other options}"
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

  programs.direnv = {
    # this requires eval direnv in ~/.zshrc as long as I manage it manually -
    # it will be autosolved when home-manager manages zshrc
    enable = true;
    nix-direnv.enable = true;
  };
}
