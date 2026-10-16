---
name: persona-review
description: Use when the user asks to review a design, plan or PR "with the personas", types /persona-review, or has just written a *-design.md or *-plan.md and wants it checked before code is written. Runs blind persona reviewers (security, stakeholder assumptions) in parallel, merges and verifies their findings locally, and writes one review file. With a PR URL it reviews the diff and checks it against the project's earlier persona review.
argument-hint: "<design file | PR URL> [persona,persona]"
---

# Persona review

Personas are reviewer prompts in `personas/`, one file each. They find
different classes of problem; none of them is a general reviewer. Code-level
review stays with `/code-review` and `/security-review`.

## Pick the input and the personas

- **Design mode** (the default and the validated use): the argument is a
  design or plan file. Without one, take the newest `*-design.md` or
  `*-plan.md` in the project folder and say which.
- **PR mode**: the argument is a PR URL. The input is its diff plus the
  newest `*-persona-review.md` in the project folder, if any. Without a
  design review this is an adversarial review whose yield is untested; say so
  in the output.
- **A clone is required in PR mode.** The diff alone hides the code around
  it, so no finding about it can be verified. Read the PR head from a clone
  of its repository (`git fetch <remote> pull/<n>/head`, then
  `git show FETCH_HEAD:<path>`); without one, stop and say so rather than
  review the diff alone.
- **Outside a `~/p` project** (a review session started in a bare clone):
  write no file. Findings go back to the caller, whose instructions decide
  where they are posted, and the caller's working rules (a read-only
  checkout, how to read the PR head) go to every reviewer verbatim.
- Personas: the argument's list, else the project `CLAUDE.md` line
  `personas: a, b`, else `security, assumptions`. Never more than three:
  reading cost grows with every persona and precision drops.

## Run

1. Spawn every reviewer in **one message** so they run in parallel. The
   security persona runs **twice**, independently: single runs vary a lot and
   the union of two catches what either alone misses. `assumptions` runs
   once: its misses come from requirements nobody wrote down, which no
   rerun can find.
2. Each reviewer gets: its persona file verbatim, the input, read access to the
   project's state files and clones, and the instruction to write findings
   to a scratch file and return them. Reviewers never see each other's
   output and never comment anywhere. In PR mode, add that the input is a
   diff: report what it introduces or breaks at the design level, and which
   persona-review decisions it fails to honour.
3. Merge yourself: dedupe across runs and personas, then **verify every
   finding** against the code or the quoted design line. Drop what does not
   hold; never forward a finding unread.

## Output

Inside a `~/p` project, write one file to the project folder:
`<YYYY-MM-DD>-<topic>-persona-review.md`. Per finding: the decision it
attaches to, the failure scenario, what to decide instead, and who decides.

- **Stakeholder questions** go in their own section, each phrased for a
  busy non-engineer to answer in one line, with whom to ask. The user sends
  them; they are not engineering's to answer.
- **Engineering decisions** go to the user through `AskUserQuestion`,
  framed as tensions.
- In PR mode, findings about the diff follow the user's rules for review
  comments on that surface (verify, then post, signed); decisions the diff
  breaks are listed against the persona-review line they break.

Posted comments never name this skill or a persona: they are personal
tooling, and a finding stands on its own. Personas do not argue to a
consensus: debate talks down the lone dissent, which is the finding worth
having.

## Adding a persona

Admit a persona only after a blind backtest on a past project: snapshot the
design state at a date before the code, collect the ground truth (what
review or production later found) with a separate agent, run the persona
without access to either, and score. Admit it when it catches about half the
ground truth, or fewer when a hit would have saved the most expensive rework.
Write the persona prompt without the ground truth's vocabulary: a checklist
phrase lifted from the answers inflates the score.
