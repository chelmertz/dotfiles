package main

import (
	"encoding/json"
	"strconv"
	"testing"
	"time"

	"github.com/chelmertz/dotfiles/claude-review/ids"
)

// rid is round n's id, as the ledger would number it.
func rid(n int) ids.RoundID {
	var r ids.RoundID
	if err := json.Unmarshal([]byte(strconv.Itoa(n)), &r); err != nil {
		panic(err)
	}
	return r
}

// A state that owns a worker names its round, from the event that made it.
func TestWorkerStatesNameTheirRound(t *testing.T) {
	next, _, _ := transition(PRState{State: stIdle, Head: "h1"}, Event{Kind: evOwed, Head: "h1", Round: rid(1)}, t0)
	if next.State != stRunning || next.Round != rid(1) {
		t.Errorf("owed: %+v", next)
	}
	starting := PRState{State: stStarting, Kind: kindClick, Head: "h1"}
	if next, _, _ = transition(starting, Event{Kind: evLaunched, Round: rid(2)}, t0); next.Round != rid(2) {
		t.Errorf("launched: %+v", next)
	}
	running := PRState{State: stRunning, Kind: kindTick, Head: "h1", Round: rid(3)}
	next, _, _ = transition(running, Event{Kind: evBlocked, Question: "q", Round: rid(3)}, t0)
	if next.State != stAwaiting || next.Round != rid(3) || next.Prev == nil || next.Prev.Round != rid(3) {
		t.Errorf("blocked: %+v", next)
	}
	if next, _, _ = transition(PRState{State: stIdle}, Event{Kind: evClick, Round: rid(4)}, t0); !next.Round.IsZero() {
		t.Errorf("a starting named round %s", next.Round)
	}
}

// Ended, Blocked and Resumed act on the element that names their round,
// under a window as on top; a window's session is the user's, so the
// window stays on top and only the element below it changes.
func TestRoundEventsActOnTheElementThatNamesTheirRound(t *testing.T) {
	posted := PRState{State: stHandled, Kind: kindPosted}
	running := PRState{State: stRunning, Kind: kindTick, Head: "h1", Round: rid(1)}
	window := PRState{State: stHeld, Kind: kindWindow, Head: "h1", Prev: &running}

	next, effects, ok := transition(window, Event{Kind: evEnded, Round: rid(1), Next: &posted}, t0)
	if !ok || next.Kind != kindWindow || next.Prev == nil || next.Prev.State != stHandled || len(effects) != 1 || effects[0] != efRoundsRead {
		t.Errorf("ended under a window: %s, prev %+v, %v %v", describe(next), next.Prev, effects, ok)
	}
	next, _, ok = transition(window, Event{Kind: evBlocked, Round: rid(1), Question: "q"}, t0)
	if !ok || next.Kind != kindWindow || next.Prev == nil || next.Prev.Kind != kindQuestion || next.Prev.Prev == nil || next.Prev.Prev.Round != rid(1) {
		t.Errorf("blocked under a window: %s, prev %+v, %v", describe(next), next.Prev, ok)
	}
	asked := next
	next, _, ok = transition(asked, Event{Kind: evResumed, Round: rid(1)}, t0)
	if !ok || next.Kind != kindWindow || next.Prev == nil || next.Prev.State != stRunning || next.Prev.Round != rid(1) {
		t.Errorf("resumed under a window: %s, prev %+v, %v", describe(next), next.Prev, ok)
	}
}

// A round no element names is not the state's: nothing moves, and the
// caller reads it and drops it. A state stored before rounds were named
// takes any round, as before; idle takes one too, for a round recorded
// without its move.
func TestARoundNoElementNamesMovesNothing(t *testing.T) {
	posted := PRState{State: stHandled, Kind: kindPosted}
	running := PRState{State: stRunning, Kind: kindTick, Head: "h1", Round: rid(2)}
	if namesRound(running, rid(1)) {
		t.Error("round 1 named by running{2}")
	}
	if next, _, ok := transition(running, Event{Kind: evEnded, Round: rid(1), Next: &posted}, t0); ok {
		t.Errorf("an older round ended the running one: %s", describe(next))
	}
	legacy := PRState{State: stRunning, Kind: kindTick, Head: "h1"}
	if !namesRound(legacy, rid(1)) {
		t.Error("a running stored without its round refused a round")
	}
	if !namesRound(PRState{State: stIdle}, rid(1)) {
		t.Error("idle refused a round recorded without its move")
	}
	if namesRound(PRState{State: stHandled, Kind: kindPosted}, rid(1)) {
		t.Error("a handled state named a round")
	}
}

