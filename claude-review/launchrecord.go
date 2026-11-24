package main

import (
	"fmt"
	"time"

	"github.com/chelmertz/dotfiles/claude-review/ids"
	"github.com/chelmertz/dotfiles/claude-review/prlock"
)

// A round is written before its launch, in its own commit, unlisted. Once
// its worker is listed, one step lists it and moves the PR with the
// round's id; a launch that starts nothing takes the row back in the same
// step as its failure. A crash in between leaves an unlisted round, which
// settle resolves before it reads any round (resolveUnlisted). No state
// and no rule exist for it: the rules that move a PR on a launch do.

// launchRecord is what one launch writes around itself, under the PR's
// lock: record before it, listed or failed after it. id is the round
// record wrote, zero until it ran or when it wrote none.
type launchRecord struct {
	// record writes the round (or counts the retry) before the launch, at
	// the time the launch was decided, from where the session transcript's
	// new lines will start.
	record func(at time.Time, from int64) (ids.RoundID, error)
	// listed is the one step after the worker is listed.
	listed func(id ids.RoundID) error
	// failed is the step for a launch that started nothing, why in detail.
	failed func(id ids.RoundID, detail string) error
	id     ids.RoundID
}

// before is the hook a roundStarter runs just before the launch: the
// transcript measured, the round recorded. An error stops the launch.
func (rec *launchRecord) before(at time.Time, size func() int64) func() error {
	return func() error {
		id, err := rec.record(at, size())
		rec.id = id
		return err
	}
}

// owedRecord records tick's round for p: a new round row, listed with the
// owed event.
func (h tickHost) owedRecord(lk prlock.Locked, p Planned) *launchRecord {
	return &launchRecord{
		record: func(at time.Time, from int64) (ids.RoundID, error) {
			return h.ledger.RecordRound(lk, p.PR, p.Head, p.Trigger, at, from)
		},
		listed: func(id ids.RoundID) error {
			e := owedEvent(p)
			e.Round = id
			_, _, err := h.step(lk, p.PR, []ledgerWrite{listRound(id)}, may(e))
			return err
		},
		failed: h.failedToStart(lk, p.PR),
	}
}

// retryRecord records a retry of r: the retry counted on r's row, listed
// with the move to running{retry}, taken back with the move to held.
func (h tickHost) retryRecord(lk prlock.Locked, pr PR, r Round) *launchRecord {
	return &launchRecord{
		record: func(_ time.Time, from int64) (ids.RoundID, error) {
			return r.ID, h.ledger.RecordRetry(lk, pr, r.ID, from)
		},
		listed: func(id ids.RoundID) error {
			_, _, err := h.step(lk, pr, []ledgerWrite{listRound(id)}, may(retryEvent(r)))
			return err
		},
		failed: func(id ids.RoundID, detail string) error {
			e := Event{Kind: evEnded, Next: &PRState{State: stHeld, Kind: kindLaunch, Detail: detail}, Round: r.ID}
			_, _, err := h.step(lk, pr, launchStartedNothing(id, true), may(e))
			return err
		},
	}
}

// clickRecord records a click's round on c: a new round row, listed with
// the launched event (and the click again, when settling the last round
// moved the PR off starting). A click whose head GitHub did not say
// records nothing, so nothing would settle a running round. Listing
// failures are said, not the click's: settle lists the round next pass.
func (h tickHost) clickRecord(lk prlock.Locked, c clickTarget) *launchRecord {
	return &launchRecord{
		record: func(at time.Time, from int64) (ids.RoundID, error) {
			if c.head == "" {
				return ids.RoundID{}, nil
			}
			return h.ledger.RecordRound(lk, c.pr, c.head, Click{At: at.UTC(), From: c.from}, at, from)
		},
		listed: func(id ids.RoundID) error {
			if id.IsZero() {
				return nil
			}
			events, err := h.clickEvents(lk, c.pr, c.from, id)
			if err == nil {
				_, _, err = h.step(lk, c.pr, []ledgerWrite{listRound(id)}, events...)
			}
			h.warn(c.pr, err)
			return nil
		},
		failed: h.failedToStart(lk, c.pr),
	}
}

