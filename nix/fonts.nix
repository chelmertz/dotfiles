{ lib, pkgs, ... }:
let
  # Claude Code's question tabs print U+2610/U+2612, which every installed
  # font draws as a small or thin box. ghostty's font-codepoint-map picks a
  # font per code point, not a glyph, so the Material checkboxes in Symbols
  # Nerd Font (U+F0131 blank, U+F0132 checked) have to be copied onto the
  # ballot-box code points in a font of their own.
  ballot-nerd = pkgs.runCommand "ballot-nerd-font" {
    nativeBuildInputs = [ (pkgs.python3.withPackages (ps: [ ps.fonttools ])) ];
  } ''
    mkdir -p $out/share/fonts/truetype
    python3 - ${pkgs.nerd-fonts.symbols-only}/share/fonts/truetype/NerdFonts/Symbols/SymbolsNerdFontMono-Regular.ttf $out/share/fonts/truetype/BallotNerd.ttf <<'EOF'
    import sys
    from fontTools.ttLib import TTFont
    from fontTools.subset import Subsetter
    font = TTFont(sys.argv[1])
    cmap = font.getBestCmap()
    blank, checked = cmap[0xF0131], cmap[0xF0132]
    sub = Subsetter()
    sub.populate(unicodes=[0xF0131, 0xF0132])
    sub.subset(font)
    for table in font["cmap"].tables:
        if table.isUnicode():
            table.cmap = {0x2610: blank, 0x2611: checked, 0x2612: checked}
    # The icons fill the whole 2048-unit em, descender included, and the font
    # declares x-height and cap height as 0, so ghostty drew them at a fraction
    # of a letter. Redraw them as a box exactly Go Mono's cap height, and copy
    # Go Mono's metrics so ghostty sizes this face like the primary one. The
    # box is wider than the 1229-unit cell: ghostty lets a symbol spill into a
    # following blank cell and shrinks it to one cell otherwise, and a square
    # inside one cell is only 80% of a capital. Go Mono's numbers, read with
    # fontTools.
    from fontTools.pens.ttGlyphPen import TTGlyphPen
    from fontTools.pens.transformPen import TransformPen
    advance, cap_height, x_height, ascent, descent = 1229, 1480, 1086, 1935, 432
    scale = cap_height / 2048
    dx, dy = 24, round(410 * scale)
    glyphs = font.getGlyphSet()
    for name in {blank, checked}:
        pen = TTGlyphPen(glyphs)
        glyphs[name].draw(TransformPen(pen, (scale, 0, 0, scale, dx, dy)))
        font["glyf"][name] = pen.glyph()
        font["hmtx"][name] = (advance, dx)
    os2, hhea = font["OS/2"], font["hhea"]
    os2.sxHeight, os2.sCapHeight, os2.xAvgCharWidth = x_height, cap_height, advance
    os2.sTypoAscender, os2.sTypoDescender = ascent, -descent
    os2.usWinAscent, os2.usWinDescent = ascent, descent
    hhea.ascent, hhea.descent, hhea.advanceWidthMax = ascent, -descent, advance
    for rec in font["name"].names:
        if rec.nameID in (1, 4, 16):
            rec.string = "Ballot Nerd"
        elif rec.nameID == 6:
            rec.string = "BallotNerd-Regular"
    font.save(sys.argv[2])
    EOF
  '';
in
{
  fonts.fontconfig.enable = true;

  home.packages = with pkgs; [
    font-manager
    ballot-nerd

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
    nerd-fonts.comic-shanns-mono

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

  # The fontconfig half of the subpixel settings in nix/home.nix's
  # xresources.properties. Firefox and Chromium read these rather than the Xft
  # resources, so both halves have to say the same thing. NixOS defaults rgba
  # to "none", which leaves every glyph greyscale-antialiased and the lcdfilter
  # setting doing nothing.
  xdg.configFile."fontconfig/conf.d/53-subpixel.conf".text = ''
    <?xml version="1.0"?>
    <!DOCTYPE fontconfig SYSTEM "urn:fontconfig:fonts.dtd">
    <fontconfig>
      <match target="font">
        <edit name="rgba" mode="assign"><const>rgb</const></edit>
      </match>
      <match target="font">
        <edit name="lcdfilter" mode="assign"><const>lcddefault</const></edit>
      </match>
    </fontconfig>
  '';

  # macOS and Windows answer the system-UI family names with a face drawn for
  # screens (SF Pro, Segoe UI Variable). Linux answers with nothing, so a stack
  # like Notion's -- ui-sans-serif, -apple-system, BlinkMacSystemFont, "Segoe UI
  # Variable Display", "Segoe UI", Helvetica, ..., Arial -- falls past every one
  # of them to Helvetica and lands on Arial: a 1982 print grotesque with no
  # optical sizing. Inter is drawn for UI at text sizes, which is the rung Linux
  # is missing. Adwaita Sans is in the set because it is this desktop's declared
  # font-name (gsettings org.gnome.desktop.interface) and is not installed, so
  # it is how Firefox resolves system-ui. Firefox does not implement
  # ui-sans-serif at all; Chromium-based apps do, hence claiming both.
  xdg.configFile."fontconfig/conf.d/52-system-ui-inter.conf".text = ''
    <?xml version="1.0"?>
    <!DOCTYPE fontconfig SYSTEM "urn:fontconfig:fonts.dtd">
    <fontconfig>
      <alias binding="same">
        <family>system-ui</family>
        <prefer><family>Inter</family></prefer>
      </alias>
      <alias binding="same">
        <family>ui-sans-serif</family>
        <prefer><family>Inter</family></prefer>
      </alias>
      <alias binding="same">
        <family>-apple-system</family>
        <prefer><family>Inter</family></prefer>
      </alias>
      <alias binding="same">
        <family>BlinkMacSystemFont</family>
        <prefer><family>Inter</family></prefer>
      </alias>
      <alias binding="same">
        <family>Segoe UI</family>
        <prefer><family>Inter</family></prefer>
      </alias>
      <alias binding="same">
        <family>Segoe UI Variable Display</family>
        <prefer><family>Inter</family></prefer>
      </alias>
      <alias binding="same">
        <family>Segoe UI Variable Text</family>
        <prefer><family>Inter</family></prefer>
      </alias>
      <alias binding="same">
        <family>Adwaita Sans</family>
        <prefer><family>Inter</family></prefer>
      </alias>
    </fontconfig>
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
