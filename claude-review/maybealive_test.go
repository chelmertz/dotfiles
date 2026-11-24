package main

import (
	"github.com/chelmertz/dotfiles/claude-review/ids"

	"fmt"
	"slices"
	"strings"
	"testing"
	"time"
)

// A launch whose worker may be alive (launched but not listed, or the
// roster unreadable) never launches again and never offers Start: a second
// worker would review beside the first.
func TestAMaybeAliveLaunchStartsNothing(t *testing.T) {
	alive := func(class string) PRState {
		return PRState{State: stHeld, Kind: kindLaunch, Detail: class, Head: "aaa"}
	}
	posted := PRState{State: stHandled, Kind: kindPosted, Head: "aaa"}
	// Text no class names (an error stored before the classes, or none)
	// reads as a roster that could not be read, as the converter has it.
	for _, class := range []string{launchUnlisted, launchUnknown, "old error text", ""} {
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
		next, _, ok = transition(alive(class), Event{Kind: evLaunchCleared, Class: launchNotStarted}, t0)
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
	for _, class := range []string{launchNotStarted, launchDuplicate} {
		if next, _, ok := transition(alive(class), Event{Kind: evOwed}, t0); !ok || next.State != stRunning {
			t.Errorf("owed from held{%s}: %+v %v", class, next, ok)
		}
		if next, _, ok := transition(alive(class), Event{Kind: evLaunchCleared, Class: launchNotStarted}, t0); ok {
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
	if s, _ := w.h.peekState(pr); s.Detail != launchUnlisted {
		t.Errorf("a live worker's launch was cleared: %+v", s)
	}
	w.fs.workers = nil
	if err := w.h.tick(); err != nil {
		t.Fatal(err)
	}
	if s, _ := w.h.peekState(pr); s.Detail != launchNotStarted {
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
	if _, err := w.h.move(mustLock(t, w.h, pr), pr, Event{Kind: evWindowOpened}); err != nil {
		t.Fatal(err)
	}
	if live, _ := w.h.ledger.HasLiveToken(pr); live {
		t.Error("a token stayed live under the window")
	}
	if _, err := w.h.move(mustLock(t, w.h, pr), pr, Event{Kind: evWindowClosed}); err != nil {
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

// A window that closed is dropped wherever it sits in the chain, so a click
// over it cannot later restore a window that is gone and park the PR.
func TestAWindowClosedMidChainIsDropped(t *testing.T) {
	question := PRState{State: stAwaiting, Kind: kindQuestion, Head: "aaa"}
	window := PRState{State: stHeld, Kind: kindWindow, Head: "aaa", Prev: &question}
	starting := PRState{State: stStarting, Kind: kindClick, Head: "aaa", Prev: &window}
	next, _, ok := transition(starting, Event{Kind: evWindowClosed}, t0)
	if !ok || next.State != stStarting || next.Prev == nil || next.Prev.Kind != kindQuestion {
		t.Fatalf("got %+v %v, want starting over the question", next, ok)
	}
	if _, _, ok := transition(question, Event{Kind: evWindowClosed}, t0); ok {
		t.Error("a chain with no window moved")
	}

	w := newTickWorld(t)
	pr := w.accept(t, 42)
	if err := w.h.ledger.SetPRState(pr, PRState{State: stStarting, Kind: kindClick, Head: "h1", Since: t0, Prev: &PRState{State: stHeld, Kind: kindWindow, Head: "h1", Prev: &PRState{State: stHandled, Kind: kindPosted, Head: "h1"}}}); err != nil {
		t.Fatal(err)
	}
	if err := w.h.tick(); err != nil {
		t.Fatal(err)
	}
	s, _, _ := w.h.ledger.PRState(pr)
	for e := &s; e != nil; e = e.Prev {
		if e.Kind == kindWindow {
			t.Errorf("a closed window stayed in the chain: %+v", s)
		}
	}
}

// A restore lands on what it remembers only at the same head: a question
// or an Approve judged at an older head is dropped to idle, its worker
// stopped and its token spent, as a push does.
func TestARestoreChecksTheHeadOverTheChain(t *testing.T) {
	approve := PRState{State: stAwaiting, Kind: kindApprove, Head: "old"}
	window := PRState{State: stHeld, Kind: kindWindow, Head: "old", Prev: &approve}
	restoreEffects := []Effect{efStopWorkers, efRevokeTokens, efRoundsRead}

	next, effects, ok := transition(window, Event{Kind: evWindowClosed, Head: "new"}, t0)
	if !ok || next.State != stIdle || next.Head != "new" || !slices.Equal(effects, restoreEffects) {
		t.Errorf("moved head: %+v %v %v", next, effects, ok)
	}
	next, effects, ok = transition(window, Event{Kind: evWindowClosed, Head: "old"}, t0)
	if !ok || next.Kind != kindApprove || len(effects) != 0 {
		t.Errorf("same head: %+v %v", next, effects)
	}
	// Unknown head: nothing proves it moved, so the restore stands.
	if next, _, _ = transition(window, Event{Kind: evWindowClosed}, t0); next.Kind != kindApprove {
		t.Errorf("unknown head: %+v", next)
	}
	// Deeper in the chain counts too.
	question := PRState{State: stAwaiting, Kind: kindQuestion, Head: "old"}
	posted := PRState{State: stHandled, Kind: kindPosted, Head: "new", Prev: &question}
	starting := PRState{State: stStarting, Kind: kindClick, Head: "new", Prev: &posted}
	if next, _, _ = transition(starting, Event{Kind: evAbandoned, Head: "new"}, t0); next.State != stIdle {
		t.Errorf("an old question deep in the chain survived: %+v", next)
	}
	// Held states keep their head across a push, as today.
	fresh := PRState{State: stHeld, Kind: kindFresh, Head: "old"}
	w2 := PRState{State: stHeld, Kind: kindWindow, Head: "old", Prev: &fresh}
	if next, _, _ = transition(w2, Event{Kind: evWindowClosed, Head: "new"}, t0); next.Kind != kindFresh {
		t.Errorf("held fresh lost across a restore: %+v", next)
	}
}

func TestTickClosingAWindowAfterAPushDropsTheOldApprove(t *testing.T) {
	w := newTickWorld(t)
	pr := w.accept(t, 42)
	approve := PRState{State: stAwaiting, Kind: kindApprove, Head: "old"}
	if err := w.h.ledger.SetPRState(pr, PRState{State: stHeld, Kind: kindWindow, Head: "old", Prev: &approve}); err != nil {
		t.Fatal(err)
	}
	w.gh.state, w.gh.head = "open", "new"
	if err := w.h.tick(); err != nil {
		t.Fatal(err)
	}
	if s, _, _ := w.h.ledger.PRState(pr); s.State != stIdle {
		t.Errorf("an Approve judged before the push came back: %+v", s)
	}
}

// GitHub is asked about a PR with a row only when elly saw it change since
// the last answer that moved nothing; a PR elly does not list is asked
// every pass, since missing from elly is not evidence either way.
func TestRefreshAsksGitHubOnlyAfterAChange(t *testing.T) {
	w := newTickWorld(t)
	pr := w.accept(t, 42)
	if err := w.h.ledger.SetPRState(pr, PRState{State: stHandled, Kind: kindPosted, Head: "h1"}); err != nil {
		t.Fatal(err)
	}
	listed := EllyPR{Url: pr.URL(), RepoOwner: "acme", RepoName: "widgets", Author: "a", HeadRefOid: "h1", LastUpdated: t0, Buried: true}
	w.elly.prs = []EllyPR{listed}
	w.gh.state, w.gh.head = "open", "h1"
	for range 3 {
		if err := w.h.tick(); err != nil {
			t.Fatal(err)
		}
	}
	if w.gh.standings != 1 {
		t.Errorf("asked GitHub %d times about an unchanged PR, want 1", w.gh.standings)
	}
	// The user approves: the PR's update time moves, and the approval lands.
	w.elly.prs[0].LastUpdated = t0.Add(time.Minute)
	w.gh.user = &Review{ID: reviewID(3), State: "APPROVED", CommitID: "h1"}
	if err := w.h.tick(); err != nil {
		t.Fatal(err)
	}
	if s, _, _ := w.h.ledger.PRState(pr); s.State != stIdle {
		t.Errorf("an approval after a change was missed: %+v\n%s", s, w.out)
	}
	if err := w.h.ledger.SetPRState(pr, PRState{State: stHandled, Kind: kindPosted, Head: "h1"}); err != nil {
		t.Fatal(err)
	}
	w.elly.prs, w.gh.user = nil, nil
	before := w.gh.standings
	for range 2 {
		_ = w.h.tick()
	}
	if w.gh.standings-before != 2 {
		t.Errorf("a PR elly does not list was asked %d times in 2 passes", w.gh.standings-before)
	}
}

// An effect the host cannot perform fails rather than reading as done.
func TestAHostWithoutSessionsCannotStopWorkers(t *testing.T) {
	h := tickHost{ledger: openTestLedger(t), now: func() time.Time { return t0 }}
	if err := h.stopWorkers(widgetsPR); err == nil {
		t.Error("stopping workers without a session list succeeded")
	}
}

// A re-request on a question row at the same head launches a new round; the
// question's worker is stopped and its round read first, so neither a live
// worker nor an unread round is left behind.
func TestAReRequestOnAQuestionStopsItsWorker(t *testing.T) {
	w := newTickWorld(t)
	pr := w.accept(t, 1)
	if _, err := w.h.ledger.StartRound(pr, "h1", NewHead{Head: "h1"}, t0.Add(-time.Hour)); err != nil {
		t.Fatal(err)
	}
	w.gh.facts = RoundFacts{State: "open", Head: "h1"}
	w.gh.state, w.gh.head = prOpen, "h1"
	w.fs.workers = append(w.fs.workers, workerFor(pr.RoundSessionID(), 601))
	w.fs.states[pr.RoundSessionID()] = "blocked"
	w.blocked[pr.RoundSessionID()] = "which table?"
	if err := w.h.tick(); err != nil {
		t.Fatal(err)
	}
	if s, _ := w.h.peekState(pr); s.Kind != kindQuestion {
		t.Fatalf("setup: %+v", s)
	}
	re := requestedPR(1)
	re.HeadRefOid, re.LastUpdated = "h1", t0.Add(time.Minute)
	w.elly.prs = []EllyPR{re}
	w.h.now = func() time.Time { return t0.Add(2 * time.Minute) }
	if err := w.h.tick(); err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(w.fs.events, "stop "+pr.RoundSessionID().Key().String()) {
		t.Errorf("the question's worker was not stopped: %q\n%s", w.fs.events, w.out)
	}
	unread, _ := w.h.ledger.UnreadRounds()
	if len(unread) != 1 || unread[0].Trigger.Kind() != trigRequested {
		t.Errorf("unread %+v, want only the new round", unread)
	}
}

// A click that died after "starting" and before its launch leaves the PR
// unstartable: owed rounds and clicks both refuse starting. Tick recovers
// it after startingLifetime, under the PR's lock.
func TestTickRecoversAStartingAClickLeftBehind(t *testing.T) {
	posted := PRState{State: stHandled, Kind: kindPosted, Head: "h1"}
	starting := PRState{State: stStarting, Kind: kindClick, Head: "h1", Since: t0, Prev: &posted}
	for _, c := range []struct {
		name   string
		setup  func(w *tickWorld, pr PR)
		state  State
		detail string
	}{
		{"nothing launched", func(*tickWorld, PR) {}, stHandled, ""},
		{"a worker but no round", func(w *tickWorld, pr PR) {
			w.fs.workers = append(w.fs.workers, workerFor(pr.RoundSessionID(), 700))
			w.live[pr.RoundSessionID()] = true
		}, stHeld, launchUnlisted},
		{"the roster unreadable", func(w *tickWorld, _ PR) { w.fs.unreadable = true }, stHeld, launchUnknown},
		{"a round recorded", func(w *tickWorld, pr PR) {
			if _, err := w.h.ledger.StartRound(pr, "h1", Click{At: t0.Add(time.Second)}, t0.Add(time.Second)); err != nil {
				t.Fatal(err)
			}
			w.live[pr.RoundSessionID()] = true
		}, stRunning, ""},
	} {
		t.Run(c.name, func(t *testing.T) {
			w := newTickWorld(t)
			pr := w.accept(t, 42)
			if err := w.h.ledger.SetPRState(pr, starting); err != nil {
				t.Fatal(err)
			}
			c.setup(w, pr)
			w.h.now = func() time.Time { return t0.Add(time.Minute) }
			_ = w.h.tick()
			if s, _ := w.h.peekState(pr); s.State != stStarting {
				t.Fatalf("recovered inside startingLifetime: %+v", s)
			}
			w.h.now = func() time.Time { return t0.Add(5 * time.Minute) }
			_ = w.h.tick()
			if s, _ := w.h.peekState(pr); s.State != c.state || s.Detail != c.detail {
				t.Errorf("got %s{%s} %q, want %s %q", s.State, s.Kind, s.Detail, c.state, c.detail)
			}
		})
	}
	w := newTickWorld(t)
	pr := w.accept(t, 42)
	if err := w.h.ledger.SetPRState(pr, starting); err != nil {
		t.Fatal(err)
	}
	w.locked[pr.SessionID().String()] = true
	w.h.now = func() time.Time { return t0.Add(5 * time.Minute) }
	_ = w.h.tick()
	if s, _ := w.h.peekState(pr); s.State != stStarting {
		t.Errorf("a click holding the lock lost its starting: %+v", s)
	}
}

// The question remembered under the click has its own waiting worker; it
// is not the click's launch, so the question comes back.
func TestTickRecoveringStartingKeepsAQuestionsWorker(t *testing.T) {
	w := newTickWorld(t)
	pr := w.accept(t, 42)
	question := PRState{State: stAwaiting, Kind: kindQuestion, Detail: "which?", Head: "h1"}
	if err := w.h.ledger.SetPRState(pr, PRState{State: stStarting, Kind: kindClick, Head: "h1", Since: t0, Prev: &question}); err != nil {
		t.Fatal(err)
	}
	w.fs.workers = append(w.fs.workers, workerFor(pr.RoundSessionID(), 700))
	w.h.now = func() time.Time { return t0.Add(5 * time.Minute) }
	_ = w.h.tick()
	if s, _ := w.h.peekState(pr); s.Kind != kindQuestion {
		t.Errorf("got %+v, want the question back", s)
	}
}

// Starting is never remembered: the click that owned it aborts on seeing
// the window, so the window keeps what the click replaced.
func TestAWindowOverStartingForgetsTheStarting(t *testing.T) {
	approve := PRState{State: stAwaiting, Kind: kindApprove, Head: "h1"}
	starting := PRState{State: stStarting, Kind: kindClick, Head: "h1", Prev: &approve}
	next, _, ok := transition(starting, Event{Kind: evWindowOpened}, t0)
	if !ok || next.Kind != kindWindow || next.Prev == nil || next.Prev.Kind != kindApprove {
		t.Errorf("got %+v", next)
	}
}

// The deepest chain the rules can build, walked over every event with
// sample payloads; a rule that deepens it fails here.
func TestTheChainDepthIsPinned(t *testing.T) {
	posted := PRState{State: stHandled, Kind: kindPosted}
	events := []Event{
		{Kind: evClick}, {Kind: evLaunched}, {Kind: evAbandoned}, {Kind: evFailedToStart, Class: launchNotStarted},
		{Kind: evOwed}, {Kind: evOwed, Trigger: Requested{At: t0}}, {Kind: evChangedUnderFirstLook}, {Kind: evPush, Head: "x"},
		{Kind: evBlocked, Question: "q"}, {Kind: evResumed}, {Kind: evEnded, Next: &posted},
		{Kind: evWindowOpened}, {Kind: evWindowClosed}, {Kind: evUserApproved}, {Kind: evLaunchCleared, Class: launchNotStarted},
	}
	// The same round events naming one of two rounds, so a round acts
	// below a window and an older round's outcome is dropped.
	for _, r := range []ids.RoundID{rid(1), rid(2)} {
		events = append(events, Event{Kind: evOwed, Round: r}, Event{Kind: evLaunched, Round: r},
			Event{Kind: evBlocked, Question: "q", Round: r}, Event{Kind: evResumed, Round: r}, Event{Kind: evEnded, Next: &posted, Round: r})
	}
	depth := func(s PRState) int {
		n := 0
		for e := &s; e != nil; e = e.Prev {
			n++
		}
		return n
	}
	deepest, deepestChain := 0, ""
	// seen is the most steps left a chain was walked with: walking it again
	// with no more finds nothing new.
	seen := map[string]int{}
	key := func(s PRState) string {
		var b strings.Builder
		for e := &s; e != nil; e = e.Prev {
			fmt.Fprintf(&b, "%s|%s|%s|%s|%s<", describe(*e), e.Detail, e.Head, e.Review, e.Round)
		}
		return b.String()
	}
	var walk func(s PRState, steps int)
	walk = func(s PRState, steps int) {
		k := key(s)
		if left, ok := seen[k]; ok && left >= steps {
			return
		}
		seen[k] = steps
		if d := depth(s); d > deepest {
			deepest = d
			var names []string
			for e := &s; e != nil; e = e.Prev {
				names = append(names, describe(*e))
			}
			deepestChain = strings.Join(names, " < ")
		}
		if steps == 0 {
			return
		}
		for _, e := range events {
			if next, _, ok := transition(s, e, t0); ok {
				walk(next, steps-1)
			}
		}
	}
	walk(PRState{State: stIdle}, 14)
	// 3 since a click cannot start under an open window.
	if deepest != 3 {
		t.Errorf("deepest chain %d, pinned 3: %s", deepest, deepestChain)
	}
}
