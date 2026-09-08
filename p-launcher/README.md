# p-launcher

One hotkey opens any `~/p` project in a Claude Code terminal, shows whose turn
it is per project (you, Claude, a reviewer), and reports weekly on how many
things to run at once.

- `menu` is bound to F5 (rofi): projects sorted by urgency (asks, finished
  waits, reviewer waits, working, idle; oldest wait first), a verb
  submenu per project (open, archive, postpone, rename, add link, context),
  docked `archived…` and `report…` rows.
- Claude Code hooks call `hook` on every event; the ball state and an event
  log land in `~/.local/share/p-launcher/p.db` (SQLite, migrated in place).
- `report` renders a self-contained HTML dashboard from that data; `--demo`
  shows it with placeholder numbers.
- `links refresh` and `tend` (on a systemd user timer) track PR links via
  GitHub and elly, and can start a Claude session to address review feedback
  once enabled.

Design notes and the handoff live outside this repo, in
`~/p/personal/p-launcher/`. Tests: `go test ./...`; screenshot goldens with
`P_LAUNCHER_E2E=1` (Xvfb, rofi, ImageMagick, firefox).
