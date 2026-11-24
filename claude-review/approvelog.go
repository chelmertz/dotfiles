package main

import (
	"encoding/json"
	"errors"
	"time"
)

// factsKindApprove is FactsApprove's kind, the approve rows' facts_kind.
const factsKindApprove = "approve"

// An approval's outcome, as GitHub answered it.
const (
	outcomeApproved = "approved"
	outcomeFailed   = "failed" // GitHub refused the approval, or the PR could not be read
	outcomeStale    = "stale"  // the head moved since the review
	outcomeClosed   = "closed"
)

// LogApproval logs one approval as a recorded row: it moves no state, so
// it replays through no rule, and from and to are both the state the
// approval was taken in. It logs one approval of pr at head, from origin (originLink: an
// Approve link whose token was spent; originVerb: a typed `approve --at`),
// with its outcome, at at. The caller holds pr's lock. Neither the token
// nor its hash is written: every column is a fixed word, the head, or the
// PR's state.
func (l *Ledger) LogApproval(pr PR, head, origin, outcome string, at time.Time) error {
	tx, err := l.db.Begin()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	s, ok, err := prStateIn(tx, pr)
	if err != nil {
		return err
	}
	if !ok {
		s = PRState{State: stIdle}
	}
	f := FactsApprove{Head: head, Origin: origin, Outcome: outcome}
	// The event column names the approval by its facts kind. It is no
	// transition event: tick learns of the approval from GitHub
	// (evUserApproved).
	state, errState := json.Marshal(logState(s))
	event, errEvent := json.Marshal(loggedEvent{Kind: EventKind(f.factsKind()), Head: head})
	facts, errFacts := json.Marshal(f)
	if err := errors.Join(errState, errEvent, errFacts); err != nil {
		return err
	}
	_, err = tx.Exec(`insert into decisions (pr, at_ms, origin, build, step, event, from_state, to_state, effects, facts_kind, facts)
		values (?, ?, ?, ?, ?, ?, ?, ?, '[]', ?, ?)`,
		pr.URL(), at.Truncate(time.Millisecond).UnixMilli(), origin, version, stepRecorded,
		string(event), string(state), string(state), f.factsKind(), string(facts))
	if err != nil {
		return err
	}
	return tx.Commit()
}

// logApproval logs an approval in the ledger under d's state directory.
func logApproval(d Deps, pr PR, head, origin, outcome string) error {
	now := time.Now
	if d.Now != nil {
		now = d.Now
	}
	l, err := OpenLedger(LedgerPath(d.StateDir))
	if err != nil {
		return err
	}
	return errors.Join(l.LogApproval(pr, head, origin, outcome, now()), l.Close())
}