// A worker that ends under an open window settles into the chain: closing
// the window then brings back its result, not a running row no worker
// stands behind. An older round's outcome is read and changes nothing.
func TestARoundEndingUnderAWindowSettlesBelowIt(t *testing.T) {
	w := newTickWorld(t)
	pr := w.accept(t, 42)
	r, err := w.h.ledger.StartRound(pr, "h1", Requested{At: t0}, t0)
	if err != nil {
		t.Fatal(err)
	}
	running := PRState{State: stRunning, Kind: kindTick, Head: "h1", Round: r}
	if err := w.h.ledger.SetPRState(pr, PRState{State: stHeld, Kind: kindWindow, Head: "h1", Prev: &running}); err != nil {
		t.Fatal(err)
	}
	posted := PRState{State: stHandled, Kind: kindPosted, Head: "h1"}
	if _, err := w.h.apply(mustLock(t, w.h, pr), pr, Round{ID: r, PR: pr.URL(), Head: "h1"}, Next{State: posted}, true); err != nil {
		t.Fatal(err)
	}
	s, _, _ := w.h.ledger.PRState(pr)
	if s.Kind != kindWindow || s.Prev == nil || s.Prev.State != stHandled {
		t.Errorf("after the end under the window: %s, prev %+v", describe(s), s.Prev)
	}
	if unread, _ := w.h.ledger.UnreadRounds(); len(unread) != 0 {
		t.Errorf("the settled round is unread: %+v", unread)
	}

	older, err := w.h.ledger.StartRound(pr, "h1", Requested{At: t0}, t0)
	if err != nil {
		t.Fatal(err)
	}
	newer, err := w.h.ledger.StartRound(pr, "h1", Requested{At: t0}, t0)
	if err != nil {
		t.Fatal(err)
	}
	if err := w.h.ledger.SetPRState(pr, PRState{State: stRunning, Kind: kindTick, Head: "h1", Round: newer}); err != nil {
		t.Fatal(err)
	}
	if _, err := w.h.apply(mustLock(t, w.h, pr), pr, Round{ID: older, PR: pr.URL(), Head: "h1"}, Next{State: posted}, true); err != nil {
		t.Fatal(err)
	}
	if s, _, _ := w.h.ledger.PRState(pr); s.State != stRunning || s.Round != newer {
		t.Errorf("an older round's end moved the running one: %s", describe(s))
	}
	unread, _ := w.h.ledger.UnreadRounds()
	if len(unread) != 1 || unread[0].ID != newer {
		t.Errorf("unread %+v, want only the running round", unread)
	}
}

// The answer to a question is a reply in a thread of the round that asked
// it, not of whichever round settled last: with a later round blocked on
// its question, a reply in an earlier round's thread answers nothing.
func TestAnAnswerIsLookedForInTheQuestionsOwnRound(t *testing.T) {
	w := newTickWorld(t)
	pr := w.accept(t, 42)
	earlier, err := w.h.ledger.StartRound(pr, "h1", NewHead{}, time.Now().Add(-2*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if err := w.h.ledger.MarkRead(earlier); err != nil {
		t.Fatal(err)
	}
	askedAt := time.Now().Add(-time.Hour).Truncate(time.Second)
	asking, err := w.h.ledger.StartRound(pr, "h1", Requested{At: askedAt}, askedAt)
	if err != nil {
		t.Fatal(err)
	}
	running := PRState{State: stRunning, Kind: kindTick, Head: "h1", Round: asking}
	if err := w.h.ledger.SetPRState(pr, PRState{State: stAwaiting, Kind: kindQuestion, Detail: "q", Head: "h1", Round: asking, Prev: &running}); err != nil {
		t.Fatal(err)
	}
	s, _, _ := w.h.ledger.PRState(pr)
	if _, err := w.h.answered(pr, s); err != nil {
		t.Fatal(err)
	}
	if !w.gh.repliedSince.Equal(askedAt) {
		t.Errorf("looked for the answer since %s, want the asking round's start %s", w.gh.repliedSince, askedAt)
	}
}

// No retry resumes into a session the user has open: with a window in the
// chain, a round cut off before its summary is read, and the running
// state under the window becomes idle rather than outliving its round.
func TestNoRetryLaunchesUnderAWindow(t *testing.T) {
	w := newTickWorld(t)
	pr := w.accept(t, 42)
	r, err := w.h.ledger.StartRound(pr, "h1", Requested{At: t0}, t0)
	if err != nil {
		t.Fatal(err)
	}
	running := PRState{State: stRunning, Kind: kindTick, Head: "h1", Round: r}
	if err := w.h.ledger.SetPRState(pr, PRState{State: stHeld, Kind: kindWindow, Head: "h1", Prev: &running}); err != nil {
		t.Fatal(err)
	}
	if started, err := w.h.apply(mustLock(t, w.h, pr), pr, Round{ID: r, PR: pr.URL(), Head: "h1"}, Retry{}, true); err != nil || started {
		t.Fatalf("retry under a window: started %v err %v", started, err)
	}
	if len(w.started) != 0 {
		t.Errorf("a retry launched under the window: %d", len(w.started))
	}
	s, _, _ := w.h.ledger.PRState(pr)
	if s.Kind != kindWindow || s.Prev == nil || s.Prev.State != stIdle {
		t.Errorf("after the retry under the window: %s, prev %+v", describe(s), s.Prev)
	}
	if unread, _ := w.h.ledger.UnreadRounds(); len(unread) != 0 {
		t.Errorf("the cut-off round is unread: %+v", unread)
	}
}
