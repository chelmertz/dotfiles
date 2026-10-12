# p-launcher

One hotkey opens any `~/p` project in a Claude Code terminal, shows whose turn
it is per project (you, Claude, a reviewer), and reports weekly on how many
things to run at once.

- `menu` is bound to F5 (rofi): projects sorted by urgency (asks, finished
  waits, reviewer waits, working, idle; oldest wait first), a verb
  submenu per project (open, archive, postpone, rename, add link, context),
  docked `brief…`, `archived…` and `report…` rows.
- Claude Code hooks call `hook` on every event; the ball state and an event
  log land in `~/.local/share/p-launcher/p.db` (SQLite, migrated in place).
- `report` renders a self-contained HTML dashboard from that data; `--demo`
  shows it with placeholder numbers.
- `brief` (weekday mornings, systemd user timer) writes the daily PR brief:
  one read-only headless Claude run over elly's queue plus the launcher's own
  state, stored with its items so the next brief knows what it already
  recommended, rendered to `brief.html` and opened from the `brief…` row.
  `--dry-run` prints the prompt without spending a run, `--show` prints the
  stored brief, `--force` runs a second time the same day.
- `links refresh` and `tend` (on a systemd user timer) track PR links via
  GitHub and elly, and can start a Claude session to address review feedback
  once enabled.
- `wait <pr-url> checks|review|merge <next step>` is what a session runs
  instead of polling (`claude-no-poll-hook` blocks the polling inside `~/p`).
  `tend` resolves waits before its own pass: a fresh session with the next
  step when none is open in the project, a notification otherwise. `wait list`
  shows the open ones.

## Waits

A session that pushed and now needs CI or a reviewer registers a wait and
ends its turn, instead of polling inside its own context. The `tend` timer
picks the thread back up.

```mermaid
sequenceDiagram
    actor You
    participant A as Session A
    participant GH as GitHub / CI
    participant PL as p-launcher (tend timer)
    participant B as Session B (fresh)

    A->>GH: git push
    A--xGH: until gh pr checks ... sleep 30
    Note over A: claude-no-poll-hook blocks the poll
    A->>PL: p-launcher wait PR_URL checks "next step"
    PL->>GH: checks now? running is recorded
    A-->>You: turn ends, handoff
    loop every 10 minutes (systemd timer)
        PL->>GH: links refresh + checks
        GH-->>PL: still running, keep waiting
    end
    GH-->>PL: failed, finished after the wait began
    alt no session open, tend enabled, under the daily cap
        PL->>B: ghostty + claude, outcome and next step as first message
        Note over PL: PR marked tended, so tend's own pass skips it
        B->>GH: gh pr view --json state, stop unless OPEN
        B->>GH: fix, push, reply on threads
        B->>PL: wait again, or handoff
    else window open, PR merged or closed, or 48 h passed
        PL-->>You: desktop notification with outcome and next step
    end
```

The resumed session may fix code, push and reply on review threads. It never
opens PRs, requests review, approves, merges or resolves threads; those go to
`## Open decisions` in the project's HANDOFF.md.

How `tend` resolves each open wait (`waitOutcome` and `runWaits` in
`wait.go`):

```mermaid
flowchart TD
    open(["open wait"]) --> merged{"PR merged?"}
    merged -- yes --> mergecond{"merge wait?"}
    mergecond -- yes --> enabled
    mergecond -- no --> notify
    merged -- no --> closed{"PR closed?"}
    closed -- yes --> notify
    closed -- no --> cond{"condition"}
    cond -- checks --> running{"checks running?"}
    running -- yes --> keep(["keep waiting"])
    running -- no --> finished{"passed or failed,<br/>after the wait began?"}
    finished -- yes --> enabled
    finished -- no --> expired
    cond -- review --> comment{"new comment from<br/>someone else, not a bot?"}
    comment -- yes --> enabled
    comment -- no --> expired
    cond -- merge --> expired{"older than 48 h?"}
    expired -- yes --> notify
    expired -- no --> keep
    enabled{"tend enabled?"} -- no --> notify
    enabled -- yes --> live{"session open<br/>in the project?"}
    live -- yes --> notify
    live -- no --> cap{"daily cap of 4<br/>used up?"}
    cap -- yes --> notify
    cap -- no --> act(["launch session B,<br/>mark the PR tended"])
    notify(["desktop notification"])
```

Every outcome except "keep waiting" closes the wait. The daily cap is shared
with `tend`'s own sessions.

Design notes and the handoff live outside this repo, in
`~/p/personal/p-launcher/`. Tests: `go test ./...`; screenshot goldens with
`P_LAUNCHER_E2E=1` (Xvfb, rofi, ImageMagick, firefox).
