# i3bar in the cards palette

Date: 2026-09-08
Status: approved

## Problem

Rofi, dunst and picom share one visual language since 2026-09-07: `cardRadius`
14, `#1e1e1e` / `#f5f5f7` cards with a 1px hairline, `#4a8fe7` / `#0a7aff`
accent, Inter. i3bar still renders with stock colours: bordered grey workspace
buttons, `#285577` focus, the window-title font, and blocks that each pick their
own colour (gold quote, lime PR count, orange redshift, blue bluetooth, pure
white weather). Emoji (🖵 ⌨ ⚪ 🔴 ⚡) sit next to Font Awesome glyphs. The bar
also ignores the dark/light scheme that rofi follows.

Preview of the chosen direction, option C ("Accent fill"), next to the two
alternatives: https://claude.ai/code/artifact/daf2e59a-ff7d-418c-a5e3-d350091589c9

## Goals

1. Bar colours from the same palette values rofi uses, in both schemes.
2. The bar follows the colour-scheme toggle together with rofi.
3. One glyph font for every block; no emoji.
4. Block colour by role, not by hex in each script.
5. Tuning the focused workspace down to the quiet variant is a one-line change.

## Non-goals

Dunst light variant. Title font. Rounded bar or workspace
pills (i3bar cannot round; picom rounding was tried and reverted on
2026-09-07). Polybar. Changing the elly block, which lives in the elly repo.

## Verified constraints

Checked with `i3 -C -c <file>` on i3 4.25.1:

- `include` of a file holding a complete `bar { … }` block at top level: passes.
- `include` inside a `bar { }` block: `ERROR: CONFIG`. The whole bar block must
  therefore live in the included file.
- `include` of a missing file: passes silently, and the bar is simply absent.
  Hence the activation step below.

`gsettings get org.gnome.desktop.interface color-scheme` takes 4 ms, cheap
enough for every block run.

Every glyph named below exists in the installed Font Awesome 7 Free Solid face,
except bluetooth (`f293`) which is in Font Awesome 7 Brands. `NFM.ttf` (Nerd
Font) also claims the `f0xx`–`f2xx` range, so the bar font must name the
Font Awesome faces explicitly; fontconfig fallback would be arbitrary.

## Design

### Palette as one source

`nix/home.nix` gains, next to `cardRadius`, one attrset with both schemes:

```nix
cards = {
  dark  = { bg = "#1e1e1e"; fg = "#f2f2f2"; muted = "#8a8a8a"; border = "#292929";
            field = "#2a2a2a"; selected = "#333333"; accent = "#4a8fe7"; red = "#e5484d"; };
  light = { bg = "#f5f5f7"; fg = "#1d1d1f"; muted = "#55555a"; border = "#d5d5da";
            field = "#e9e9ee"; selected = "#dcdce2"; accent = "#0a7aff"; red = "#d70015"; };
};
```

These are the values currently hand-written in `rofi/cards-dark.rasi` and
`rofi/cards-light.rasi`. Those two files are replaced by a single template,
`rofi/cards-colors.rasi`, with `@bg@`-style placeholders that nix substitutes
per scheme, the same way `cards.rasinc` already receives `@radius@`. The
generated files keep their names and install paths, so `bin/color-scheme` and
the p-launcher goldens are unaffected. Dunst keeps its literal values for now.

### Scheme file, swappable per scheme

Added after the first live round: the focused window border uses the same
accent as the focused workspace button, so the generated file also carries
the `client.*` colours (focused = accent, focused_inactive = field,
unfocused = bg with muted text, urgent = red) and replaces the mint
`client.focused` line in `.i3/config`.


Home-manager generates two complete bar blocks from one nix function,
`~/.config/i3/scheme-dark.conf` and `~/.config/i3/scheme-light.conf`. `.i3/config`
loses its inline `bar { … }` and gains:

```
include ~/.config/i3/scheme.conf
```

