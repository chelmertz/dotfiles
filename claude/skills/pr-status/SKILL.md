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
| `ChecksFailing` | names of the failing checks. `ChecksComplete: false` means the list is not the whole truth: with names present say "at least N", and with the list **empty on a red PR** say "red, check names unavailable" — never "0 failing checks". An empty list there means elly's PAT lacks the Checks/Commit-statuses read permission, not that the PR is fine. |
| `ThreadsActionable` | `> 0` means **we** owe the reply. This is the count that was missed. |
| `ThreadsWaiting` | threads where the other side owes the reply. |
| `LastPrCommenter` | who spoke last. |
| `RereviewFrom` | reviewers who reviewed before the latest push and were not re-requested. |
| `ReviewStatus` | approval state **only**. Never read it alone. |
| `IsDraft`, `Buried` | not anyone's turn; `Buried` is a decision the user already made. |

If the request fails, elly is not running — say so and stop. Do **not** fall
back to assembling the answer from `gh`. An unavailable source means the state
is unknown, and unknown must be reported as unknown.

`p-launcher session-brief` reads the same database, so its PR lines and this
skill can never disagree.

## Decide whose turn, in this order

First match wins. The order is the point: a PR that is red or owes a reply can
never come out as "waiting on someone else".

1. `IsDraft` → nobody's turn.
2. merge conflict → **ours**.
3. `ChecksState` is `FAILURE`/`ERROR` → **ours**, and name the checks.
4. `ThreadsActionable > 0` → **ours**, and say how many and who spoke last.
5. changes requested and not yet answered → **ours**.
6. `ChecksState` is `PENDING` → nobody's turn yet; say what it is waiting for.
7. approved → **ours to merge**.
8. otherwise → theirs to review.

For a PR someone else authored, mirror it: unresolved threads or red checks put
it back with the author; otherwise it is ours to review.

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
