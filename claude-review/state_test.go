package main

import (
	"slices"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/chelmertz/dotfiles/claude-review/ids"
)

func st(s State, kind string) PRState { return PRState{State: s, Kind: kind, Head: "aaa"} }

// One row per line of the design's transition table.
func TestTransition(t *testing.T) {
	at := time.Date(2026, 11, 17, 10, 0, 0, 0, time.UTC)
	running := st(stRunning, kindTick)
	question := PRState{State: stAwaiting, Kind: kindQuestion, Head: "aaa", Detail: "why?", Prev: &running}
	window := PRState{State: stHeld, Kind: kindWindow, Head: "aaa", Prev: &question}
	idle := st(stIdle, "")
	starting := PRState{State: stStarting, Kind: kindClick, Head: "aaa", Prev: &question}
	posted := st(stHandled, kindPosted)
	retry := PRState{State: stRunning, Kind: kindRetry}
	stuck := st(stStuck, kindFailed)

	cases := []struct {
		name    string
		from    PRState
		ev      Event
		want    State
		kind    string
		effects []Effect
		ok      bool
	}{
		{"closed from anything", question, Event{Kind: evClosed}, "", "", []Effect{efStopWorkers, efRoundsRead}, true},
		{"user approved", question, Event{Kind: evUserApproved, Head: "bbb"}, stIdle, "", []Effect{efRevokeTokens}, true},
		{"user approved mid-round", running, Event{Kind: evUserApproved}, stRunning, kindTick, nil, false},
		{"tick launch failed", posted, Event{Kind: evFailedToStart, Class: launchNotStarted}, stHeld, kindLaunch, nil, true},
		{"read before a new round", running, Event{Kind: evEnded, Next: &PRState{State: stIdle}}, stIdle, "", []Effect{efRoundsRead}, true},
		{"click from idle", idle, Event{Kind: evClick, From: "elly"}, stStarting, kindClick, nil, true},
		{"click from stuck", stuck, Event{Kind: evClick}, stStarting, kindClick, nil, true},
		{"click while running", running, Event{Kind: evClick}, stRunning, kindTick, nil, false},
		{"click while starting", starting, Event{Kind: evClick}, stStarting, kindClick, nil, false},
		{"launched", starting, Event{Kind: evLaunched}, stRunning, kindClick, nil, true},
		{"abandoned restores prev", starting, Event{Kind: evAbandoned}, stAwaiting, kindQuestion, nil, true},
		{"launch failed", starting, Event{Kind: evFailedToStart, Class: launchNotStarted}, stHeld, kindLaunch, nil, true},
		{"owed from idle", idle, Event{Kind: evOwed, Head: "bbb"}, stRunning, kindTick, nil, true},
		{"owed from handled", posted, Event{Kind: evOwed}, stRunning, kindTick, nil, true},
		{"owed from launch failed", PRState{State: stHeld, Kind: kindLaunch, Detail: launchNotStarted}, Event{Kind: evOwed}, stRunning, kindTick, nil, true},
		{"owed under a first look", posted, Event{Kind: evChangedUnderFirstLook}, stHeld, kindFresh, nil, true},
		{"click looks again", st(stHeld, kindFresh), Event{Kind: evClick}, stStarting, kindClick, nil, true},
		{"owed while running", running, Event{Kind: evOwed}, stRunning, kindTick, nil, false},
		{"owed on fresh look", st(stHeld, kindFresh), Event{Kind: evOwed}, stHeld, kindFresh, nil, false},
		{"owed under a window", window, Event{Kind: evOwed}, stHeld, kindWindow, nil, false},
		{"a push does not lift stuck", stuck, Event{Kind: evOwed}, stStuck, kindFailed, nil, false},
		{"a re-request lifts stuck", stuck, Event{Kind: evOwed, Trigger: Requested{At: t0}}, stRunning, kindTick, nil, true},
		{"push drops a question", question, Event{Kind: evPush, Head: "bbb"}, stIdle, "", []Effect{efStopWorkers, efRevokeTokens}, true},
		{"push drops an approve", st(stAwaiting, kindApprove), Event{Kind: evPush, Head: "bbb"}, stIdle, "", []Effect{efStopWorkers, efRevokeTokens}, true},
		{"push while running", running, Event{Kind: evPush}, stRunning, kindTick, nil, false},
		{"blocked", running, Event{Kind: evBlocked, Question: "why?"}, stAwaiting, kindQuestion, nil, true},
		{"resumed", question, Event{Kind: evResumed}, stRunning, kindTick, nil, true},
		{"resumed without a worker", PRState{State: stAwaiting, Kind: kindQuestion}, Event{Kind: evResumed}, stAwaiting, kindQuestion, nil, false},
		{"ended", running, Event{Kind: evEnded, Next: &posted}, stHandled, kindPosted, []Effect{efRoundsRead}, true},
		{"ended while blocked", question, Event{Kind: evEnded, Next: &posted}, stHandled, kindPosted, []Effect{efRoundsRead}, true},
		{"ended needing retry", running, Event{Kind: evEnded, Next: &retry}, stRunning, kindRetry, nil, true},
		{"ended without a decision", running, Event{Kind: evEnded}, stRunning, kindTick, nil, false},
		{"ended when idle", idle, Event{Kind: evEnded, Next: &posted}, stHandled, kindPosted, []Effect{efRoundsRead}, true},
		{"ended when handled", posted, Event{Kind: evEnded, Next: &retry}, stHandled, kindPosted, nil, false},
		{"window opened", question, Event{Kind: evWindowOpened}, stHeld, kindWindow, []Effect{efRevokeTokens}, true},
		{"window opened twice", window, Event{Kind: evWindowOpened}, stHeld, kindWindow, nil, false},
		{"window closed restores prev", window, Event{Kind: evWindowClosed}, stAwaiting, kindQuestion, nil, true},
		{"window closed with no window", idle, Event{Kind: evWindowClosed}, stIdle, "", nil, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, effects, ok := transition(c.from, c.ev, at)
			if got.State != c.want || got.Kind != c.kind || ok != c.ok || !slices.Equal(effects, c.effects) {
				t.Fatalf("got %s{%s} %v ok=%v, want %s{%s} %v ok=%v", got.State, got.Kind, effects, ok, c.want, c.kind, c.effects, c.ok)
			}
		})
	}
}