// clickEvents are the events a click's listed round reports: the click
// again when settling the last round moved the PR off starting, then the
// launch.
func (h tickHost) clickEvents(lk prlock.Locked, pr PR, from string, id ids.RoundID) ([]folded, error) {
	s, err := h.stateOf(lk, pr)
	if err != nil {
		return nil, err
	}
	var events []folded
	if s.State != stStarting {
		events = append(events, may(Event{Kind: evClick, From: from}))
	}
	return append(events, may(Event{Kind: evLaunched, Round: id})), nil
}

// failedToStart is the step for a launch on pr that started nothing: the
// failure and its round's row taken back, in one commit.
func (h tickHost) failedToStart(lk prlock.Locked, pr PR) func(id ids.RoundID, detail string) error {
	return func(id ids.RoundID, detail string) error {
		_, _, err := h.step(lk, pr, launchStartedNothing(id, false), may(Event{Kind: evFailedToStart, Class: detail}))
		return err
	}
}

// retryEvent is the move a listed retry of r reports.
func retryEvent(r Round) Event {
	return Event{Kind: evEnded, Next: &PRState{State: stRunning, Kind: kindRetry, Head: r.Head}, Round: r.ID}
}

// unlisted is what settle finds of a round a crash left unlisted.
type unlisted int

const (
	// unlistedUnknown: the roster could not be read for it; it stays as it
	// is, running, until a pass can tell.
	unlistedUnknown unlisted = iota
	// unlistedLaunched: a worker is listed, or the transcript grew past
	// where the round's lines start. It is listed now and read as any.
	unlistedLaunched
	// unlistedNever: neither. Its row is taken back, and the review it was
	// for is owed again.
	unlistedNever
)

// resolveUnlisted decides whether r, written before a launch whose step
// after never committed, launched, under pr's lock lk, and commits that:
// listed with the move its launch reports (owed, a click's launch, a
// retry's), or its row taken back. A retry's row is the round it retried:
// taking it back leaves that round to be read again.
func (h tickHost) resolveUnlisted(lk prlock.Locked, pr PR, r Round) (unlisted, error) {
	found, err := h.launchedRound(pr, r)
	if err != nil || found == unlistedUnknown {
		return unlistedUnknown, err
	}
	retry := r.Retries > 0
	if found == unlistedNever {
		_, _, err := h.step(lk, pr, launchStartedNothing(r.ID, retry))
		return found, err
	}
	var events []folded
	switch c, isClick := r.Trigger.(Click); {
	case retry:
		events = []folded{may(retryEvent(r))}
	case isClick:
		if events, err = h.clickEvents(lk, pr, c.From, r.ID); err != nil {
			return unlistedUnknown, err
		}
	default:
		events = []folded{may(Event{Kind: evOwed, Head: r.Head, Trigger: r.Trigger, Round: r.ID})}
	}
	_, _, err = h.step(lk, pr, []ledgerWrite{listRound(r.ID)}, events...)
	return found, err
}

// launchedRound reads whether r's launch happened: a worker listed for its
// session, or its transcript longer than when the round was written.
func (h tickHost) launchedRound(pr PR, r Round) (unlisted, error) {
	sid := r.SessionFor(pr)
	if h.sess.Roster == nil {
		return unlistedUnknown, fmt.Errorf("round %s: this host cannot list workers to tell whether it launched", r.ID)
	}
	all, err := h.sess.Roster()
	if err != nil {
		return unlistedUnknown, nil //nolint:nilerr // unknown is an outcome: a later pass tells
	}
	mine, err := h.sess.workersFor(all, sid)
	if err != nil {
		return unlistedUnknown, nil //nolint:nilerr // unknown for this session alone
	}
	if len(mine) > 0 || (h.transcriptSize != nil && h.transcriptSize(sid) > r.TranscriptFrom) {
		return unlistedLaunched, nil
	}
	return unlistedNever, nil
}
