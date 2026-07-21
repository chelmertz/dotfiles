# keylog — design

A local keyboard-usage profiler. Captures keystrokes during manual **sessions**,
aggregates them **on the fly into counts** (no keystroke sequence ever hits disk),
and produces an actionable report: dead keys, per-finger load, same-finger bigrams,
per-context usage, i3-binding usage. Purpose: measure real usage on the current
keyboards to make informed **Glove80 layout** decisions.

Status: v1 core implemented and tested; live evdev/i3 capture wired but unrun
(needs the `input` group). See `README.md` for build/run and what's verified.
Reference for the target report output: `../scratchpad/keylog-report-mock.html`
(fabricated data, shows the intended shape of every insight).

## Goal & framing

- **Primary question:** where is effort going, and what should the Glove80 layout
  optimise for? (demote near-dead keys, offload the overloaded pinky, decide whether
  a coding layer / thumb keys earn their place, settle the åäö question.)
- **The value is front-loaded.** OS/evdev capture sees the keycodes a keyboard
  *emits*. The Glove80 runs ZMK — layers, combos, home-row-mods are resolved *in
  firmware*, so once on the Glove80 the captured keycodes no longer map to physical
  key positions and per-finger/SFB analysis stops being physically meaningful. So the
  useful measurement window is **now, on the non-programmable boards** (Keychron,
  laptop), where keycode == physical key. This tool is a pre-Glove80 instrument.

## Privacy posture

Raw ordered keystrokes are **never persisted**. The daemon holds the previous
keysym(s) in memory only long enough to increment bigram/skipgram counters, then
discards them. Only aggregate counts reach sqlite. Consequence: nothing on disk is a
reconstructable keylog, so no encryption is needed. The cost — bucketing dimensions
(device/app/filetype/...) must be chosen at capture time; re-slicing by a new
dimension means running a new session. Accepted.

## Non-goals (v1)

- No always-on capture. Capture happens only during an explicit session.
- No zsh/shell-command context feeder (planned later; socket protocol leaves room).
- No mid-session hotplug handling — the keyboard device set is snapshotted at session
  start; plugging/unplugging the Keychron mid-session is logged as a warning only.
- No LLM at runtime. All verdicts are deterministic rules (see Analysis).
- No oxeylyzer/HTML-export-to-external-tool bridge (the report is self-contained).

## Architecture

Single Go binary `keylog` in `keylogger/` (its own `go.mod`), several goroutines
sharing a small mutex-guarded context struct and a channel of key events:

- **Capture** — one goroutine per keyboard device. Enumerate `/dev/input/event*`,
  keep devices advertising `EV_KEY` with alpha keys (skip mice/other). Read events,
  emit only keydowns (`value == 1`); drop key-repeats (`value == 2`) and releases
  (`value == 0`) so a held key (games) doesn't skew counts. Library:
  `github.com/holoplot/go-evdev`.
- **i3 listener** — in-process subscriber (`go.i3wm.org/i3`). On `window::focus`,
  update shared context `{app: window_class, workspace}`.
- **Feeder socket** — unix socket at `$XDG_RUNTIME_DIR/keylog.sock`. External feeders
  send one-line JSON context updates. v1 feeder: neovim. Protocol below.
- **Aggregator** — owns the count maps and the rolling `prev` / skip-deque. On each
  keydown: update modifier state, resolve nothing (stores raw keycode), increment
  `unigram`, `bigram`, `skipgram` counters stamped with the current context. Flush to
  sqlite every ~5s via upsert (crash loses ≤5s of counts, never a session).

```
 /dev/input/event*  ──▶ Capture ─┐
 i3 IPC (focus)     ──▶ i3 listener ─┼─▶ Aggregator ──(5s upsert)──▶ sqlite
 nvim ▶ keylog ctx ─▶ socket ───────┘        │
                                     holds: current context,
                                     prev keycode, skip-deque,
                                     modifier state
```

## Data model (sqlite at `~/.local/share/keylog/keylog.db`)

