# persona-review: why it looks like this

Not loaded by Claude Code; `SKILL.md` is the skill. This is the backstory.

## Origin

A coworker's workflow: one reviewer persona per specialty (UX, security,
architect, performance/DB), adapted per project, iterating until they agree.
Adopted in part, after checking where rework actually came from.

## What the rework data said

One month-long project, about 49 PRs and about 308 review findings, plus the
handoff checklist's history (156 items added, 115 dropped, 2 reopened):

- The costliest rework came from integration gaps (things that only break
  when deployed end to end) and from product decisions nobody confirmed
  with the people who could answer. No reviewer persona prevents the first;
  a tracer bullet does. The second is what the `assumptions` persona is for.
- About a third of findings (nits, false claims in prose, weak tests) are
  code-time by nature and already caught by self-review and CI bots.
- Security was the only specialty with real design-time yield. DB/concurrency
  was moderate, and only where two writers share data. UX and perf were noise
  on that project (about 6 and 4 design-catchable findings).

## What changed from the original idea

- **No debate to consensus.** Agents arguing until they agree converge out of
  politeness and talk down the lone dissent, which is the finding worth
  having. Personas review blind; one adjudicator verifies and merges.
- **Generic prompts, project context from files.** A per-project copy of a
  persona drifts. The expertise comes from the project's own decisions and
  code, not from the persona's name.
- **Only personas that pass a backtest.** At most three per run, because
  reading cost grows with every persona and precision drops.

## The backtest

For each fixture, the design state was snapshotted from git history at a date
before any code, and a separate agent built the ground truth from what review
later found. Each persona then ran blind against the snapshot.

- **security:** 6/8 of the new design-level findings on the first run, but its
  prompt had borrowed two phrases from the ground truth. With those phrases
  removed, the control run scored 4/8 strictly and 6/8 counting partials. Most
  of the drop was variance between runs, not the leak, so the persona runs
  twice and the union is kept.
- **assumptions:** 1/3 against items fixed before the runs. The hit was the
  single costliest mistake: a whole implementation path dropped because a
  product was being retired. The two misses were requirements never written
  down as decisions, which no prompt over the docs can find. Admitted on cost,
  not hit rate.
- **state-model** (a second fixture, an authentication rollout design): a
  state × event reviewer, added because neither persona above reads a design
  for undefined lifecycle rules. 2/5 lifecycle items over two runs, and 3 hits
  plus 2 partials of 11 overall; the `assumptions` control on the same input
  scored 0 hits. One hit was the costliest defect, a login path that skipped
  the new check entirely. Admitted on cost, not hit rate. Known blind spots:
  it drops cells the design lists as open questions even when the design
  leans to the wrong answer, and it does not model a rollout across
  environments as an entity with states.

The fixtures hold work data and are kept outside this repo.

## Not yet tested

PR mode (reviewing a diff against an earlier persona review) has no backtest.
