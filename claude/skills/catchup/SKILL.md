---
name: catchup
description: Use when picking work back up in a ~/p project after time away, when the session-start brief says something changed since the handoff, or whenever the user types /catchup. Reads what actually happened, folds it into HANDOFF.md, and says what to do next. Counterpart of the `handoff` skill, which writes the state this one re-checks.
argument-hint: "[what to check first, optional]"
---

# Catch up

`/handoff` writes what was true when you stopped. This reads what became true
while you were away and makes the file agree with it. The session brief
(`p-launcher session-brief`, printed by a SessionStart hook) already listed the
GitHub facts for free; this command re-checks them and supplies the judgement.
An empty list is a reason to be quick, not a reason not to run.

## Decide first, in one line

- **The brief listed changes** → reconcile them. That is the job.
- **The brief listed nothing** → run the checks below anyway, then say so and
  restate the next step. An empty list is a claim about GitHub, and only the
  checks make it true: the data behind it is up to ten minutes old, and its
  window is only as honest as the last `links seen`. Do not go looking for work
  beyond the checks.
- **No brief in context** (a resumed or compacted session) → run
  `p-launcher session-brief` and start from its output.
- **No HANDOFF.md** → say so and offer to write one from what is in the repo.
  Do not invent state. The `project-state` skill has the layout.

Say which of the four this is before doing anything else.

## Check only these, in this order

The list is bounded on purpose. This runs several times a day per project, and
re-reading the whole project costs more than the drift it finds.

1. **Every line the brief printed.** Each is a fact `HANDOFF.md` has not caught
   up with yet.
2. **Every PR or issue URL in `## Now` and `## Next` the brief did not
   mention.** One `curl -s localhost:9876/api/v0/prs` covers all of them at
   once — elly polls every PR involving you every 5 minutes, and the `pr-status`
   skill has the fields and the ordering that decide whose turn each one is.
   Never reach for `gh pr view --json reviewDecision` to answer that; the field
   reports only whether an approving review exists, and `claude-pr-status-hook`
   blocks the call.

   A URL the response does not contain is a fact, not a gap: elly stores open
   PRs only (`state:open` in its search, and every fetch replaces the table), so
   absence means merged or closed. That is the one thing to ask `gh` —
   `gh pr view <url> --json state,mergedAt` — and it is a lookup on an already
   identified PR, not a way to build the list. One `gh` call per tool
   invocation: two in one compound command hits the permission classifier.

   If elly does not answer, the state is unknown and gets written down as
   unknown. Do not reassemble it from `gh`.
3. **Each clone or worktree named in `## Now`** — fetch, then ahead/behind
   against its upstream and whether the tree is dirty. Name which worktree; they
   are one per task and another session may hold one.
4. **Every line under `## Unverified`.** This is the section the command exists
   for. If a claim can be demonstrated now, demonstrate it rather than carrying
   it forward another session.

Stop there. The design docs and the journal are there to be consulted when an
item needs them, not loaded because you are here.

Then run `p-launcher links seen <ns/name>`. That is what tells the next brief
these links were actually looked at, and it is the only thing that narrows its
"changed since" window. Skipping it costs a wider window, never a wrong one.
`/handoff` deliberately does not do this: writing a file verifies nothing.

## Then write

Follow the `project-state` layout.

1. `## Now` — rewrite what changed and re-date it. Delete what stopped being
   true; do not append a correction under it.
2. `## Next` — tick what the world finished, add what the changes created, fix
   the `Progress: x/y` count.
3. `## Open decisions` — a change that needs the user goes here, written for a
   reader with no context. A merged PR that invalidates the plan is a decision,
   not a tick.
4. `## Unverified` — move demonstrated claims into `JOURNAL.md`, dated. Leave
   the rest, and say what would demonstrate each.
5. `Last action:` in the header — one plain sentence. The session brief prints
   this line cold, so it has to make sense without the rest of the file.

Never write a planned state in the present tense: if the thing that makes it
true has not merged, name what will.

## Then print exactly this, and nothing longer

```
Caught up:   <what moved while you were away, one line, or "nothing had moved">
Progress:    <done>/<total> items
Next step:   <the top unchecked item, in the shape it will be picked up>
Decisions:   <count needing the user, or "none">
```

## Finally

Say what you propose to do first and **stop there**. This command exists so the
first move of a session is informed, not so it happens without being asked. The
user picks.