`~/.config/i3/scheme.conf` is an unmanaged symlink, the counterpart of the
unmanaged `~/.config/rofi/config.rasi`. `bin/color-scheme` repoints it with
`ln -sfn` after writing the rofi theme, then runs `i3-msg reload`, ignoring
failure (no i3 running). A `home.activation` step creates the symlink when it
is missing, choosing the variant matching the current gsettings scheme and
defaulting to dark, so a fresh switch never leaves the bar absent.

Settings shared by both variants:

```
bar {
    status_command i3blocks
    position top
    font pango:Inter, Font Awesome 7 Free, Font Awesome 7 Free Solid, Font Awesome 7 Brands 15
    tray_output eDP-1
    tray_output primary
    tray_padding 4
    workspace_min_width 40
    padding 2 14 2 10
    colors { … }
}
```

`font` inside the bar block affects the bar only; window titles keep the
current title font. `padding` exists because i3bar insets the statusline from
the screen edge only when a tray shares that output, and the tray now lives on
the laptop screen.

Colours, dark shown, light by substitution:

```
colors {
    background         #1e1e1e
    statusline         #8a8a8a        # muted: default block colour
    separator          #292929        # border: thin rule between groups
    focused_workspace  #1e1e1e #1e1e1e #f2f2f2   # text ramp, no box
    active_workspace   #1e1e1e #1e1e1e #8a8a8a   # visible on the other output
    inactive_workspace #1e1e1e #1e1e1e #6a6a6a   # dim
    urgent_workspace   #e5484d #e5484d #1e1e1e   # the one fill on the bar
    binding_mode       #2a2a2a #2a2a2a #f2f2f2   # field: resize / fkey mode
}
```

The accent fill went first to a quiet `bg bg fg`, which turned out to be
unidentifiable: `fg` against `muted` is 2.27:1 in light. A 1px hairline was
tried next and dropped for reintroducing the borders the rest of the bar had
just given up.

An earlier revision of this section claimed the button could not be boxed at
all, because i3bar insets it 1px from the bar top and 4px from the bottom.
That was measured against a bar height read off a screenshot, and the height
was wrong: `xwininfo` reports the bar at 25px, where the button occupies rows
1..23 and is centred. A fill is therefore available if the ramp ever proves
too quiet.

The cause was the palette, not the lever. `muted` had been darkened to
`#55555a` for the block glyphs, which left an inactive workspace nearly as
dark as the focused one. A `dim` token gives the workspace ramp its own
lighter grey, so text alone carries it: focused `fg`, visible on another
output `muted`, everything else `dim`, at 6.4:1 between focused and inactive
in light. `dim` is the bar's own; the rofi template does not use it. Urgent
keeps a fill as the one alarm on the bar. The accent marks the focused window
only.

### Block colour by role

A generated script `~/.local/bin/i3blocks-color <role>` prints the hex for
`fg`, `muted`, `accent` or `red` in the scheme gsettings reports, dark when
gsettings fails or reports anything but `prefer-light`. It is emitted from the
same `cards` attrset, so the bar, rofi and the blocks cannot drift.

Policy, applied in every block script in this repo:

Revised after the design critique of the first live round (macOS renders every
menu-bar extra in one label colour and shows state by glyph shape; the accent
belongs to one thing):

- Default: print no colour line. i3bar paints it with `statusline`, which is
  `fg`.
- A toggle that is off (redshift, bluetooth disconnected) or a paused track:
  `muted`.
- An alarm (recording, keylog capturing, prometheus firing, battery low):
  `red`.
- `accent` is never printed by a block; it marks the focused workspace and
  window only.

No rules between groups, only air: `separator=false` globally,
`separator_block_width` 14 inside a group and 24 after its last block. Inside
a block the unit is one space, from either the label's trailing space or the
script, never both. The clock is three blocks (`date`, `week`, `clock`) so the
gaps within it are the bar's 14px unit; the two literal spaces it used before
were a third rhythm between the 5px label unit and the 14px block unit. Glyphs
come from Font Awesome 7 Regular where a regular variant exists, Solid
otherwise. The tray sits on the laptop output. Groups, left to right:

