---
name: design-review
description: Use when the user asks to review a design, plan or PR "with the lenses/personas", types /design-review, or has just written a *-design.md or *-plan.md and wants it checked before code is written. Runs blind lens reviewers (security, stakeholder assumptions) in parallel, merges and verifies their findings locally, and writes one review file. With a PR URL it reviews the diff and checks it against the project's earlier design review.
argument-hint: "<design file | PR URL> [lens,lens]"
---

# Design review through lenses

Lenses are reviewer prompts in `lenses/`, one file each. They find
different classes of problem; none of them is a general reviewer. Code-level
review stays with `/code-review` and `/security-review`.

## Pick the input and the lenses

- **Design mode** (the default and the validated use): the argument is a
  design or plan file. Without one, take the newest `*-design.md` or
  `*-plan.md` in the project folder and say which.
- **PR mode**: the argument is a PR URL. The input is its diff plus the
  newest `*-design-review.md` in the project folder, if any. Without a
  design review this is an adversarial review whose yield is untested; say so
  in the output.
- Lenses: the argument's list, else the project `CLAUDE.md` line
  `design-lenses: a, b`, else `security, assumptions`. Never more than three:
  reading cost grows with every lens and precision drops.

## Run

1. Spawn every reviewer in **one message** so they run in parallel. The
   security lens runs **twice**, independently: single runs vary a lot and
   the union of two catches what either alone misses. `assumptions` runs
   once: its misses come from requirements nobody wrote down, which no
   rerun can find.
2. Each reviewer gets: its lens file verbatim, the input, read access to the
   project's state files and clones, and the instruction to write findings
   to a scratch file and return them. Reviewers never see each other's
   output and never comment anywhere. In PR mode, add that the input is a
   diff: report what it introduces or breaks at the design level, and which
   design-review decisions it fails to honour.
3. Merge yourself: dedupe across runs and lenses, then **verify every
   finding** against the code or the quoted design line. Drop what does not
   hold; never forward a finding unread.

## Output

Write one file to the project folder:
`<YYYY-MM-DD>-<topic>-design-review.md`. Per finding: the decision it
attaches to, the failure scenario, what to decide instead, and who decides.

- **Stakeholder questions** go in their own section, each phrased for a
  busy non-engineer to answer in one line, with whom to ask. The user sends
  them; they are not engineering's to answer.
- **Engineering decisions** go to the user through `AskUserQuestion`,
  framed as tensions.
- In PR mode, findings about the diff follow the user's rules for review
  comments on that surface (verify, then post, signed); decisions the diff
  breaks are listed against the design-review line they break.

Everything stays local until the user posts it. Lenses do not argue to a
consensus: debate talks down the lone dissent, which is the finding worth
having.

## Adding a lens

Admit a lens only after a blind backtest on a past project: snapshot the
design state at a date before the code, collect the ground truth (what
review or production later found) with a separate agent, run the lens
without access to either, and score. Admit it when it catches about half the
ground truth, or fewer when a hit would have saved the most expensive rework.
Write the lens prompt without the ground truth's vocabulary: a checklist
phrase lifted from the answers inflates the score.
