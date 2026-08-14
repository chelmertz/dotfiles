# mediabox-add: content-based identification, archive support, deletion prompt

Date: 2026-08-14
Status: approved

## Problem

`bin/mediabox-add` routes files to the media box by **file extension alone**. An
extension is a claim made by whoever named the file, not evidence about its
contents. The script also carries three concrete transfer bugs and offers no way
to reclaim local disk after a successful send.

Measured behaviour of `file(1)` 5.45 on synthetic ROM headers, which motivated
the identification design:

| fixture | `file(1)` verdict |
| --- | --- |
| iNES | `NES ROM image (iNES): 2x16k PRG, 1x8k CHR` |
| Genesis | `Sega Mega Drive / Genesis ROM image` |
| Game Boy | `Game Boy ROM image: "TETRIS"` |
| N64 (z64) | `data` |
| SNES (LoROM) | `ISO-8859 text, with very long lines` |

`file(1)` has no rule for N64 or SNES. Shelling out to it would therefore leave
the two formats it cannot name unprotected, so probes are implemented natively.

## Goals

1. Identify by content, not by name. Deny by default.
2. Stop executables at the door — nothing unidentified leaves the laptop.
3. Accept `.zip` and `.7z` safely, extracting locally.
4. Auto-resolve the disc-image system prompt where evidence allows.
5. Offer to reclaim local disk once a send is verified.

## Non-goals

Config file, parallel transfers, resume, library scanning, renaming, scraping.

## Language and packaging

Go, following the existing `keylogger/` precedent: a `mediabox/` directory with
`go.mod` and `mediabox.nix` (`buildGoModule`), wired into `home.packages`. The
`bin/mediabox-add` entry is removed from `nix/bin.nix`.

The choice is driven by the work, not by taste: byte reads at fixed offsets,
ISO9660 directory walking, and bounded archive extraction. Bash has none of
these natively and would shell out for all three, which is precisely the
attack surface being removed.

## Identification ladder

Three independent signals. No single signal is authoritative.

1. **Extension** — produces a candidate set. A hint, never a verdict.
2. **Content probe** — byte signatures at known offsets.
3. **Size window** — per-system plausibility bounds.

### Danger gate (runs first)

Applied to the file's own leading bytes, before any probe. Rejects ELF
(`\x7fELF`), PE/DOS (`MZ`), Mach-O, Java class, OLE/MSI (`D0 CF 11 E0`), Windows
shortcut, and shebang (`#!`). An extension blocklist (`.exe .dll .so .sh .bat
.cmd .ps1 .vbs .js .jar .msi .com .scr .pif`) rejects regardless of content,
which also defeats the `movie.mkv.exe` double-extension trick since the last
extension is the one read.

The gate applies to the outer file only. A PS1 disc image legitimately contains
executables; that is its payload, not its signature.

### Content probes

| System | Evidence |
| --- | --- |
| NES | `NES\x1a` at 0 |
| N64 | `0x80371240` (z64/BE), `0x37804012` (v64), `0x40123780` (n64/LE); byte order reported |
| Game Boy | 48-byte Nintendo logo at 0x104; CGB flag at 0x143 splits gb/gbc |
| GBA | 156-byte Nintendo logo at 0x004 plus fixed `0x96` at 0xB2 |
| Genesis | `SEGA` at 0x100 |
| SMS / GG | `TMR SEGA` at 0x1FF0, 0x3FF0 or 0x7FF0 |
| SNES | internal header at 0x7FC0 (LoROM) / 0xFFC0 (HiROM): checksum XOR complement must equal `0xFFFF`; also yields the mapping |
| Video | container magic — Matroska `1A 45 DF A3`, `ftyp` at 4, etc. |

SNES has no magic number, which is why `file(1)` cannot name it. The
checksum/complement identity is a real verifiable invariant and is used instead.

### Disc images

Locate `CD001`, handling both 2048-byte and raw 2352-byte sector layouts, then
walk the ISO9660 root directory:

