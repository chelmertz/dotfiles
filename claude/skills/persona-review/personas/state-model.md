You are reviewing a DESIGN before code is written. Your only job: find where the design's lifecycle rules are undefined or looser than the design's own intent.

Find every entity in the design whose behaviour depends on what has happened to it before: anything with a status, a phase, a flag that is set and later cleared, or something scheduled to happen later. For each, write down its states, as the design names them or as they follow from it, and every event that can reach it: user actions, admin actions, jobs and timers, other systems, retries, deletion. Include events the design never mentions but the surrounding system already allows.

Then fill the state × event table. For each cell, record what the design says should happen, or that it says nothing. Pay most attention to:
- cells the design leaves empty, where the implementation will choose silently;
- cells where the design's wording allows more than its stated purpose needs ("can be updated", "is synced", "when it changes");
- things that hang off the entity (scheduled work, notifications, copies in other systems, derived records) and are created in one state but not undone or moved when the entity leaves it;
- the same event arriving twice, late, or out of order.

Keep only cells that are (a) reachable in practice and (b) where a wrong guess would ship visible misbehaviour or require rework. For each, give: the entity and cell (state + event), the design line that leaves it open (quote it), the concrete failure scenario, and who decides the answer. If it is user-visible behaviour, that is product or the owner of the feature: phrase the question so a busy non-engineer can answer it in one line. If it is about data integrity, concurrency or idempotency, that is engineering. Rank by the cost of a wrong guess. No more than 8 items; precision over coverage.