func TestTransitionKeepsWhatItShould(t *testing.T) {
	at := time.Date(2026, 11, 17, 10, 0, 0, 0, time.UTC)
	running := PRState{State: stRunning, Kind: kindTick, Head: "aaa"}
	got, _, _ := transition(running, Event{Kind: evBlocked, Question: "why?"}, at)
	if got.Detail != "why?" || got.Head != "aaa" || !got.Since.Equal(at) || got.Prev == nil || got.Prev.State != stRunning {
		t.Fatalf("blocked: %+v", got)
	}
	got, _, _ = transition(got, Event{Kind: evPush, Head: "bbb"}, at)
	if got.Head != "bbb" || got.Detail != "" || got.Prev != nil {
		t.Fatalf("push: %+v", got)
	}
	got, _, _ = transition(PRState{State: stIdle}, Event{Kind: evClick, From: "thunderbird"}, at)
	if got.Detail != "thunderbird" || got.Prev == nil || got.Prev.State != stIdle {
		t.Fatalf("click: %+v", got)
	}
}

// Every state the model names can be reached from idle through the rules.
func TestEveryStateIsReachable(t *testing.T) {
	reached := map[stateKey]bool{{stIdle, ""}: true}
	for changed := true; changed; {
		changed = false
		for _, r := range rules {
			for k := range reached {
				if !r.from.matches(k.State, k.Kind) {
					continue
				}
				for _, to := range r.targets() {
					if !reached[to] {
						reached[to], changed = true, true
					}
				}
			}
		}
	}
	for _, k := range allStates {
		if !reached[k] {
			t.Errorf("%s{%s} is unreachable from idle", k.State, k.Kind)
		}
	}
}

// A state that needs the user has exactly one primary link; others have none,
// and every note fits elly.
func TestRenderPrimaryLinks(t *testing.T) {
	pr := PR{Owner: "o", Repo: "r", Number: 1}
	at := time.Date(2026, 11, 17, 10, 0, 0, 0, time.UTC)
	for _, k := range allStates {
		if k.State == stIdle {
			if a := render(pr, PRState{State: k.State}, View{}); a.State != "" || a.Links != nil {
				t.Errorf("%s has a row: %+v", k.State, a)
			}
			continue
		}
		s := PRState{State: k.State, Kind: k.Kind, Head: strings.Repeat("a", 40), Detail: strings.Repeat("x", 400), Review: "https://github.com/o/r/pull/1#r", Since: at}
		a := render(pr, s, View{Token: ids.Token{}})
		primaries := 0
		for _, l := range a.Links {
			if l.Primary {
				primaries++
			}
		}
		want := 0
		if needsUser(a.State) {
			want = 1
		}
		if primaries != want {
			t.Errorf("%s{%s}: %d primary links on a %s row, want %d", k.State, k.Kind, primaries, a.State, want)
		}
		if a.Note == "" || utf8.RuneCountInString(a.Note) > ellyMaxNote {
			t.Errorf("%s{%s}: note %q", k.State, k.Kind, a.Note)
		}
	}
}

