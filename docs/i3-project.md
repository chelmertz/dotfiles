# i3-project (removed)

`bin/i3-project` gave i3 named "project" workspaces. It was removed in favour of
p-launcher. The last commit that
contains the implementation is `49f42d4`; recover it with
`git show 49f42d4:bin/i3-project`.

## What it did

**Named project slots.** Workspaces 2, 4, 5, 8, 3, 6 (in that preference order)
could carry a project name, shown as `N - name` in the bar. The slot-to-name map
lived in `~/.config/i3/projects` as `slot:name` lines. Names were unique; a
clash produced a `notify-send` and no change.

**Menu (`i3-project menu`, `$mod+n`).** A rofi list of active projects plus
`+ new project`, `~ restore`, and, when the focused workspace was a project,
`~ rename: X` and `x close: X`. Typing an unknown name created a project: on a
free project slot it labelled the current workspace, otherwise it took the next
free slot. Close reverted the workspace name to its bare number.

**Rofi modi (`i3-project rofi-modi`).** The same list as a `projects` modi
inside `bin/rofi.sh`'s combi view, without the actions that need a second
prompt.

**Switch (`i3-project switch N`).** Bound to `$mod+2..6`, `$mod+8` and F2-F4.
Went to workspace N and re-applied the project label if i3 had lost it (i3 drops
the name when a workspace empties and is recreated).

**Restore (`i3-project restore`).** Re-labelled the fixed workspaces (1 browser,
7 steam, 9 slack) and every project workspace, then, if an external monitor was
active, put workspace 9 on the laptop panel (`eDP-1`) and everything else on the
external. It ran at i3 startup, at the end of the rofi screen-layout picker, and
from the autorandr post-switch hook on tau.

**Per-project URL lists.** Each project could keep URLs in
`~/.config/i3/project-urls/<name>.txt`, surfaced as an i3blocks block (signal
11) showing a link icon and a count. Left click opened every URL and focused
Firefox; middle click added the URL on the clipboard (falling back to the
primary selection, `http*` only); right click opened a zenity dialog to remove
entries. Renaming a project moved its URL file.

**Other consumers.** `bin/xx` treated labelled slots as occupied when picking a
free workspace, so it never renamed a project's workspace out from under it.

## What replaced each part

The number keys and F2-F4 are plain `workspace number` switches, with
`workspace_auto_back_and_forth` so pressing the current workspace's key returns
to the previous one. Workspace labels come only from the `$wsN` variables in
`.i3/config`. Nothing pins workspaces to monitors any more. The URL lists and
the menu were not carried over.

The state files (`~/.config/i3/projects`, `~/.config/i3/project-urls/`) are left
on disk and are no longer read.