```sql
sessions(
  id INTEGER PRIMARY KEY,
  started_at INTEGER,  -- unix seconds
  ended_at   INTEGER,
  host       TEXT,
  note       TEXT
);

-- one row per (context, key, modifier-combo); flushed via upsert
unigrams(
  session_id, device, app, workspace, filetype,
  keycode  INTEGER,
  modmask  INTEGER,      -- bitmask: Shift/Ctrl/Alt/Super held at press
  count    INTEGER,
  PRIMARY KEY (session_id, device, app, workspace, filetype, keycode, modmask)
);

bigrams(
  session_id, device, app, workspace, filetype,
  kc1 INTEGER, kc2 INTEGER,
  count INTEGER,
  interval_sum_ms INTEGER,   -- Σ inter-key gaps; mean = interval_sum_ms / count
  PRIMARY KEY (session_id, device, app, workspace, filetype, kc1, kc2)
);

skipgrams(  -- skip-1, for same-finger-skipgram (SFS)
  session_id, device, app, workspace, filetype,
  kc1 INTEGER, kc2 INTEGER,
  count INTEGER,
  PRIMARY KEY (session_id, device, app, workspace, filetype, kc1, kc2)
);
```

Flat context columns (not a normalized `contexts` table): fewer moving parts, and the
repeated strings are irrelevant at personal-session scale. The in-memory aggregator
keys its maps on `struct{ device, app, workspace, filetype string; keycode, modmask int }`.

### Why keycode + modmask, resolved late

The daemon stores raw `keycode` and `modmask` and resolves **meaning at report time**
via two static JSON maps — this keeps libxkbcommon/cgo out of the daemon and makes it
layout-agnostic:

- `sv.json`: `keycode → {base, shift}` char. With the Shift bit of `modmask`, resolves
  the frequency histogram and the åäö question.
- `qwerty-fingers.json`: `keycode → {finger, hand}`. Resolves per-finger load and
  same-finger detection. Valid for the physical (non-programmable) boards only.
- `modmask` is what makes the **i3-binding view free**: `Mod+Enter fired N×` is just
  `unigrams[keycode=Enter, modmask=Super]`; it also gives modifier-load and the Shift
  bit for char resolution. One field, three features.

### Capture semantics

- Keydown only; repeats/releases dropped.
- Modifier keypresses (Shift/Ctrl/Alt/Super) count as their own unigram rows *and*
  update the modifier state used to stamp subsequent keys.
- Bigram/skipgram stamp context from the **second** key.
- `prev` (and the skip-deque) **reset** on: idle gap > 1s, window focus change, and
  session start — so a pause or an alt-tab can't invent a phantom pair.

## Context feeders

- **i3** (app + workspace): in-process, no config beyond running under i3.
- **neovim** (filetype): an autocmd on `BufEnter`/`FileType` shells out to
  `keylog ctx --filetype=<ft> --buffer=<path>`, a tiny subcommand that writes one JSON
  line to the socket. No lua socket code; reuses the daemon's socket. When no session
  is running the socket is absent and `keylog ctx` no-ops silently.
- Socket message: `{"source":"nvim","filetype":"go","buffer":"main.go"}`. Daemon
  merges into current context; unknown sources ignored. Leaves room for a future zsh
  `preexec` feeder with `{"source":"zsh","command":"..."}`.

## Session lifecycle & CLI

Session == daemon lifetime.

- `keylog start [--note "..."]` — launch daemon, write a `sessions` row, begin capture,
  arm a 1h auto-stop timer.
- `keylog stop` — flush, set `ended_at`, exit.
- `keylog status` — running? which session, elapsed, live keydown count, current context.
- `keylog report [--session N] [--by app|device|filetype] [--html out.html]` — render
  metrics + verdicts. Default: latest session, terminal output.
- `keylog ctx --filetype=.. --buffer=..` — internal feeder command (writes to socket).

## Analysis

Two deterministic layers, no AI at runtime.

