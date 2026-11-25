package main

import (
	"bytes"
	"errors"
	"strings"
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

// The journal line names what a step did: the move, its own writes after
// it, or the writes alone when the state stays.
func TestStepJournalLineNamesTheMoveAndTheWrites(t *testing.T) {
	note := ledgerWrite{"note it", func(querier, time.Time) error { return nil }}
	owed := must(Event{Kind: evOwed, Head: "h1", Trigger: Requested{At: t0}})
	for name, c := range map[string]struct {
		writes []ledgerWrite
		events []folded
		want   string
		not    string
	}{
		"move only":   {nil, []folded{owed}, "move " + widgetsPR.URL() + " from idle", "; "},
		"writes only": {[]ledgerWrite{note}, nil, "note it of " + widgetsPR.URL(), "move "},
		"both":        {[]ledgerWrite{note}, []folded{owed}, " on owed; note it", "note it of"},
	} {
		w := newTickWorld(t)
		out := &bytes.Buffer{}
		w.h.out = out
		lk := mustLock(t, w.h, widgetsPR)
		if _, ok, err := w.h.step(lk, widgetsPR, c.writes, c.events...); err != nil || !ok {
			t.Fatalf("%s: ok %v err %v", name, ok, err)
		}
		lk.Unlock()
		if got := out.String(); !strings.Contains(got, c.want) || strings.Contains(got, c.not) {
			t.Errorf("%s: journal %q, want %q and no %q", name, got, c.want, c.not)
		}
	}
}

// A worker that will not stop fails the stop, so the step it guards does
// not commit over a live worker.
func TestStopWorkersFailsWhenAWorkerWillNotStop(t *testing.T) {
	w := newTickWorld(t)
	w.fs.workers = []Worker{workerFor(widgetsPR.SessionID(), 100)}
	w.fs.stopErr = errors.New("no tty")
	if err := w.h.stopWorkers(widgetsPR); err == nil || !strings.Contains(err.Error(), "no tty") {
		t.Errorf("err %v, want the stop's failure", err)
	}
}
