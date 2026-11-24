package main

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/chelmertz/dotfiles/claude-review/ids"
	"github.com/chelmertz/dotfiles/claude-review/prlock"
)

// A step is planned once under the PR's lock, its events folded over
// transition, and committed in one transaction (BEGIN IMMEDIATE, from the
// DSN): the stored state reread and checked against the plan's from, the
// effects that write the ledger, the token the new state needs, the
// decision rows and the new state or its deletion. A crash anywhere in it
// leaves the step undone, never half done. Workers are stopped before the
// commit and rows sent after it (tickHost.commit).

// querier is what reads and writes the ledger: the ledger's own handle, or
// a step's transaction.
type querier interface {
	Exec(query string, args ...any) (sql.Result, error)
	QueryRow(query string, args ...any) *sql.Row
}

// origin is what decided a step: the tick or a link clicked in the
// browser. The log also takes "verb", a verb typed in a terminal, which no
// step commits yet.
const (
	originTick = "tick"
	originLink = "link"
)

// Step kinds, the decisions table's step column. Only decided is written
// yet; recorded (a launch's round row), failsafe (a dismissal) and global
// (the breaker) arrive with the callers that commit them.
const stepDecided = "decided"

// decision is one event that found a rule, logged as one decided row.
type decision struct {
	e        Event
	from, to PRState
	effects  []Effect
}

// plan is one step: the state it was planned from, the state it leads to,
// the effects in order, and the decision of each event.
type plan struct {
	at        time.Time
	origin    string
	from, to  PRState
	effects   []Effect
	decisions []decision
}

// planStep folds events over transition from s, at at cut to milliseconds
// so a logged decision replays exactly. Every event must find a rule: one
// that finds none plans nothing, and ok is false.
func planStep(s PRState, at time.Time, events ...Event) (p plan, ok bool) {
	p = plan{at: at.Truncate(time.Millisecond), from: s, to: s}
	for _, e := range events {
		next, effects, ok := transition(p.to, e, p.at)
		if !ok {
			return plan{}, false
		}
		p.decisions = append(p.decisions, decision{e: e, from: p.to, to: next, effects: effects})
		for _, ef := range effects {
			if !slices.Contains(p.effects, ef) {
				p.effects = append(p.effects, ef)
			}
		}
		p.to = next
	}
	return p, len(p.decisions) > 0
}

// eventNames names p's events for the journal.
func (p plan) eventNames() string {
	names := make([]string, len(p.decisions))
	for i, d := range p.decisions {
		names[i] = string(d.e.Kind)
	}
	return strings.Join(names, ", ")
}

// Commit writes p for pr in one transaction, under pr's lock lk. The state
// stored must be p's from: under the lock only a bug changes it between
// the plan and the commit, so a mismatch is an error and nothing is
// written. The state is written last, after the effects, the mint and the
// decision rows.
func (l *Ledger) Commit(lk prlock.Locked, pr PR, p plan) error {
	if err := lk.Check(prKey(pr)); err != nil {
		return err
	}
	tx, err := l.db.Begin()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	stored, ok, err := prStateIn(tx, pr)
	if err != nil {
		return err
	}
	if !ok {
		stored = PRState{State: stIdle}
	}
	if !sameState(stored, p.from) {
		return fmt.Errorf("%s: the stored state is %s, the step was planned from %s: a write outside the PR's lock", pr.URL(), describe(stored), describe(p.from))
	}
	for _, ef := range p.effects {
		if err := commitEffect(tx, pr, ef, p.at); err != nil {
			return err
		}
	}
	if !deleted(p.to) {
		if err := mintFor(tx, pr, p.to); err != nil {
			return err
		}
	}
	for _, d := range p.decisions {
		if err := logDecision(tx, pr, p, d); err != nil {
			return err
		}
	}
	if deleted(p.to) {
		err = deletePRState(tx, pr)
	} else {
		err = setPRState(tx, pr, p.to)
	}
	if err != nil {
		return err
	}
	return tx.Commit()
}

// sameState compares two states as they are stored.
func sameState(a, b PRState) bool {
	ra, errA := json.Marshal(a)
	rb, errB := json.Marshal(b)
	return errA == nil && errB == nil && string(ra) == string(rb)
}

