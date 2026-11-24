package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"
)

// replayDecisions checks every decided row of l's log: its from_state and
// event, at its at_ms, replay through transition to its to_state.
func replayDecisions(l *Ledger) error {
	rows, err := l.db.Query(`select id, at_ms, event, from_state, to_state from decisions where step = 'decided'`)
	if err != nil {
		return err
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var id, at int64
		var event, from, to string
		if err := rows.Scan(&id, &at, &event, &from, &to); err != nil {
			return err
		}
		var le loggedEvent
		var s, want PRState
		if err := errors.Join(json.Unmarshal([]byte(event), &le), json.Unmarshal([]byte(from), &s), json.Unmarshal([]byte(to), &want)); err != nil {
			return fmt.Errorf("decision %d: %w", id, err)
		}
		next, _, ok := transition(s, le.event(), time.UnixMilli(at).UTC())
		if !ok {
			return fmt.Errorf("decision %d: %s on %s finds no rule on replay", id, describe(s), le.Kind)
		}
		if !sameState(next, want) {
			got, _ := json.Marshal(next)
			return fmt.Errorf("decision %d: %s on %s replays to %s, logged %s", id, describe(s), le.Kind, got, to)
		}
	}
	return rows.Err()
}

// decisionColumns is every decisions row's columns as text.
func decisionColumns(t *testing.T, l *Ledger) [][]string {
	t.Helper()
	rows, err := l.db.Query(`select coalesce(pr, ''), at_ms, origin, build, step, coalesce(event, ''), coalesce(from_state, ''),
		coalesce(to_state, ''), effects, facts_kind, facts from decisions`)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rows.Close() }()
	var out [][]string
	for rows.Next() {
		row := make([]string, 11)
		ptrs := make([]any, len(row))
		for i := range row {
			ptrs[i] = &row[i]
		}
		if err := rows.Scan(ptrs...); err != nil {
			t.Fatal(err)
		}
		out = append(out, row)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return out
}

// Under the lock only a bug changes the state between the plan and the
// commit: Commit refuses, and writes nothing.
func TestCommitRefusesAStateChangedSincePlanning(t *testing.T) {
	g := newGenWorldT(t, 1)
	pr := g.prs[0]
	lk, err := lockPR(pr, g.h.lock)
	if err != nil {
		t.Fatal(err)
	}
	defer lk.Unlock()
	p, ok := planStep(PRState{State: stIdle}, t0, Event{Kind: evOwed, Head: g.gh.head[1], Trigger: Requested{At: t0}})
	if !ok {
		t.Fatal("no rule for owed from idle")
	}
	stored := PRState{State: stHandled, Kind: kindNothing, Head: g.gh.head[1], Since: t0}
	if err := g.h.ledger.SetPRState(pr, stored); err != nil {
		t.Fatal(err)
	}
	if err := g.h.ledger.Commit(lk, pr, p); err == nil {
		t.Fatal("a plan from idle committed over handled")
	}
	if s := g.state(t, 1); !sameState(s, stored) {
		t.Errorf("the refused commit changed the state to %s", describe(s))
	}
	if rows := decisionColumns(t, g.h.ledger); len(rows) != 0 {
		t.Errorf("the refused commit logged %d decisions", len(rows))
	}
}

// Commit takes the PR's own lock, and no other.
func TestCommitRefusesAnotherPRsLock(t *testing.T) {
	g := newGenWorldT(t, 2)
	lk, err := lockPR(g.prs[1], g.h.lock)
	if err != nil {
		t.Fatal(err)
	}
	defer lk.Unlock()
	p, _ := planStep(PRState{State: stIdle}, t0, Event{Kind: evOwed, Trigger: Requested{At: t0}})
	if err := g.h.ledger.Commit(lk, g.prs[0], p); err == nil {
		t.Error("a commit under another PR's lock was written")
	}
	for n := 1; n <= 2; n++ {
		if s := g.state(t, n); s.State != "" {
			t.Errorf("PR %d: the refused commit stored %s", n, describe(s))
		}
	}
}

// A history that asks a question, mints an Approve and closes: every
// commit logs a decided row per event, from tick, with the facts of its
// source, and each replays.
func TestEveryCommitLogsADecidedRowThatReplays(t *testing.T) {
	g := newGenWorldT(t, 1)
	sid := g.prs[0].RoundSessionID()
	g.mustTick(t)
	g.setJob(sid, "blocked", "which config?")
	g.passes(t, 1)
	g.setJob(sid, "working", "")
	msg, _ := g.message(1, "approve at head")
	g.finish(1, msg, "")
	g.passes(t, 2)
	if s := g.state(t, 1); s.State != stAwaiting || s.Kind != kindApprove {
		t.Fatalf("no Approve: %s", describe(s))
	}
	g.gh.open[1] = false
	g.passes(t, 1)
	rows := decisionColumns(t, g.h.ledger)
	var events []string
	for _, r := range rows {
		var le loggedEvent
		if err := json.Unmarshal([]byte(r[5]), &le); err != nil {
			t.Fatal(err)
		}
		events = append(events, string(le.Kind))
		if r[2] != originTick || r[3] != version || r[4] != stepDecided {
			t.Errorf("row %v: want origin tick, build %s, step decided", r[:5], version)
		}
		if want := factsOf(le.event()).factsKind(); r[9] != want {
			t.Errorf("%s logged facts %s, want %s", le.Kind, r[9], want)
		}
	}
	want := []string{"owed", "blocked", "ended", "closed"}
	if !slices.Equal(events, want) {
		t.Errorf("logged events %v, want %v", events, want)
	}
	if err := replayDecisions(g.h.ledger); err != nil {
		t.Error(err)
	}
}

