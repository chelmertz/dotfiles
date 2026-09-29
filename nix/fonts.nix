{ lib, pkgs, ... }:
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

  # fontconfig's stock 30-metric-aliases.conf lists TeX Gyre Heros as a
  # metric-compatible Helvetica, and NixOS ships gyre-fonts in its default X11
  # font set, so Helvetica resolved to it ahead of Arial. Heros sets the dot of
  # its 'i' almost onto the stem at 16px, so "liabilities" reads as a picket
  # fence on any page whose stack reaches Helvetica before Arial -- Notion's
  # does. Arial is metric-compatible too and hints cleanly at that size. The
  # same rule maps Times and Courier onto Gyre faces, but those two render
  # fine, so they are deliberately left alone.
  # Both font fixes in this file fail silently -- nothing errors, an app just
  # renders wrongly, and only when someone opens it. Chromium resolves the CSS
  # generics through Arial, Times New Roman and Courier New, so losing corefonts
  # (a pname rename breaking the unfree allowlist would do it) leaves every
  # Chromium generic with no font: Steam painted images and nothing else on
  # 2026-09-28. Asserting the generics would not catch that -- fc-match returned
  # DejaVu Sans for sans-serif the whole time Steam was blank -- so assert the
  # three names Chromium actually asks for. Helvetica is the Notion fix above,
  # which a new default font outranking Arial would undo. Warn rather than fail,
  # so a nixpkgs-side font change cannot block an unrelated switch.
  home.activation.fontResolutionCheck = lib.hm.dag.entryAfter [ "linkGeneration" ] ''
    fontExpect() {
      got=$(${pkgs.fontconfig}/bin/fc-match -f '%{family}' "$1" 2>/dev/null)
      if [ "$got" != "$2" ]; then
        echo "fonts: '$1' resolves to '$got', expected '$2' -- see nix/fonts.nix" >&2
      fi
    }
    fontExpect Arial             Arial
    fontExpect "Times New Roman" "Times New Roman"
    fontExpect "Courier New"     "Courier New"
    fontExpect Helvetica         Arial
  '';

  xdg.configFile."fontconfig/conf.d/51-helvetica-prefer-arial.conf".text = ''
    <?xml version="1.0"?>
    <!DOCTYPE fontconfig SYSTEM "urn:fontconfig:fonts.dtd">
    <fontconfig>
      <alias binding="same">
        <family>Helvetica</family>
        <prefer><family>Arial</family></prefer>
      </alias>
    </fontconfig>
  '';
}
