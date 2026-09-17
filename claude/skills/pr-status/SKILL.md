---
name: pr-status
description: Use before stating anything about pull request state — whose turn it is, what is waiting on someone, whether a PR is ready to merge or ready to send. Also whenever a list of PRs is about to be given to a person. Reads elly, which already polls every PR, instead of inferring state from a gh field that does not carry it.
---

# PR status

Never answer "what is waiting on X" from `gh pr list`. `reviewDecision` is the
field that looks like the answer and is not: it reports whether an approving
review exists, and says nothing about failing checks or about who owes the next
reply. A PR sits at `REVIEW_REQUIRED` while it is bright red and while eight
review threads wait on us.

That is not hypothetical. On 2026-09-16 four PRs were sent to a colleague as
"waiting on you". Two had 11 and 10 failing checks; the other two had 4 and 8
threads whose last comment was ours. All four read `REVIEW_REQUIRED`. The list
looked right and was wrong in every row.

`claude-pr-status-hook` blocks the command that produced it.

## Read elly

elly polls every PR involving the user every 5 minutes and stores what actually
decides whose turn it is.

```
curl -s localhost:9876/api/v0/prs
```

Per PR, the fields that matter:

| Field | Means |
|---|---|
| `ChecksState` | `FAILURE`/`ERROR` = red. `PENDING` = still running. `SUCCESS` = green. `""` = no checks. |
| `ChecksFailing` | names of the failing checks. `ChecksComplete: false` means the list is not the whole truth: with names present say "at least N", and with the list **empty on a red PR** say "red, check names unavailable" — never "0 failing checks". An empty list there is Github refusing to name the jobs, not the PR being fine. |
| `ThreadsActionable` | `> 0` means **we** owe the reply, on anyone's PR. elly computes it against our username, so it does not flip with authorship: on someone else's PR it counts the threads we started and have neither answered nor reacted to. This is the count that was missed. |
| `ThreadsWaiting` | threads where the other side owes the reply. |
| `LastPrCommenter` | who spoke last, and `""` when that was a bot — elly ignores `github-actions` and `vercel`. Empty is "no human has spoken", not "unknown". |
| `ReviewRequestedFromUsers` | who is being asked, `@`-prefixed for a team. Empty means nobody was requested, which on our own PR is the thing to say. |
| `RereviewFrom` | reviewers who reviewed before the latest push and were not re-requested. |
| `ReviewStatus` | approval state **only**. Never read it alone. |
| `IsDraft`, `Buried` | not anyone's turn; `Buried` is a decision the user already made. |

## Ask elly what it cannot do

```
curl -s localhost:9876/api/v0/config/status
```

A non-empty `degradations` array is elly naming its own blind spot, with the
remedy. Repeat it rather than working around it, and never present a deficiency
as a clean result.

`checks_unreadable` is the standing one on this machine, and it is not a
misconfiguration to go and fix. Github issues the *Checks* permission to Github
Apps only — it is absent from the fine-grained token UI altogether, so no token
of that class can be granted it. Red and green stay correct, because that
verdict is an aggregate Github computes itself; what is missing is the name of
the failing Github Actions job. External reporters such as Buildkite are still
named, through *Commit statuses*. A classic token with the `repo` scope would
get the names, and was rejected on 2026-09-17: read-and-write on every
reachable repository is not worth a job name. So say "red, check names
unavailable" and move on — do not suggest a permission change.

If the request fails, elly is not running — say so and stop. Do **not** fall
back to assembling the answer from `gh`. An unavailable source means the state
is unknown, and unknown must be reported as unknown.

`review_requests_unreadable` means the second of elly's two searches failed on
the last poll. Everything listed is still right; what may be missing is a PR we
were asked to review and have never commented on. It usually clears by itself.

`p-launcher session-brief` does **not** read elly live. It reads its own `link`
table, filled at the last `p-launcher links refresh`, so the two disagree until
a refresh runs — on 2026-09-17 the brief said "13 unresolved threads, waiting on
you" on a PR elly already had at zero, minutes after the replies went in. When
they differ, elly is current and the brief is stale; run `p-launcher links
refresh` and re-read rather than reporting either one over the other.

The brief is not redundant, though: it covers PRs elly cannot see. Its rows are
whatever the project linked, looked up through `gh`, so a PR the user is neither
involved in nor a named reviewer on — https://github.com/matchiapp/matchi-frontend/pull/1988
was one — appears there and in no elly search.

## What elly still cannot see

elly runs two searches: `involves:` — author OR assignee OR mentions OR
commenter — and `review-requested:`. Between them they cover a PR we touched
and a PR we were asked for by name.

A review requested from a **team** we belong to matches neither, because
`review-requested:` is direct requests only and elly cannot resolve team
membership. Such a PR is invisible until someone mentions us on it. If the
question is "is there anything I have not been shown", that is the gap to name;
do not answer it from `gh pr list`.

## Decide whose turn, in this order

First match wins. The order is the point: a PR that is red or owes a reply can
never come out as "waiting on someone else".

1. `IsDraft` or `Buried` → nobody's turn.
2. merge conflict → **ours**.
3. `ChecksState` is `FAILURE`/`ERROR` → **ours** on our own PR, the author's on
   theirs. Name the checks.
4. `ThreadsActionable > 0` → **ours**, on anyone's PR, and say how many and who
   spoke last. Do not flip this one to the author because they authored it.
5. changes requested and not yet answered → **ours**.
6. `ChecksState` is `PENDING` → nobody's turn yet; say what it is waiting for.
7. approved, our PR → **ours to merge**.
8. our PR, none of the above → theirs to review; name who from
   `ReviewRequestedFromUsers`, and say plainly when that is empty, because a PR
   nobody was asked to review is waiting on us to ask.
9. someone else's PR, none of the above → **ours to review only if we were
   actually asked**: our login in `ReviewRequestedFromUsers` or `RereviewFrom`.

Anything left over is a PR we are merely involved in, and it is nobody's turn.
Do not put it in an "on you" list. This matters because it is most of them: the
same run that produced nine real rows produced seven of these, every one of
which would have read as a review we owed.

## Before sending a list to a person

Every row needs a reason next to it, and the reason has to come from the fields
above rather than from a verdict field. Quote the URL in full — never `#123`,
never `owner/repo#123`.

Two failure modes to check for by name, because both have happened:

- **A red PR in a "please review" list.** Asking someone to read a PR with 11
  failing checks wastes their time and is the fastest way to lose the next
  review.
- **A PR listed as theirs when the last word is ours.** `ThreadsActionable`
  settles it; nothing else does.
- **A PR we only commented on, listed as a review we owe.** Step 9 settles it:
  no login in `ReviewRequestedFromUsers` or `RereviewFrom` means nobody asked.

If elly's `LastFetched` is more than ~15 minutes old, say the data's age rather
than presenting it as current, or trigger a refresh:

```
curl -s -X POST localhost:9876/api/v0/prs/refresh
```

## When one PR needs more than elly holds

elly deliberately does not store everything. For the body of a specific review
thread, a diff, or a workflow log, go to `gh` for **that PR** — with the checks
in the same call so the review field is never read alone:

```
gh pr view <url> --json reviewDecision,statusCheckRollup,mergeable
```

That is a lookup on a PR already identified. It is not a way to build the list.