- `SYSTEM.CNF` with `BOOT=cdrom:\SLUS_|SLES_|SCUS_|SCES_` → **ps1**
- `UMD_DATA.BIN`, or `SYSTEM.CNF` with `BOOT2=` → **psp**
- `IP.BIN` / `SEGA SEGAKATANA` → **dreamcast**

`.cue` files are parsed as text and their `FILE` targets resolved, so a cue and
its bin are handled as one unit. This fixes the "asked twice for one game"
problem structurally rather than by remembering the previous answer.

A strong content match resolves the system with no prompt. When no probe fires,
the size window supplies a *suggested* default (≤800 MB → ps1, ≤1.4 GB →
dreamcast, above → psp) and the user is still prompted, with that suggestion
preselected. Size never overrides a positive content match; a positive match at
an implausible size is refused rather than guessed — the 4 GB `.sfc` case.

## Archives

Always extracted locally. Nothing is written to disk until the index alone has
cleared every check.

1. Read the index only — zip central directory, or 7z header.
2. Reject: absolute paths, any `..` component, backslash or drive-letter names,
   symlink and device entries, entry count over cap, declared uncompressed total
   over cap, per-entry compression ratio over cap.

   The declared-total cap is what actually bounds the damage, since extraction
   enforces each entry's declared size exactly. The ratio cap is a cheap early
   warning on top and must stay generous: cartridge ROMs are mostly padding and
   legitimately compress around 1000:1, while real bombs run to a million to one.
   A tight ratio only refuses ordinary games — confirmed against a 4 MB padded
   ROM, which an earlier 300:1 cap rejected.
3. Classify members: **payload** (routable), **companion** (`.txt .nfo .md5
   .sfv .jpg .png .diz`), **unknown** (including nested archives).
4. **Zero payload members → do not extract, do not delete, report and move on.**
5. **Any unknown member → send the payload, but mark the bundle not deletable.**
   Companions never block deletion.
6. Extract payload only, into a `0700` temp dir, writing **basenames only** so
   traversal is structurally impossible rather than merely checked. Each entry
   is copied through an `io.LimitedReader`; bytes written must match the
   declared size, so a lying header cannot fill the disk.
7. Re-run the full ladder, danger gate included, on the **extracted bytes**. The
   index name was only ever a hint.

`.7z` uses pure-Go `github.com/bodgit/sevenzip` rather than shelling out to
p7zip: no external binary, no shell, same code path.

## Transfer

Bugs fixed in the current script, all live rather than hypothetical:

- A filename containing `:` makes rsync read `foo:bar.mkv` as a remote host.
- `mapfile` over a newline-joined string breaks on filenames containing newlines.
- `ssh "mkdir -p '$dest'"` builds a remote shell command by string concatenation.
- `USER` shadows the standard environment variable.

Go passes argv through `exec.Command` and never `sh -c`. Sources are resolved to
absolute paths, which removes the colon ambiguity by construction, and a `--`
separator handles leading dashes. `rsync --mkpath` replaces the remote `mkdir`.
Destinations are checked against a compiled-in allowlist of roots.

## Verification and deletion

Verification is `rsync --checksum --dry-run --itemize-changes` over the same
set. If nothing is itemized, remote content matches; this costs no second full
network read.

One atomic prompt at end of run, defaulting to **N**:

```
verified on mediabox:
  games/roms/snes/Super Mario World.sfc   512K
  media/movies/Arrival.mkv                4.2G

delete 2 local files? (+1 extracted temp file, removed either way)
  ~/Downloads/smw.zip     archive, fully consumed
  ~/Downloads/Arrival.mkv
[y/N]
```

Bundles held back are listed separately with their reason. The temp extraction
dir is removed unconditionally on `defer`, whichever way the prompt is answered.

## Testing

Table-driven tests over fixtures generated in-test, covering each probe, each
size bound, and the danger gate. Adversarial archive cases: traversal entry,
absolute path, symlink entry, ratio bomb, lying declared size, payload-free
archive, nested archive. The transfer layer sits behind an interface so
`go test ./...` runs offline with no media box present.
