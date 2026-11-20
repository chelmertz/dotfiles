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

// A stuck row knows two facts; anything else stored (a migrated row's old
// text) is unknown and gets neutral words, never "failed twice".
func TestStuckFailedWordsOnlyWhatItKnows(t *testing.T) {
	for detail, want := range map[string]string{
		noteFailedTwice:  noteFailedTwice,
		noteStoppedTwice: noteStoppedTwice,
		"exit status 3":  noteStoppedUnknown,
		"":               noteStoppedUnknown,
	} {
		if got := renderRow(widgetsPR, PRState{State: stStuck, Kind: kindFailed, Detail: detail}, View{}).Note; got != want {
			t.Errorf("%q: %q, want %q", detail, got, want)
		}
	}
}

// A window opened over an Approve row spends its token; the restore on
// close mints a fresh one, so no fork runs beside a live Approve.
func TestAWindowOverApproveRevokesItsToken(t *testing.T) {
	w := newTickWorld(t)
	pr := w.accept(t, 42)
	approve := PRState{State: stAwaiting, Kind: kindApprove, Head: "h1"}
	if err := w.h.move2(pr, approve); err != nil {
		t.Fatal(err)
	}
	old, _, _ := w.h.ledger.TokenFor(pr, "h1")
	if _, err := w.h.move(pr, Event{Kind: evWindowOpened}); err != nil {
		t.Fatal(err)
	}
	if live, _ := w.h.ledger.HasLiveToken(pr); live {
		t.Error("a token stayed live under the window")
	}
	if _, err := w.h.move(pr, Event{Kind: evWindowClosed}); err != nil {
		t.Fatal(err)
	}
	fresh, has, _ := w.h.ledger.TokenFor(pr, "h1")
	if !has || fresh == old {
		t.Errorf("after the window: token %v has %v, old %v", fresh, has, old)
	}
	if a := w.elly.put[pr.URL()]; !strings.Contains(a.Links[0].URL, fresh.String()) {
		t.Errorf("row %+v lacks the fresh token", a.Links)
	}
}

// refresh moves a state only under the PR's lock: a click holding it may
// be about to write its own state.
func TestRefreshSkipsALockedPR(t *testing.T) {
	w := newTickWorld(t)
	pr := w.accept(t, 42)
	if err := w.h.ledger.SetPRState(pr, PRState{State: stAwaiting, Kind: kindDismiss, Head: "h1"}); err != nil {
		t.Fatal(err)
	}
	w.gh.user = &Review{State: "APPROVED", CommitID: "h1"}
	w.locked[pr.SessionID().String()] = true
	if err := w.h.tick(); err != nil {
		t.Fatal(err)
	}
	if s, ok, _ := w.h.ledger.PRState(pr); !ok || s.Kind != kindDismiss {
		t.Errorf("a locked PR moved to %+v", s)
	}
	delete(w.locked, pr.SessionID().String())
	if err := w.h.tick(); err != nil {
		t.Fatal(err)
	}
	if s, _, _ := w.h.ledger.PRState(pr); s.State != stIdle {
		t.Errorf("the approval at the head did not clear the PR once unlocked: %+v", s)
	}
}
