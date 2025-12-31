{ pkgs, ... }:
{
  home.username = "ch";
  home.homeDirectory = "/home/ch";
  home.stateVersion = "24.05";

  home.packages = with pkgs; [
    fzf
    gh
    git
    gron
    jq
    meld
    ripgrep
    shellcheck
    tree
    vlc
  ];

  programs.home-manager.enable = true;
}