**Metrics** (pure functions over the counts):
- per-key frequency (share of keydowns), via `keycode→char`
- per-finger / per-hand load, via `keycode→finger`
- SFB rate + top offenders (bigrams where both keys share a finger)
- SFS rate (same, over skipgrams)
- slowest bigrams (`interval_sum_ms / count`)
- correction rate (Backspace / total), global and per-context
- per-context top keys and bracket/symbol frequency ratios
- i3-binding fire counts (unigrams with a Super bit) ∪ parsed `~/.config/i3/config`
  bindings (to surface never-fired ones)
- modifier load, number-row share, dead-key set (< 0.5%)

**Rule catalog** (~16 rules; each = threshold → templated sentence with the offending
keys/numbers slotted in; ranked by severity). Initial thresholds below are defensible
defaults, tunable after the first real sessions:

| # | Rule | Fires when | Severity |
|---|------|-----------|----------|
| 1 | Weak-finger overload | any finger load > 14% (pinky emphasised) | critical |
| 2 | High-freq key in weak position | key > 2% AND on pinky/awkward (Backspace, Enter, `-`) | optimise |
| 3 | Underused strong fingers | index finger load < 9% | info |
| 4 | SFB rate high | SFB% > 1% (>3% = worse) | optimise |
| 5 | SFS rate high | SFS% above threshold | optimise |
| 6 | Slow/fumbly bigrams | mean interval > threshold on frequent pairs | info |
| 7 | Dead keys | key < 0.5% of keydowns | info |
| 8 | Language-letter guard | flagged-dead key is a language letter (å/ä/ö) → **do not demote** | no-action |
| 9 | Correction rate high | Backspace / total > 8% | optimise |
| 10 | Coding-layer signal | bracket freq in code filetypes ≫ global | optimise |
| 11 | Thumb underuse | thumb load low → offload candidates exist | optimise |
| 12 | i3 dead bindings | config bindings that never fired | info |
| 13 | i3 hot binding on awkward chord | high-count binding on hard-to-reach combo | info |
| 14 | Hand imbalance | \|left − right\| > 15pts | info / no-action |
| 15 | Number-row usage shape | low but bursty → layer not worth it | no-action |
| 16 | Modifier load | modifier share unusually high | info |

Rules 8/14/15 can emit **no-action** verdicts (data-backed "leave it alone"), so the
report doesn't only nag.

**Report output:** terminal histograms by default; `--html` emits a self-contained page
matching `keylog-report-mock.html` (verdict rails colour-coded critical/optimise/
no-action/mixed; keys rendered as keycaps; light/dark aware). Verdicts ranked
most-severe first.

## Deployment

- Project: `keylogger/` in this dotfiles repo, own `go.mod`, single binary output.
- Permissions: daemon needs read on `/dev/input/event*` → add user to the `input`
  group via home-manager/ansible (alongside existing config). No root, no setuid.
- Build/install: home-manager builds the binary and puts `keylog` on PATH (same
  mechanism as other tools here). Run `home-manager switch` after changes.
- Static maps (`sv.json`, `qwerty-fingers.json`) ship in the repo next to the binary.

## Testing

- Metrics: table-driven — synthetic count sets → expected derived numbers.
- Rules: table-driven — metrics fixtures → expected findings (severity + which keys
  named). This is the deterministic-verdict guarantee.
- Capture/aggregator: feed a scripted event stream (incl. repeats, idle gaps, focus
  changes) → assert the resulting counters and that `prev` resets correctly.
- No evdev/root needed in tests — the event stream is an interface, real devices behind
  one implementation, a scripted slice behind another.

## Open questions / future

- Threshold calibration — initial values above are guesses; tune against real sessions
  (and cross-check the ergo-community numbers: SFB target, comfort ceilings).
- Multi-session trends ("since last session") once several sessions exist — the schema
  already supports it (per-session rows).
- Mid-session hotplug, zsh feeder, oxeylyzer export — deferred.
```