// A held-back owed round shows as paused on a row that would otherwise not
// need the user (idle, handled), and never replaces any other: an Approve
// stays visible under the daily cap. Every state crossed with every hold.
func TestRenderPaused(t *testing.T) {
	pr := PR{Owner: "o", Repo: "r", Number: 1}
	holds := []Hold{holdFor(true, ""), holdFor(false, breakerDuplicate+"x"), holdFor(true, breakerCap+"x"), holdFor(false, "old text")}
	for _, k := range allStates {
		s := PRState{State: k.State, Kind: k.Kind, Head: "h1"}
		bare := render(pr, s, View{})
		for _, h := range holds {
			a := render(pr, s, View{Hold: h})
			if k.State == stIdle || k.State == stHandled {
				if a.State != stateNeedsYou || a.Note != h.note() || len(a.Links) != 2 || !a.Links[0].Primary || a.Links[1] != statesLink(pr) {
					t.Errorf("%s{%s} under %+v: %+v", k.State, k.Kind, h, a)
				}
				continue
			}
			if a.Note != bare.Note || a.State != bare.State {
				t.Errorf("%s{%s}: a hold replaced its row: %+v", k.State, k.Kind, a)
			}
		}
	}
}

// A rule may list only effects move performs: a listed effect nobody runs
// reads as done in the table and the diagram, and is not.
func TestRulesListOnlyPerformedEffects(t *testing.T) {
	w := newTickWorld(t)
	for i, r := range rules {
		for _, ef := range r.effects {
			if !slices.Contains(performedEffects, ef) {
				t.Errorf("rule %d (%s) lists %q, which move does not perform", i, r.event, ef)
			}
		}
	}
	for _, ef := range performedEffects {
		if err := w.h.effect(widgetsPR, ef); err != nil {
			t.Errorf("effect %q: %v", ef, err)
		}
	}
	if err := w.h.effect(widgetsPR, Effect("launch")); err == nil {
		t.Error("an effect move does not know was accepted")
	}
}

// A launch failure stores a class, never the error that caused it: a
// reason no class names moves nothing, and each target keeps only the
// payload field its state owns.
func TestEventPayloadsLandOnlyWhereTheyMeanSomething(t *testing.T) {
	posted := PRState{State: stHandled, Kind: kindPosted, Head: "h1"}
	if next, _, ok := transition(posted, Event{Kind: evFailedToStart, Class: "exec: claude: token=abc"}, t0); ok {
		t.Errorf("free text reached a state: %+v", next)
	}
	next, _, _ := transition(posted, Event{Kind: evFailedToStart, Class: launchDuplicate, From: "elly", Question: "q"}, t0)
	if next.Detail != launchDuplicate {
		t.Errorf("held launch stored %q", next.Detail)
	}
	next, _, _ = transition(PRState{State: stIdle}, Event{Kind: evClick, From: "elly", Class: launchDuplicate}, t0)
	if next.Detail != "elly" {
		t.Errorf("starting stored %q", next.Detail)
	}
}

// Tokens follow the chain: a move that leaves no Approve at the head
// spends the PR's tokens, so an earlier tab's Approve cannot approve while
// a new review of the same commit runs.
func TestAMoveLeavingNoApproveRevokesTokens(t *testing.T) {
	approve := PRState{State: stAwaiting, Kind: kindApprove, Head: "h1"}
	revokes := func(effects []Effect) bool { return slices.Contains(effects, efRevokeTokens) }
	for name, e := range map[string]Event{
		"re-request":    {Kind: evOwed, Head: "h1", Trigger: Requested{At: t0}},
		"first look":    {Kind: evChangedUnderFirstLook, Head: "h1"},
		"failed launch": {Kind: evFailedToStart, Class: launchNotStarted},
		"closed":        {Kind: evClosed},
	} {
		if _, effects, ok := transition(approve, e, t0); !ok || !revokes(effects) {
			t.Errorf("%s from awaiting{approve}: effects %v ok %v, want tokens revoked", name, effects, ok)
		}
	}
	// A click keeps the Approve under its starting: abandoned, it comes back.
	if _, effects, _ := transition(approve, Event{Kind: evClick}, t0); revokes(effects) {
		t.Error("a click over an Approve spent its token")
	}
	if _, effects, _ := transition(approve, Event{Kind: evUserApproved, Head: "h1"}, t0); len(slices.DeleteFunc(slices.Clone(effects), func(e Effect) bool { return e != efRevokeTokens })) != 1 {
		t.Errorf("revoke listed twice: %v", effects)
	}
	before := slices.Clone(rules[0].effects)
	transition(approve, Event{Kind: evClosed}, t0)
	if !slices.Equal(rules[0].effects, before) {
		t.Error("the rule table's effects were changed in place")
	}
}
