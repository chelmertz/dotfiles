package main

import (
	"fmt"
	"testing"
)

// stack builds a chain top first: each state's Prev is the next one.
func stack(states ...PRState) PRState {
	for i := len(states) - 2; i >= 0; i-- {
		below := states[i+1]
		states[i].Prev = &below
	}
	return states[0]
}

// TestChainYieldsDepthAndOwner pins the iterator: every element top first,
// its depth, and whether it owns a worker, where the running state a
// question remembers is the question's own worker and owns none.
func TestChainYieldsDepthAndOwner(t *testing.T) {
	running := PRState{State: stRunning, Kind: kindTick, Round: rid(1)}
	question := PRState{State: stAwaiting, Kind: kindQuestion, Round: rid(1)}
	window := PRState{State: stHeld, Kind: kindWindow}
	alive := PRState{State: stHeld, Kind: kindLaunch, Detail: launchUnlisted}
	idle := PRState{State: stIdle}
	approve := PRState{State: stAwaiting, Kind: kindApprove}
	type step struct {
		key   stateKey
		owner bool
	}
	cases := []struct {
		name  string
		s     PRState
		wants []step
	}{
		{"idle alone", idle, []step{{stateKey{stIdle, ""}, false}}},
		{"running alone owns", running, []step{{stateKey{stRunning, kindTick}, true}}},
		{"running under a question is the question's", stack(question, running), []step{
			{stateKey{stAwaiting, kindQuestion}, true}, {stateKey{stRunning, kindTick}, false},
		}},
		{"a window over a question over running", stack(window, question, running), []step{
			{stateKey{stHeld, kindWindow}, false}, {stateKey{stAwaiting, kindQuestion}, true}, {stateKey{stRunning, kindTick}, false},
		}},
		{"running under a window owns", stack(window, running), []step{
			{stateKey{stHeld, kindWindow}, false}, {stateKey{stRunning, kindTick}, true},
		}},
		{"a maybe-alive launch owns, also under a question", stack(question, alive, approve), []step{
			{stateKey{stAwaiting, kindQuestion}, true}, {stateKey{stHeld, kindLaunch}, true}, {stateKey{stAwaiting, kindApprove}, false},
		}},
		{"the rule reaches one element only", stack(question, idle, running), []step{
			{stateKey{stAwaiting, kindQuestion}, true}, {stateKey{stIdle, ""}, false}, {stateKey{stRunning, kindTick}, true},
		}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var got []step
			want := 0
			for d, l := range chain(c.s) {
				if d != want {
					t.Errorf("depth %d, want %d", d, want)
				}
				want++
				got = append(got, step{stateKey{l.State, l.Kind}, l.Owner})
			}
			if fmt.Sprint(got) != fmt.Sprint(c.wants) {
				t.Errorf("chain = %v, want %v", got, c.wants)
			}
		})
	}
}

// TestChainStopsWhenTheLoopBreaks: a break ends the walk, as range-over-func
// requires (yield must not be called again after it returns false).
func TestChainStopsWhenTheLoopBreaks(t *testing.T) {
	n := 0
	for range chain(stack(PRState{State: stIdle}, PRState{State: stIdle}, PRState{State: stIdle})) {
		n++
		break
	}
	if n != 1 {
		t.Errorf("visited %d after a break, want 1", n)
	}
}
