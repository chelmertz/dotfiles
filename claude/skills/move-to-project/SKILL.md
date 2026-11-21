---
name: move-to-project
description: Use when the user asks to move, save or file the current conversation into a ~/p project ("move this into a p-project called X", "make this a project"), or types /move-to-project. Creates the project with p-launcher and writes what the conversation established into its state files, so a later session can pick it up cold.
argument-hint: "<name> [description]"
---

# Move this conversation into a ~/p project

The conversation is the only copy of what it established. This skill turns it
into a project whose state files carry that forward. Follow the
`project-state` skill for the layout; this is the order of work.

## 1. Name it

- Namespace: `m/` for MATCHi work, `personal/` for everything else. Ask only
  when the conversation genuinely could be either.
- Name: exactly what the user gave. If it breaks the lowercase convention of
  its siblings, keep it and mention that once.
- `ls ~/p/<ns>/<name>` first. An existing project gets its `HANDOFF.md`
  updated the way the `handoff` skill does it, never recreated.

## 2. Create it

```
p-launcher create <ns>/<name> --no-open
p-launcher describe <ns>/<name> "<description>"
```

Without a description from the user, write one of a few plain words in the
style of `p-launcher list`. Add every PR or issue URL the conversation is
waiting on with `p-launcher link add <ns>/<name> <url>`: the session-start
brief reports only links that moved.

## 3. Write the files

- `CLAUDE.md`: replace the placeholder paragraph with what the initiative is
  for and what it is not, plus the durable constraints the conversation found
  (hardware, accounts, repos, limits). Leave the template's boilerplate below
  it alone.
- `HANDOFF.md`: `Last action:` as one plain sentence; `## Now` with each fact
  dated by when it was established, including commits already pushed elsewhere
  by SHA and whether they are applied; `## Next` as `- [ ] ` items a cold
  session can act on (exact commands where the conversation produced them),
  with `Progress:` counted; `## Unverified` for every estimate or untested
  claim, each with what would settle it.
- `DECISIONS.md`: only when something was chosen. Each entry dated, with what
  was rejected and why.
- `JOURNAL.md` and `GOTCHAS.md`: only when the conversation verified something
  or paid for a trap in this project.

Leave out what belongs elsewhere: machine or toolchain traps go to
`~/.claude/CLAUDE.md`, durable facts about a repo go upstream through it.
Never write a planned state in the present tense.

## 4. Do not commit, do not open

Something else commits `~/p` on its own; a new folder showing `??` is
expected. Do not run `p-launcher open`: the user opens it when they want to
work there (F5, or `p-launcher open <ns>/<name>`).

## 5. Report

The path, then one line per file written saying what it holds, then the top
`## Next` item. Nothing about the conversation itself.
