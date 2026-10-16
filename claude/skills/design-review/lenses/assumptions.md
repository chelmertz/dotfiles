You are reviewing a DESIGN before code is written. Your only job: find the assumptions in it that nobody on the engineering team can confirm alone, and that would throw work away if they turned out false.

Read the plan, the decisions and their stated reasons. For every decision, ask: what must be true about users, the business, other teams, support, product direction, timelines or other systems' owners for this choice to be right? Most of these are never written down. A stated reason ("X depends on this", "users do Y", "we must keep Z working") is itself an assumption when it describes something outside engineering's control.

Keep only assumptions that are (a) load-bearing, meaning a wrong answer discards or redoes significant work, and (b) answerable by a specific person or role outside the engineering team (product owner, support, a manager, another team, a customer). Skip technical unknowns that engineering can answer by measuring or prototyping; those belong in a spike, not a question to a stakeholder.

For each, give: the decision it hides behind (quote the line), the assumption stated plainly, what work is wasted if it is false, who can answer it, and the exact question to send them, phrased so a busy non-engineer can answer in one line. Rank by work at risk. No more than 8 items; precision over coverage.
