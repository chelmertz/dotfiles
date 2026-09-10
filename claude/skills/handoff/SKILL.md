---
name: handoff
description: Use when the statusline shows /handoff, when a work item is finished and the context is filling, or whenever the user types /handoff. Updates the project's HANDOFF.md, then says whether to /clear now or carry on.
argument-hint: "[what was just finished, optional]"
---

# Hand off

## Decide first, in one line

- **The work item is finished** → hand off, then clear.
- **The item is mid-flight** → write the handoff anyway, as cheap insurance
  against a crash, and tell the user to carry on. Do not clear.
- **The statusline nagged and the item is not finished** → the room left is the
  deciding fact and only the user can see it, so say what a stopping point
  would cost here and let them choose. Do not stall the work on your own
  estimate of remaining tokens.

Say which of the three this is before doing anything else. The point of this
command is that the user does not have to make that call in the common cases.

## Then update the state files

Follow the `project-state` skill's layout. In `HANDOFF.md`:

1. `## Now` — rewrite what changed, and date it. Delete what is no longer true
   rather than appending a correction.
2. `## Next` — tick what got done, add what surfaced. One item per line, and
   fix the `Progress: x/y` count.
3. `## Open decisions` — anything that needs the user: a fork you could not
   settle, a design that turned out subpar, an error needing their judgement.
   This is the section they read first, so write it for a reader with no
   context.
4. `## Unverified` — anything claimed but not demonstrated, and what would
   demonstrate it.
5. Move what stopped being actionable into `JOURNAL.md` (verified, dated) or
   `DECISIONS.md` (chosen, why, what was rejected). Keep `HANDOFF.md` under its
   ceiling by moving things out, never by trimming wording.

Machine or toolchain traps go in `~/.claude/CLAUDE.md`, not the project file.
Durable facts about the system being built go upstream through a PR.

## Then print exactly this, and nothing longer

```
Last action: <one plain sentence, no jargon>
Progress:    <done>/<total> items
Next step:   <the top unchecked item, in the shape it will be picked up>
Decisions:   <count needing the user, or "none">
```

Those four lines are what the next session's start briefing will show, so they
have to make sense cold.

## Finally

If the decision was to clear, end by telling the user to press `/clear` now.
A command cannot invoke a built-in, so that keystroke stays theirs. If the
decision was to carry on, say so and stop - do not suggest clearing.
