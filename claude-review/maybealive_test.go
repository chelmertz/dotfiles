package main

import (
	"slices"
	"strings"
	"testing"
)

// A launch whose worker may be alive (launched but not listed, or the
// roster unreadable) never launches again and never offers Start: a second
// worker would review beside the first.
func TestAMaybeAliveLaunchStartsNothing(t *testing.T) {
	alive := func(class string) PRState {
		return PRState{State: stHeld, Kind: kindLaunch, Detail: class, Head: "aaa"}
	}
	posted := PRState{State: stHandled, Kind: kindPosted, Head: "aaa"}
	for _, class := range []string{launchUnlisted, launchUnknown} {
		for _, e := range []Event{{Kind: evOwed, Trigger: Requested{At: t0}}, {Kind: evClick}} {
			if next, _, ok := transition(alive(class), e, t0); ok {
				t.Errorf("%s from held{%s} moved to %s{%s}", e.Kind, class, next.State, next.Kind)
			}
		}
		// A review that finished while its launch was unclear still lands.
		next, effects, ok := transition(alive(class), Event{Kind: evEnded, Next: &posted}, t0)
		if !ok || next.State != stHandled || len(effects) != 1 || effects[0] != efRoundsRead {
			t.Errorf("ended from held{%s}: %s{%s} %v %v", class, next.State, next.Kind, effects, ok)
		}
		// Only the roster re-check leaves it: nothing alive, nothing unread.
		next, _, ok = transition(alive(class), Event{Kind: evLaunchCleared, Detail: launchNotStarted}, t0)
		if !ok || next.State != stHeld || next.Kind != kindLaunch || next.Detail != launchNotStarted {
			t.Errorf("cleared from held{%s}: %+v %v", class, next, ok)
		}
		a := renderRow(widgetsPR, alive(class), View{})
		for _, l := range a.Links {
			if l.Primary && l.URL == startAgainLink(widgetsPR).URL {
				t.Errorf("held{%s} offers Start again", class)
			}
		}
		if a.Links[0] != primary(attachLink(widgetsPR)) {
			t.Errorf("held{%s} links %+v, want Open Claude primary", class, a.Links)
		}
	}
	for _, class := range []string{launchNotStarted, launchDuplicate, "old error text"} {
		if next, _, ok := transition(alive(class), Event{Kind: evOwed}, t0); !ok || next.State != stRunning {
			t.Errorf("owed from held{%s}: %+v %v", class, next, ok)
		}
		if next, _, ok := transition(alive(class), Event{Kind: evLaunchCleared, Detail: launchNotStarted}, t0); ok {
			t.Errorf("cleared from a dead held{%s}: %+v", class, next)
		}
	}
}

func TestTickReChecksAMaybeAliveLaunch(t *testing.T) {
	w := newTickWorld(t)
	pr := w.accept(t, 42)
	if err := w.h.ledger.SetPRState(pr, PRState{State: stHeld, Kind: kindLaunch, Detail: launchUnlisted, Head: "h1"}); err != nil {
		t.Fatal(err)
	}
	w.fs.workers = append(w.fs.workers, Worker{Short: pr.RoundSessionID().Key(), SessionID: pr.RoundSessionID(), PID: 4242, LaunchID: pr.RoundSessionID()})
	if err := w.h.tick(); err != nil {
		t.Fatal(err)
	}
	if s, _ := w.h.stateOf(pr); s.Detail != launchUnlisted {
		t.Errorf("a live worker's launch was cleared: %+v", s)
	}
	w.fs.workers = nil
	if err := w.h.tick(); err != nil {
		t.Fatal(err)
	}
	if s, _ := w.h.stateOf(pr); s.Detail != launchNotStarted {
		t.Errorf("no worker and no unread round, still %+v", s)
	}
	if a := w.elly.put[pr.URL()]; !strings.Contains(a.Note, "start it again") {
		t.Errorf("row %q", a.Note)
	}
}

func TestClickOnAMaybeAliveLaunchStartsNothing(t *testing.T) {
	h, fs := clickHost(t, true)
	if err := h.ledger(t).SetPRState(testPR, PRState{State: stHeld, Kind: kindLaunch, Detail: launchUnknown, Head: "h6"}); err != nil {
		t.Fatal(err)
	}
	if code := run([]string{target}, h.d); code != 1 || slices.Contains(fs.events, "launch") {
		t.Errorf("exit %d, events %q", code, fs.events)
	}
	if !strings.Contains(notified(h), "may be running") {
		t.Errorf("notified %q", notified(h))
	}
	if s, _, _ := h.ledger(t).PRState(testPR); s.Detail != launchUnknown {
		t.Errorf("state %+v", s)
	}
}