// No token value reaches the log, and a question only as its hash.
func TestDecisionLogHoldsNoTokenAndNoQuestionText(t *testing.T) {
	g := newGenWorldT(t, 1)
	sid := g.prs[0].RoundSessionID()
	const question = "which config, staging or prod?"
	g.mustTick(t)
	g.setJob(sid, "blocked", question)
	g.passes(t, 1)
	g.setJob(sid, "working", "")
	msg, _ := g.message(1, "approve at head")
	g.finish(1, msg, "")
	g.passes(t, 1)
	g.gh.head[1], g.update[1] = g.sha(), g.now // a push revokes, the next Approve mints again
	g.passes(t, 1)
	msg, _ = g.message(1, "approve at head")
	g.finish(1, msg, "")
	g.passes(t, 1)
	// The approvals: the link spends the token, then the user types one.
	head := g.gh.head[1]
	tok, has, err := g.h.ledger.TokenFor(g.prs[0], head)
	if err != nil || !has {
		t.Fatalf("no token at the head: %v", err)
	}
	if ok, err := g.h.ledger.UseToken(g.prs[0], head, tok, winClosed); !ok || err != nil {
		t.Fatalf("spend: %v %v", ok, err)
	}
	for _, origin := range []string{originLink, originVerb} {
		if err := g.h.ledger.LogApproval(g.prs[0], head, origin, outcomeApproved, g.now); err != nil {
			t.Fatal(err)
		}
	}
	if n := len(approveRows(t, g.h.ledger)); n != 2 {
		t.Fatalf("%d approve rows, want 2", n)
	}
	rows, err := g.h.ledger.db.Query(`select token from tokens`)
	if err != nil {
		t.Fatal(err)
	}
	var tokens []string
	for rows.Next() {
		var tok string
		if err := rows.Scan(&tok); err != nil {
			t.Fatal(err)
		}
		tokens = append(tokens, tok)
	}
	if err := errors.Join(rows.Err(), rows.Close()); err != nil {
		t.Fatal(err)
	}
	if len(tokens) < 2 {
		t.Fatalf("the history minted %d tokens, want 2", len(tokens))
	}
	logged := decisionColumns(t, g.h.ledger)
	if len(logged) == 0 {
		t.Fatal("nothing logged")
	}
	for _, row := range logged {
		for i, col := range row {
			for _, tok := range tokens {
				if strings.Contains(col, tok) {
					t.Errorf("decisions column %d holds a token: %s", i, col)
				}
			}
			if strings.Contains(col, question) {
				t.Errorf("decisions column %d holds the question: %s", i, col)
			}
		}
	}
}

// A decided row carries its event and both states; only failsafe and
// global rows may go without.
func TestDecisionsRefuseADecidedRowWithoutItsStates(t *testing.T) {
	l := openTestLedger(t)
	insert := `insert into decisions (pr, at_ms, origin, build, step, effects, facts_kind, facts) values ('x', 0, 'tick', 'dev', ?, '[]', 'close', '{}')`
	if _, err := l.db.Exec(insert, stepDecided); err == nil {
		t.Error("a decided row without event and states was stored")
	}
	if _, err := l.db.Exec(insert, "global"); err != nil {
		t.Errorf("a global row: %v", err)
	}
}

// Each Facts variant carries the events of its one source, and every event
// has one.
func TestFactsVariantsCarryTheirSourcesEvents(t *testing.T) {
	carries := map[string][]EventKind{
		"round":    {evEnded, evBlocked, evResumed},
		"owed":     {evOwed},
		"standing": {evPush, evUserApproved, evChangedUnderFirstLook},
		"window":   {evWindowOpened, evWindowClosed},
		"roster":   {evLaunchCleared},
		"launch":   {evLaunched, evFailedToStart},
		"click":    {evClick, evAbandoned},
		"close":    {evClosed},
	}
	seen := map[EventKind]bool{}
	for kind, events := range carries {
		for _, e := range events {
			if got := factsOf(Event{Kind: e}).factsKind(); got != kind {
				t.Errorf("%s: facts %s, want %s", e, got, kind)
			}
			seen[e] = true
		}
	}
	for _, r := range rules {
		if !seen[r.event] {
			t.Errorf("rule event %s has no facts source", r.event)
		}
	}
}
