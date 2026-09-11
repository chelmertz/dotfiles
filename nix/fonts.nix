{ pkgs, ... }:
{
  fonts.fontconfig.enable = true;

  home.packages = with pkgs; [
    font-manager

    # icon / symbol fonts
    font-awesome
    emacs-all-the-icons-fonts
    nerd-fonts.symbols-only

    # monospace / coding
    fira-code
    fira-mono
    go-font
    ibm-plex
    inconsolata
    iosevka
    # second choice in obsidian.nix's monospace stack, and undeclared until
    # 2026-09-11, so that entry silently fell through to Fira Code on both
    # machines. gamma never had it either; this is not a migration regression.
    jetbrains-mono
    martian-mono
    recursive
    roboto-mono
    source-code-pro

    # sans-serif
    hanken-grotesk
    inter
    plus-jakarta-sans
    public-sans
    roboto
    roboto-flex

    # serif / display
    libre-baskerville
    merriweather
    noto-fonts
    roboto-slab
    roboto-serif

    # icon fonts
    material-design-icons
  ];
}
