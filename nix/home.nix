{ pkgs, ... }:
{
  home.username = "ch";
  home.homeDirectory = "/home/ch";
  home.stateVersion = "24.05";

  home.packages = with pkgs; [
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
  ];

  programs.home-manager.enable = true;
}