// performedEffects are the effects a step performs; a rule lists no other.
var performedEffects = []Effect{efStopWorkers, efRevokeTokens, efRoundsRead}

// commitEffect performs ef in a step's transaction. efStopWorkers is not
// the ledger's: it ran before the commit (tickHost.stopWorkers).
func commitEffect(q querier, pr PR, ef Effect, at time.Time) error {
	switch ef {
	case efStopWorkers:
		return nil
	case efRevokeTokens:
		return revokeTokens(q, pr)
	case efRoundsRead:
		return markPRRead(q, pr, at)
	}
	return fmt.Errorf("effect %s is not one a step performs", ef)
}

// mintFor mints the Approve link's token when s is the first state to need
// one at its head. An open mention holds the token back; the row says why,
// and viewFor mints once the mention clears.
func mintFor(q querier, pr PR, s PRState) error {
	if s.State != stAwaiting || s.Kind != kindApprove {
		return nil
	}
	if dismissing, err := dismissalPending(q, pr); err != nil || dismissing {
		return err
	}
	if _, has, err := tokenFor(q, pr, s.Head); err != nil || has {
		return err
	}
	if _, err := mintToken(q, pr, s.Head); err != nil && !errors.Is(err, errMentionOpen) {
		return err
	}
	return nil
}

// --- The decision log ------------------------------------------------------

// decisionsSchema is the decision log: one row per decided event, with the
// state it moved from and to and the facts it was decided on. It records
// decisions; no state is ever rebuilt from it. No column holds a token,
// and free text (a question, where a click came from) only as a hash.
const decisionsSchema = `
create table if not exists decisions (id integer primary key, pr text, at_ms integer not null,
	origin text not null check (origin in ('tick', 'link', 'verb')),
	build text not null,
	step text not null check (step in ('decided', 'recorded', 'failsafe', 'global')),
	event text, from_state text, to_state text,
	effects text not null,
	facts_kind text not null, facts text not null,
	check (step in ('failsafe', 'global') or (event is not null and from_state is not null and to_state is not null)));
`

