# keylog

Local keyboard-usage profiler. Captures keystrokes during manual **sessions**,
aggregates them **on the fly into counts** (no keystroke sequence ever hits disk),
and renders an actionable report — dead keys, per-finger load, same-finger
bigrams, per-context usage, i3-binding usage — to inform **Glove80 layout**
decisions.

See `DESIGN.md` for the full rationale. Reference for the intended report output:
`../scratchpad/keylog-report-mock.html` (design mock) — the real report matches it.

## Try it now (no hardware, no permissions)

```sh
go build -o keylog .
./keylog seed                       # fabricate a demo session
./keylog report --i3config demo     # terminal report
./keylog report --i3config demo --html /tmp/keylog.html   # HTML report
```

## Commands

| command | what it does |
|---|---|
| `keylog seed` | write a fabricated demo session (for trying the report) |
| `keylog report [--session N] [--html PATH] [--i3config PATH\|demo]` | render a session |
| `keylog start [--note ..] [--duration 1h]` | begin live capture (needs `input` group) |
| `keylog stop` | end the running session |
| `keylog status` | is a session running, and for how long |
| `keylog ctx --filetype=.. --buffer=..` | feed nvim context to the daemon (internal) |

## Live capture setup

Capture reads `/dev/input/event*`, which requires group membership. **This is the
one manual step** — nothing here changes it for you.

1. **Add your user to the `input` group** (home-manager or ansible), then re-login:
   ```nix
   users.users.<you>.extraGroups = [ "input" ];
   ```
2. **Build & install** — see `keylog.nix` for a `buildGoModule` expression to add
   to your home-manager packages, or just `go build -o ~/bin/keylog .`.
3. **neovim feeder** — add to your nvim config so filetype context is captured
   (terminal-vs-nvim is invisible to i3 alone):
   ```lua
   vim.api.nvim_create_autocmd({ "BufEnter", "FileType" }, {
     callback = function(a)
       vim.system({ "keylog", "ctx",
         "--filetype=" .. vim.bo.filetype,
         "--buffer=" .. (a.file or "") })
     end,
   })
   ```
   `keylog ctx` is a silent no-op when no session is running.
4. **i3** — no setup; the daemon subscribes to i3 window-focus events in-process.

Then:
```sh
keylog start          # captures for up to 1h, or until `keylog stop`
# ... work normally ...
keylog report
```

## What's tested vs not

- **Tested** (unit + end-to-end via a scripted event stream): aggregation and
  bigram/idle/focus-reset semantics, sqlite upsert round-trip, all metrics, the
  rule engine, i3-config parsing, and the daemon loop (fake source → flush).
- **Not yet verified on hardware**: real evdev capture and the i3 listener — they
  compile and are wired, but need the `input` group to run. Smoke-test `keylog
  start` after step 1 above.
