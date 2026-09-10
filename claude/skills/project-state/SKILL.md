---
name: project-state
description: Use when starting or resuming work in a ~/p project, writing or updating a HANDOFF.md, deciding which file a fact belongs in, or ending a session that another session will pick up. Defines the state-file layout every ~/p project shares.
---

# Project state in ~/p

Each `~/p/<namespace>/<name>` is one initiative. A session there is short-lived
and replaceable; the files below are what survives it. Six projects
independently converged on this layout before it was written down - this skill
names it so tools can find it and sessions stop re-deriving it.

## The files, by job

Fixed names. `p-launcher` and every future check reads them by name, so a
handoff called `HANDOFF-<topic>.md` is invisible to tooling.

| File | Job | Grows? |
|---|---|---|
| `CLAUDE.md` | Why the initiative exists, its constraints, where things are. | No - should barely change |
| `HANDOFF.md` | What is true now, what is next, what is unverified, what needs a decision. | No - ceiling ~120 lines |
| `DECISIONS.md` | What was chosen, why, what was rejected. Append only. | Yes, by design |
| `JOURNAL.md` | What was verified when. Append only. | Yes, by design |
| `GOTCHAS.md` | Traps already paid for *in this project*. | Slowly |
| `<date>-<topic>-design.md` / `-plan.md` | The thinking at that time. Never edited afterwards. | New file per topic |

Create `DECISIONS.md`, `JOURNAL.md` and `GOTCHAS.md` when there is a first line
to put in them, not upfront.

## HANDOFF.md shape

```markdown
# <project> handoff

Updated <date>.

## Now (re-check before trusting)

<what is true, each line dated or datable>

## Next

Progress: <done>/<total>.

- [ ] one item per line, in the shape a fresh session can pick up
- [x] finished items stay until the count is read, then move to JOURNAL.md

## Open decisions

<where the last session stopped because it needed a human, and what the
options were. Empty is a good sign, not a missing section.>

## Unverified

<claims not yet demonstrated, and what would demonstrate them>
```

`## Clone map` is worth adding in a project with more than one clone.

Two conventions that carry their weight:

- **"re-check before trusting" is not politeness.** State goes stale between
  sessions - a PR merges, a check turns red. Say when it was true.
- **`## Open decisions` is the escalation slot.** An autonomous or loop-run
  session that hits a genuine fork stops and writes here rather than guessing.
  This is the section a human reads first.

## Which file does this fact go in

- Is it *what is true now, or what to do next*? `HANDOFF.md`.
- Is it *what we chose and why*? `DECISIONS.md`, even if it feels obvious today.
- Is it *what was verified, and when*? `JOURNAL.md`.
- Is it *a trap specific to this project*? `GOTCHAS.md`.
- Is it *a trap in this machine or toolchain* - a compiler flag, a shell alias,
  a CLI that expires? `~/.claude/CLAUDE.md`, not here. Those repeat across
  every project, and a note buried in one project's handoff is a note the next
  project pays for again.
- Is it a *durable fact about the system being built*? Upstream, in the repo
  that owns it, through a PR - a spec, an ADR, a task list. The `~/p` folder is
  a workspace, not a source of truth. This is the rule that keeps a handoff
  from becoming the documentation.

## Handing off

- Hand off at a **work-item boundary**, not when the context bar says so. The
  statusline shows tokens remaining precisely so the judgement stays yours:
  188k left with a 40k item in hand means carry on.
- Past ~90% context, wrap at the next green test. Past ~97%, hand off now.
- Handing off means: update `## Now` with today's date, tick or add `## Next`
  items, record anything that needed a human in `## Open decisions`, move
  what is no longer actionable into `JOURNAL.md` or `DECISIONS.md`, then
  `/clear`. Do not grow `HANDOFF.md` past its ceiling; move something out.
- Never write a planned state in the present tense. If the thing that makes it
  true has not merged, name what will make it true.

## Resuming

Read `CLAUDE.md` and `HANDOFF.md`. Read nothing else until an item needs it -
the design docs and the journal are there to be consulted, not loaded.