func logDecision(q querier, pr PR, p plan, d decision) error {
	event, err := json.Marshal(logEvent(d.e))
	if err != nil {
		return err
	}
	from, err := json.Marshal(logState(d.from))
	if err != nil {
		return err
	}
	to, err := json.Marshal(logState(d.to))
	if err != nil {
		return err
	}
	effects, err := json.Marshal(d.effects)
	if err != nil {
		return err
	}
	f := factsOf(d.e)
	facts, err := json.Marshal(f)
	if err != nil {
		return err
	}
	_, err = q.Exec(`insert into decisions (pr, at_ms, origin, build, step, event, from_state, to_state, effects, facts_kind, facts)
		values (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		pr.URL(), p.at.UnixMilli(), firstNonEmpty(p.origin, originTick), version, stepDecided,
		string(event), string(from), string(to), string(effects), f.factsKind(), string(facts))
	return err
}

// hashed is text by its hash; empty stays empty.
func hashed(text string) string {
	if text == "" {
		return ""
	}
	return hashOf(text)
}

// logState is s as the log keeps it: free-text details (a question, where
// a click came from) hashed, every other detail kept, since variantOf
// branches on it, and times cut to milliseconds, in UTC. transition copies these
// details from the event, which logEvent hashes the same way, so a logged
// row replays to its logged to_state.
func logState(s PRState) PRState {
	switch (stateKey{s.State, s.Kind}) {
	case stateKey{stStarting, kindClick}, stateKey{stAwaiting, kindQuestion}:
		s.Detail = hashed(s.Detail)
	}
	s.Since = s.Since.Truncate(time.Millisecond).UTC()
	if s.Prev != nil {
		prev := logState(*s.Prev)
		s.Prev = &prev
	}
	return s
}

// loggedEvent is an Event as the log keeps it: From and Question hashed,
// the trigger as its stored kind, key and from.
type loggedEvent struct {
	Kind        EventKind   `json:"kind"`
	Head        string      `json:"head,omitempty"`
	Round       ids.RoundID `json:"round,omitzero"`
	From        string      `json:"from,omitempty"`
	Question    string      `json:"question,omitempty"`
	Class       string      `json:"class,omitempty"`
	Next        *PRState    `json:"next,omitempty"`
	TriggerKind string      `json:"trigger_kind,omitempty"`
	TriggerKey  string      `json:"trigger_key,omitempty"`
	TriggerFrom string      `json:"trigger_from,omitempty"`
}

func logEvent(e Event) loggedEvent {
	le := loggedEvent{Kind: e.Kind, Head: e.Head, Round: e.Round, From: hashed(e.From), Question: hashed(e.Question), Class: e.Class}
	if e.Next != nil {
		next := logState(*e.Next)
		le.Next = &next
	}
	if e.Trigger != nil {
		le.TriggerKind, le.TriggerKey = e.Trigger.Kind(), e.Trigger.DedupeKey()
		if c, ok := e.Trigger.(Click); ok {
			le.TriggerFrom = hashed(c.From)
		}
	}
	return le
}

// event is the logged event back, for replaying its row.
func (le loggedEvent) event() Event {
	e := Event{Kind: le.Kind, Head: le.Head, Round: le.Round, From: le.From, Question: le.Question, Class: le.Class, Next: le.Next}
	if le.TriggerKind != "" {
		e.Trigger = triggerOf(le.TriggerKind, le.TriggerKey, le.TriggerFrom)
	}
	return e
}

// Facts are what a decision was made on, one variant per source of
// events. Each has a fixed field list of ids, heads and classes, so a
// link, a token or free text has nowhere to go. The interface is sealed.
type Facts interface{ factsKind() string }

// FactsRound: a round's worker ran, waited or ended (settle).
type FactsRound struct {
	Round   ids.RoundID `json:"round,omitzero"`
	Head    string      `json:"head,omitempty"`
	Decided string      `json:"decided,omitempty"` // the state decide chose, as its pair
}

// FactsOwed: a review is owed at Head for Trigger (a trigger kind).
type FactsOwed struct {
	Head    string `json:"head,omitempty"`
	Trigger string `json:"trigger,omitempty"`
}

// FactsStanding: GitHub's standing of the PR: a push, the user's approval,
// a history rewritten under the first look.
type FactsStanding struct {
	Head string `json:"head,omitempty"`
}

// FactsWindow: the user's window on the PR opened or closed.
type FactsWindow struct {
	Head string `json:"head,omitempty"`
}

// FactsRoster: the worker list said a launch left no worker.
type FactsRoster struct {
	Class string `json:"class,omitempty"`
}

// FactsLaunch: a launch started a round, or why it started none.
type FactsLaunch struct {
	Round ids.RoundID `json:"round,omitzero"`
	Class string      `json:"class,omitempty"`
}

// FactsClick: the user clicked, or the click gave up.
type FactsClick struct {
	From string `json:"from,omitempty"` // hashed
}

// FactsClose: the PR is merged or closed.
type FactsClose struct{}

func (FactsRound) factsKind() string    { return "round" }
func (FactsOwed) factsKind() string     { return "owed" }
func (FactsStanding) factsKind() string { return "standing" }
func (FactsWindow) factsKind() string   { return "window" }
func (FactsRoster) factsKind() string   { return "roster" }
func (FactsLaunch) factsKind() string   { return "launch" }
func (FactsClick) factsKind() string    { return "click" }
func (FactsClose) factsKind() string    { return "close" }

// factsOf is the observation e reports, by the source that observes it.
func factsOf(e Event) Facts {
	switch e.Kind {
	case evEnded, evBlocked, evResumed:
		f := FactsRound{Round: e.Round, Head: e.Head}
		if e.Next != nil {
			f.Decided = describe(*e.Next)
		}
		return f
	case evOwed:
		f := FactsOwed{Head: e.Head}
		if e.Trigger != nil {
			f.Trigger = e.Trigger.Kind()
		}
		return f
	case evPush, evUserApproved, evChangedUnderFirstLook:
		return FactsStanding{Head: e.Head}
	case evWindowOpened, evWindowClosed:
		return FactsWindow{Head: e.Head}
	case evLaunchCleared:
		return FactsRoster{Class: e.Class}
	case evLaunched, evFailedToStart:
		return FactsLaunch{Round: e.Round, Class: e.Class}
	case evClick, evAbandoned:
		return FactsClick{From: hashed(e.From)}
	case evClosed:
		return FactsClose{}
	}
	panic(fmt.Sprintf("factsOf: event %q has no source", e.Kind))
}
