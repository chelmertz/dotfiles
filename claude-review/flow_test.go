package main

import (
	"testing"
	"time"
)

// A worker blocked on a question that works again (the user answered in the
// session) shows as running, and its end settles the round as usual.
func TestTickAResumedWorkerRunsAgain(t *testing.T) {
	w := newTickWorld(t)
	pr := w.accept(t, 1)
	if _, err := w.h.ledger.StartRound(pr, "h1", NewHead{}, t0); err != nil {
		t.Fatal(err)
	}
	if err := w.h.ledger.SetPRState(pr, PRState{State: stRunning, Kind: kindTick, Head: "h1", Since: t0}); err != nil {
		t.Fatal(err)
	}
	w.gh.facts = RoundFacts{State: "open", Head: "h1", RoundReviewURL: "r"}
	w.blocked[pr.RoundSessionID()] = "which table?"
	if err := w.h.tick(); err != nil {
		t.Fatal(err)
	}
	if s, _ := w.h.peekState(pr); s.State != stAwaiting || s.Kind != kindQuestion {
		t.Fatalf("blocked: %+v", s)
	}
	delete(w.blocked, pr.RoundSessionID())
	w.live[pr.RoundSessionID()] = true
	if err := w.h.tick(); err != nil {
		t.Fatal(err)
	}
	if s, _ := w.h.peekState(pr); s.State != stRunning || s.Kind != kindTick {
		t.Fatalf("resumed: %+v", s)
	}
	delete(w.live, pr.RoundSessionID())
	if err := w.h.tick(); err != nil {
		t.Fatal(err)
	}
	if s, _ := w.h.peekState(pr); s.State != stHandled || s.Kind != kindPosted {
		t.Fatalf("ended: %+v", s)
	}
}

// stuck holds against a push and gives way to a review (re-)request.
func TestTickStuckLiftsOnlyOnARerequest(t *testing.T) {
	w := newTickWorld(t)
	pr := w.accept(t, 42)
	if err := w.h.ledger.SetPRState(pr, PRState{State: stStuck, Kind: kindFailed, Head: "h0"}); err != nil {
		t.Fatal(err)
	}
	w.elly.prs = []EllyPR{ellyPR(func(p *EllyPR) { p.HeadRefOid = "h1" })}
	if err := w.h.tick(); err != nil {
		t.Fatal(err)
	}
	if len(w.started) != 0 {
		t.Fatalf("a push lifted stuck: %q", w.started)
	}
	w.elly.prs = []EllyPR{ellyPR(func(p *EllyPR) {
		p.HeadRefOid = "h1"
		p.Asked = askedAs(askedRequested)
		p.LastUpdated = t0.Add(time.Hour)
	})}
	if err := w.h.tick(); err != nil {
		t.Fatal(err)
	}
	if s, _ := w.h.peekState(pr); len(w.started) != 1 || s.State != stRunning {
		t.Fatalf("a re-request did not lift stuck: started %q, state %+v", w.started, s)
	}
}