| group | blocks |
| --- | --- |
| quote | quote |
| work | project-urls, recording, keylog, elly, prometheus |
| environment | wttr, battery |
| media | mediaplayer |
| clock | date, week, clock |
| toggles | redshift, bluetooth, screenlayout, colorscheme |

### Per-block changes

All glyphs Font Awesome 7. `color=` lines in `.i3blocks.conf` are removed.

| block | file | change |
| --- | --- | --- |
| quote | `.i3blocks.conf` | keep label `f10e`; drop `color=#ffcc00` |
| project-urls | `bin/i3-project` | keep `f0c1`; drop `#8fabc7`; `fg` when count > 0 |
| recording | `bin/i3blocks_recording.sh` | 🔴 REC → `f111` REC in `red`; ⚪ → `f192`, no colour |
| keylog | `bin/i3blocks_keylog.sh` | ⌨ → `f11c`; capturing → `red` |
| elly | elly repo | unchanged here; prints `#00ff00` until the elly repo adopts `i3blocks-color fg` |
| prometheus | `bin/i3blocks_prometheus` | firing count in `red` |
| wttr | `.i3blocks.conf` | `format=%t` with label `f2c9`; drop `color=#ffffff`. Condition dropped: wttr's plain-text `%x` came back as `mmm` |
| battery | `bin/battery` | ⚡ label → level glyph `f244`…`f240` by quarter, `f0e7` when charging; drop the colour gradient; `red` below 15 %, `urgent` kept |
| mediaplayer | `bin/i3blocks_spotify.sh` | keep `f28b` / `f004`; `fg` when playing, no colour when paused |
| date, week, clock | `bin/i3blocks_date.sh` | one block per part, no icons: `tis 8 sep`, `W37`, `14:53` |
| redshift | `bin/i3blocks_redshift.sh` | keep `f0eb`; on → `accent`, off → no colour |
| bluetooth | `bin/i3blocks_bluetooth.sh` | keep `f293`; connected → `accent`, else no colour |
| screenlayout | `.i3blocks.conf` | 🖵 → `f108` |
| colorscheme | `bin/i3blocks_colorscheme.sh` | keep `f186` / `f185`; drop `#b0c4de` / `#ffcc00` |

### Error handling

- gsettings unavailable: `i3blocks-color` prints the dark value.
- `i3-msg reload` fails (no i3, or a config error): `color-scheme` reports it on
  stderr and still exits 0, so the rofi and gsettings half of the toggle stands.
- `scheme.conf` missing: activation step recreates it; `i3 -C` cannot catch this
  because a missing include passes, so the activation step is the guard.
- A generated bar file that fails to parse: `i3 -C -c ~/.config/i3/config` after
  `home-manager switch` is the check, and `i3-msg reload` refuses a broken
  config and keeps the running one.

## Verification

1. `home-manager --option warn-dirty false switch --flake ./nix#ch` from the
   worktree.
2. `i3 -C -c ~/.config/i3/config` exits 0.
3. `~/.local/bin/i3blocks-color accent` prints `#0a7aff` under prefer-light and
   `#4a8fe7` after the toggle.
4. Click the colorscheme block twice: bar, rofi and block colours flip together
   both ways, and `readlink ~/.config/i3/scheme.conf` follows.
5. `go test ./p-launcher/...` passes: the rofi goldens render against the
   installed theme, so they confirm the generated rasi files match the
   hand-written ones they replace.
6. Screenshot both schemes for the PR.

## Follow-ups (separate PRs)

- elly repo: `contrib/elly_i3blocks.sh` uses `i3blocks-color fg`.
- Dunst light variant, driven from the same `cards` attrset and swapped by
  `bin/color-scheme` via `dunstctl reload`.
- Window frames and titles: `client.*` colours from `cards`, Inter titles.
- Terminal palette: neutral dark/light pair matching `cards.bg`.
