{ pkgs, ... }:
{
  fonts.fontconfig.enable = true;

  home.packages = with pkgs; [
    font-manager

    # Chromium resolves the CSS generics (sans-serif, serif, monospace) through
    # preferences naming Arial, Times New Roman and Courier New, and then
    # discards the Liberation fonts fontconfig substitutes for them. Every run
    # of text that does not name an installed family then gets no font at all.
    # Steam's client was the visible casualty on 2026-09-28: it painted images
    # and its own Motiva Sans webfont and nothing else, because its remaining
    # text falls through to sans-serif. Measured inside Steam's CEF, "16px
    # sans-serif" advanced 0px before this package and 112px after. corefonts
    # carries the literal Arial, Times New Roman and Courier New, which is the
    # one thing that match cannot reject.
    corefonts

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
