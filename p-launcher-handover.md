# p-launcher: unit PATH failures, and the class they belong to

Written 2026-09-11. Scratch file, delete once the open items below are done.

## What broke

`p-launcher-links.service` failed on every timer firing on tau — every 10
minutes, all day. The unit runs two commands:

```
p-launcher links refresh
p-launcher tend
```

`links refresh` succeeded. `tend` died on the first PR it decided to act on:

```
tend .../pull/26: start ghostty for /home/ch/p/m/mfa-superadmin:
  exec: "ghostty": executable file not found in $PATH
notify-send also failed: exec: "notify-send": executable file not found in $PATH
```

The unit pinned `Environment = "PATH=${lib.makeBinPath [ pkgs.gh ]}"`. `gh` is
all `links refresh` needs, but `tend` needs considerably more:

- **`ghostty`** — `tendApply` calls `launch()` in `open.go`, which forks
  `ghostty` by bare name.
- **`zsh` and `claude`** — `launchArgs` builds
  `ghostty … -e zsh -ic "claude; exec zsh"`, and `launch` passes
  `cmd.Env = scrubEnv(os.Environ())`. `scrubEnv` only strips `CLAUDE*`, so the
  ghostty it starts inherits *this same PATH*. Both have to resolve from it, or
  the terminal opens onto nothing.
- **`notify-send`** — how `main.go` reports a failure. It was missing too, so
  the message about the missing ghostty also vanished.

Fixed in `f96b374` by appending `%h/.nix-profile/bin`, which is what
`p-launcher-brief` one unit below already did. All of `ghostty`, `zsh`,
`claude`, `notify-send` and `i3-msg` resolve there on tau.

`tend`'s launch path does **not** need `i3-msg`: it calls `launch()` directly
rather than `openWith()`, so `waitForTagged`/`getTree` never run. The F5 menu
path does need it, but that runs in the user's session, not the unit.

## Why it hid for a day

`tend` is inert until `p-launcher kv set tend.enabled 1`, and even enabled it
only acts once a PR qualifies. Until one did, the unit exited 0. gamma never
saw it at all — `tend.enabled` is unset there.

The 2026-09-10 sibling sweep checked `systemctl` status for `obsidian-poll`,
`p-launcher-brief`, `p-launcher-links`, `p-launcher-backup` and `elly`, found
them all `success`, and called them clean. **Exit status cannot prove anything
about a branch that has not been taken.** That is the whole lesson.

## The class

A systemd user unit pins a minimal PATH; the program forks a binary that is not
on it. This repo's own comments record three earlier rounds:

| Unit | Missing | Symptom |
|---|---|---|
| `prom-system-health` | `gh` | Reported a logged-out account on a logged-in machine |
| `obsidian-poll` | `find`, `gh`, `git` | `/usr/bin` stopped existing on NixOS |
| `spotify-backup` | `ssh` | `git push` to a `git@` remote, "cannot run ssh" |
| `p-launcher-links` | `ghostty`, `notify-send`, `zsh`, `claude` | The above |

Nine units in `nix/` pin a PATH. Swept properly on 2026-09-11 — resolving each
program's actual fork sites against that unit's own PATH on tau, rather than
reading status — the rest are clean: `http-monitor`, `prom-system-health`,
`obsidian-poll`, `spotify-backup`, `p-launcher-brief`, `p-launcher-backup`,
`mediabox-settings`, `domain-exporter`.

To repeat the sweep, per unit in `~/.config/systemd/user/*.service`: read
`Environment=PATH=`, expand `%h`, then `PATH=$that command -v <each forked
binary>`. The fork sites come from the program — `exec.Command` in Go,
`subprocess.run` in Python, command position in shell. Note `python3` in
`obsidian-poll` is a false positive: it is the interpreter, given as an
absolute store path in `ExecStart`, not something the script forks.

## What to do about it

**Wrapping, not a check.** The nixpkgs idiom is `makeWrapper` / `wrapProgram
--prefix PATH`, so a program carries its own runtime dependencies and no caller
has to know them. Pinning `Environment=PATH=` per unit puts the dependency list
somewhere the program's author never looks, which is why it keeps drifting.

For shell scripts, `writeShellApplication` takes `runtimeInputs` and runs
shellcheck at build time. `resholve` goes further: it rewrites every command
reference to an absolute store path and **fails the build** when one cannot be
resolved — an actual compile-time check for this exact class.

A smoke test does not work here. `cmd --version` exercises none of the
conditional branches that all four bugs lived on. The real equivalent is a
`pkgs.nixosTest` VM that boots and runs the unit, which is heavyweight and hard
to drive into "a PR qualifies for tend".

A `nix flake check` over a hand-maintained unit-to-commands table would catch
regressions, and could enumerate units from the config so a new unit that pins
a PATH and is absent from the table fails the build. That converts "remember to
check" into "the build stops you". It is strictly worse than wrapping, though,
because the table still has to track each program's fork sites by hand.

## Open

- [ ] **Wrap the p-launcher binary** with its runtime dependencies
      (`ghostty`, `libnotify`, `zsh`, `i3`), then shrink
      `p-launcher-links`/`-brief`/`-backup` PATHs to what their own shell
      wrappers need. Smallest useful validation of the approach.
- [ ] Decide whether the other seven pinned-PATH units follow, or whether
      wrapping p-launcher is where it stops.
- [ ] `openssh` is not in `home.packages` at all. Any future unit that pins
      `%h/.nix-profile/bin` and expects `ssh` will hit the spotify-backup
      failure again.
- [ ] `tend` starts a Claude session in a terminal from a timer. Worth a
      deliberate look at what happens when several qualify at once. Two limits
      exist — `tendCfg.DailyCap`, and a `--max` flag defaulting to 2 in
      `main.go` — but the unit passes neither explicitly, and the behaviour has
      not been watched under load.
