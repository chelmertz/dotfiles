---
name: spec-ideation
description: Take a half-formed idea to an agreed, requirement-shaped Confluence spec page, ready for spec-transfer. Use when exploring or arguing a design that will end up in OpenSpec, when drafting or revising requirements on a Specs page, when reconciling a draft against what the repo already specifies, or when asked to write thinking up in Confluence. Covers the Ideas/Specs boundary, requirement shape, the reconcile step and the agreement gate. Stops at Agreed — spec-transfer moves it to git.
---

# Ideating in Confluence, up to an agreed spec

A fast, messy cycle for deciding what to build; a slow, deterministic one for building it.
This skill is the fast half. It ends at **Agreed**; `spec-transfer` moves it to git, and
after that git wins.

Full process: *Ways of working — Confluence to OpenSpec*.

## The boundary that does all the work

`Ideas` and `Specs` are separate pages, and the separation is physical rather than a
heading. An agent asked to transfer reads from `Specs` and nowhere else — the page
boundary is the guard, so it only holds if we respect it.

Two rules follow, and both are easy to break by being helpful:

- **Never write requirement IDs or Given/When/Then into `Ideas`.** A page that looks like
  a spec invites someone to treat it as one.
- **Nothing is promoted in place.** When an idea earns a spec it is *rewritten*,
  requirement-shaped, on a new page under `Specs`. The idea stays where it is.

## Ideating

Free prose. No structure, no IDs, no scenarios. Argue, speculate, paste links, be wrong.

- Take a position, and record what you rejected. The parked list is the point — it stops
  the same idea being rediscovered in three months.
- Name the failure an idea prevents, not the feature it adds.
- Stay here until the shape stops changing. Drafting early spends the ID budget on
  something that will be rewritten anyway.

Do not create a `Specs` page because the conversation felt productive. It earns one when
you can state what an observer would see if it worked.

## Drafting

A new page under `Specs`, from the Specification template (source: *Spec template
source*). Not a copy of the Ideas page.

**Requirements, not prose.** Numbered, each with at least one scenario:

    ### R-1 — One line saying what must be true

    The service SHALL do the thing. It SHALL NOT do the other thing.

    Why it exists — the constraint, or the failure it prevents.

    #### Scenario: The ordinary case
    - GIVEN some starting state
    - WHEN something happens
    - THEN an observable outcome SHALL follow

RFC-2119 throughout: SHALL, SHALL NOT, MAY.

Three ways a draft goes wrong, all seen for real:

- **Mechanism written as a requirement.** *"No configuration read SHALL be issued"* is
  invisible to a client with curl, and the repo's own spec rules reject it. Only what an
  observer can falsify, from a named vantage point, is a requirement. Mechanism belongs in
  `design.md`, in the repo.
- **An invented capability name.** The capability vocabulary belongs to the repo: a
  capability is valid if `openspec/specs/<name>/` exists, or the proposal explicitly
  introduces it. Run 1 declared `model-gateway` where the repo uses `chat-completions`,
  which would have built a second capability tree for one surface. Never validate against a
  list written in Confluence.
- **Reusing an ID.** IDs never move — append only, deprecate or supersede in place. Every
  back-reference in git points at an ID, so deleting R-2 makes R-3 look like the second
  requirement to anyone reading later.

Read `openspec/config.yaml` before drafting. It carries rules the Confluence page has
never seen.

## Reconcile — before agreeing, not before transferring

**The first real run transferred three requirements already specified in the repo, one of
them better than our version.** Nothing had looked at the destination.

Read everything under `openspec/specs/` and `openspec/changes/` — including `archive/` —
and compare by **behaviour**, not by ID. A requirement written directly in the repo has no
manifest and makes no claims, so no verifier can see it. This check is human.

Do it before the agreement gate. By transfer time the argument has already happened, and
sunk cost makes people transfer anyway.

| Finding | What happens |
|---|---|
| Fully covered | Mark it SUPERSEDED on the page and keep it as a stub. Never delete |
| Partially covered | Narrow it in Confluence and re-agree. Do not transfer a superset, do not edit it in flight |
| Contradicts the repo | Raise it. The page is intent, the spec is contract |

## Agreeing

Both sign-offs go to `DECIDED` in the page's decision list, and the IDs freeze.

**Check the gate against HTML, not markdown.** The markdown rendering silently drops the
decision list, so the agreement gate is invisible in it. Use markdown to read requirement
text if you like; check gates only against HTML.

Say what is about to become visible. A spec page is private to two people; an OpenSpec
change is visible to everyone with repo access.

## Handoff

Stop here. Hand over the page ID and the requirement IDs to `spec-transfer`, which runs its
own preflight — it will not trust that this skill ran.

Deltas stay small, so hand over a subset rather than the whole page.

## Reading Confluence without being misled

- **Search titles before bodies.** Registers and design pages are titled well; body search
  is noisy. Then look for the page you are about to create — in a mature space there are
  usually two with the same title.
- **Check you actually got the body.** The Atlassian MCP has more than one page-read tool,
  and at least one returns only a summary and an excerpt. No `body` field means you have
  not read the page.
- **Confluence is evidence, not truth.** Every page carries a last-modified date; a
  register untouched for a year is a lead, not a fact. Anything load-bearing gets checked
  against code, schema or `git log` — and when they disagree the code wins, and the
  disagreement is usually worth writing down.
- **Panels are apparatus**, not content: editorial notes, warnings, revision history. They
  are excluded from transferred source, so nothing load-bearing goes in one.
