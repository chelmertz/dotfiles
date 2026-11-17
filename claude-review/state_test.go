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
		{"closed from anything", question, Event{Kind: evClosed}, stGone, "", []Effect{efStopWorkers, efRoundsRead, efDelete}, true},
		{"user approved", question, Event{Kind: evUserApproved, Head: "bbb"}, stIdle, "", []Effect{efRevokeTokens}, true},
		{"user approved mid-round", running, Event{Kind: evUserApproved}, stRunning, kindTick, nil, false},
		{"tick launch failed", posted, Event{Kind: evNotStarted, Detail: "no clone"}, stHeld, kindLaunch, nil, true},
		{"read before a new round", running, Event{Kind: evEnded, Next: &PRState{State: stIdle}}, stIdle, "", []Effect{efRoundsRead}, true},
		{"click from idle", idle, Event{Kind: evClick, Detail: "elly"}, stStarting, kindClick, []Effect{efReaccept, efLaunch}, true},
		{"click from stuck", stuck, Event{Kind: evClick}, stStarting, kindClick, []Effect{efReaccept, efLaunch}, true},
		{"click while running", running, Event{Kind: evClick}, stRunning, kindTick, nil, false},
		{"click while starting", starting, Event{Kind: evClick}, stStarting, kindClick, nil, false},
		{"launched", starting, Event{Kind: evLaunched}, stRunning, kindClick, nil, true},
		{"not started restores prev", starting, Event{Kind: evNotStarted}, stAwaiting, kindQuestion, nil, true},
		{"launch failed", starting, Event{Kind: evNotStarted, Detail: "no clone"}, stHeld, kindLaunch, nil, true},
		{"owed from idle", idle, Event{Kind: evOwed, Head: "bbb"}, stRunning, kindTick, []Effect{efLaunch}, true},
		{"owed from handled", posted, Event{Kind: evOwed}, stRunning, kindTick, []Effect{efLaunch}, true},
		{"owed from launch failed", st(stHeld, kindLaunch), Event{Kind: evOwed}, stRunning, kindTick, []Effect{efLaunch}, true},
		{"owed under a first look", posted, Event{Kind: evOwed, FreshLook: true}, stHeld, kindFresh, nil, true},
		{"click looks again", st(stHeld, kindFresh), Event{Kind: evClick}, stStarting, kindClick, []Effect{efReaccept, efLaunch}, true},
		{"owed while running", running, Event{Kind: evOwed}, stRunning, kindTick, nil, false},
		{"owed on fresh look", st(stHeld, kindFresh), Event{Kind: evOwed}, stHeld, kindFresh, nil, false},
		{"owed under a window", window, Event{Kind: evOwed}, stHeld, kindWindow, nil, false},
		{"a push does not lift stuck", stuck, Event{Kind: evOwed}, stStuck, kindFailed, nil, false},
		{"a re-request lifts stuck", stuck, Event{Kind: evOwed, Rerequest: true}, stRunning, kindTick, []Effect{efLaunch}, true},
		{"push drops a question", question, Event{Kind: evPush, Head: "bbb"}, stIdle, "", []Effect{efStopWorkers, efRevokeTokens}, true},
		{"push drops an approve", st(stAwaiting, kindApprove), Event{Kind: evPush, Head: "bbb"}, stIdle, "", []Effect{efStopWorkers, efRevokeTokens}, true},
		{"push while running", running, Event{Kind: evPush}, stRunning, kindTick, nil, false},
		{"blocked", running, Event{Kind: evBlocked, Detail: "why?"}, stAwaiting, kindQuestion, nil, true},
		{"resumed", question, Event{Kind: evResumed}, stRunning, kindTick, nil, true},
		{"resumed without a worker", PRState{State: stAwaiting, Kind: kindQuestion}, Event{Kind: evResumed}, stAwaiting, kindQuestion, nil, false},
		{"ended", running, Event{Kind: evEnded, Next: &posted}, stHandled, kindPosted, []Effect{efRoundsRead}, true},
		{"ended while blocked", question, Event{Kind: evEnded, Next: &posted}, stHandled, kindPosted, []Effect{efRoundsRead}, true},
		{"ended needing retry", running, Event{Kind: evEnded, Next: &retry}, stRunning, kindRetry, []Effect{efLaunch}, true},
		{"ended without a decision", running, Event{Kind: evEnded}, stRunning, kindTick, nil, false},
		{"ended when idle", idle, Event{Kind: evEnded, Next: &posted}, stHandled, kindPosted, []Effect{efRoundsRead}, true},
		{"ended when handled", posted, Event{Kind: evEnded, Next: &retry}, stHandled, kindPosted, nil, false},
		{"answered", question, Event{Kind: evAnswered}, stIdle, "", nil, true},
		{"answered without a question", st(stAwaiting, kindApprove), Event{Kind: evAnswered}, stAwaiting, kindApprove, nil, false},
		{"window opened", question, Event{Kind: evWindowOpened}, stHeld, kindWindow, nil, true},
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
	got, _, _ := transition(running, Event{Kind: evBlocked, Detail: "why?"}, at)
	if got.Detail != "why?" || got.Head != "aaa" || !got.Since.Equal(at) || got.Prev == nil || got.Prev.State != stRunning {
		t.Fatalf("blocked: %+v", got)
	}
	got, _, _ = transition(got, Event{Kind: evPush, Head: "bbb"}, at)
	if got.Head != "bbb" || got.Detail != "" || got.Prev != nil {
		t.Fatalf("push: %+v", got)
	}
	got, _, _ = transition(PRState{State: stIdle}, Event{Kind: evClick, Detail: "thunderbird"}, at)
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
		if k.State == stGone || k.State == stIdle {
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
// need the user, and never hides one that does.
func TestRenderPaused(t *testing.T) {
	pr := PR{Owner: "o", Repo: "r", Number: 1}
	v := View{Paused: "paused: the daily round limit for this PR is reached"}
	for _, s := range []PRState{{State: stIdle}, {State: stHandled, Kind: kindPosted}} {
		a := render(pr, s, v)
		if a.State != stateNeedsYou || a.Note != v.Paused || len(a.Links) != 1 || !a.Links[0].Primary {
			t.Errorf("%s: %+v", s.State, a)
		}
	}
	if a := render(pr, PRState{State: stAwaiting, Kind: kindQuestion}, v); a.Note == v.Paused {
		t.Errorf("paused hid a question: %+v", a)
	}
}
