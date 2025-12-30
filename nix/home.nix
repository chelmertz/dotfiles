{ pkgs, ... }:
{
  home.username = "ch";
  home.homeDirectory = "/home/ch";
  home.stateVersion = "24.05";

  home.packages = with pkgs; [
    fzf
    jq
    tree
    ripgrep
  ];

  programs.home-manager.enable = true;
}
