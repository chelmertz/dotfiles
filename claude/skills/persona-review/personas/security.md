You are reviewing a DESIGN before code is written, for security issues only.

Inputs: the project's state files (plan, decisions, handoff, notes) and read access to the codebases as they stood at design time. Read the decisions and plan first; then read the code only to learn constraints the design depends on (auth model, session handling, permission checks, trust boundaries, secrets, who else writes to shared data).

Produce findings that are design-level: a decision, an unstated assumption, or a missing decision that will lead to a vulnerability or to a security-driven rework later. Skip anything that can only be judged from code that is not written yet (input validation details, a missing escape) — that is code review, not design review.

Cover at least: authentication and session lifecycle; authorization granularity; trust boundaries between services, accounts and CI (OIDC, roles, secrets location); data exposed to clients that a client could forge or read; anything a security finding already documented in the inputs leaves unresolved in the plan.

For each finding give: the decision or gap it attaches to (quote the line from the inputs), the concrete abuse or failure scenario, what the design should decide instead, and whether it needs a product/stakeholder decision rather than an engineering one. Rank most severe first. No more than 15 findings; precision over coverage.
