package main

import (
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/chelmertz/dotfiles/claude-review/ids"
)

// c8Events are every event kind with sample payloads: two heads, two
// rounds, each decided state, each launch class.
func c8Events() []Event {
	var out []Event
	for _, head := range []string{"h1", "h2"} {
		out = append(out,
			Event{Kind: evClosed}, Event{Kind: evUserApproved, Head: head},
			Event{Kind: evClick, From: "elly"}, Event{Kind: evAbandoned, Head: head},
			Event{Kind: evOwed, Head: head}, Event{Kind: evOwed, Head: head, Trigger: Requested{At: t0}},
			Event{Kind: evChangedUnderFirstLook, Head: head}, Event{Kind: evPush, Head: head},
			Event{Kind: evWindowOpened}, Event{Kind: evWindowClosed, Head: head},
			Event{Kind: evLaunchCleared, Class: launchNotStarted})
		for _, class := range []string{launchNotStarted, launchUnlisted, launchUnknown, launchDuplicate} {
			out = append(out, Event{Kind: evFailedToStart, Class: class})
		}
		for _, r := range []int{0, 1, 2} {
			out = append(out, Event{Kind: evLaunched, Round: rid(max(r, 1))},
				Event{Kind: evBlocked, Question: "q", Head: head, Round: ridOrZero(r)},
				Event{Kind: evResumed, Round: ridOrZero(r)})
			for _, k := range decidable {
				next := PRState{State: k.State, Kind: k.Kind, Head: head, Detail: c8Detail(k)}
				out = append(out, Event{Kind: evEnded, Next: &next, Head: head, Round: ridOrZero(r)})
			}
		}
	}
	return out
}

func ridOrZero(n int) ids.RoundID {
	if n == 0 {
		return ids.RoundID{}
	}
	return rid(n)
}

func c8Detail(k stateKey) string {
	switch k {
	case stateKey{stAwaiting, kindQuestion}:
		return "q"
	case stateKey{stAwaiting, kindDrafts}:
		return "2"
	case stateKey{stHeld, kindLaunch}:
		return launchNotStarted
	}
	return ""
}

// c8Chains is every chain the rules reach from idle and from the
// converter's reading of every stored pair, each at either head.
func c8Chains(t *testing.T) []PRState {
	t.Helper()
	events := c8Events()
	seen := map[string]bool{}
	var out, queue []PRState
	add := func(s PRState) {
		if k := c8Key(s); !seen[k] {
			seen[k] = true
			out, queue = append(out, s), append(queue, s)
		}
	}
	for _, k := range allStates {
		for _, head := range []string{"h1", "h2"} {
			add(normalized(PRState{State: k.State, Kind: k.Kind, Head: head, Detail: c8Detail(k)}))
		}
	}
	for len(queue) > 0 {
		s := queue[0]
		queue = queue[1:]
		for _, e := range events {
			if next, _, ok := transition(s, e, t0); ok && !deleted(next) {
				add(next)
			}
		}
		if len(out) > 20000 {
			t.Fatalf("over 20000 chains: the rules grow chains without bound")
		}
	}
	return out
}

func c8Key(s PRState) string {
	var b strings.Builder
	for e := &s; e != nil; e = e.Prev {
		fmt.Fprintf(&b, "%s|%s|%s|%s<", describe(*e), e.Detail, e.Head, e.Round)
	}
	return b.String()
}

func chainOf(s PRState) []PRState {
	var out []PRState
	for e := &s; e != nil; e = e.Prev {
		out = append(out, *e)
	}
	return out
}

// worker-owning or user-held elements a transition may not silently drop.
func keeps(e PRState) bool {
	return e.State == stRunning || e.State == stHeld && e.Kind == kindWindow ||
		e.State == stAwaiting && e.Kind == kindQuestion || maybeAlive(e)
}

// The design's invariants, over every reachable chain and every event.
func TestInvariantsOverEveryReachableChain(t *testing.T) {
	chains := c8Chains(t)
	t.Logf("%d reachable chains", len(chains))
	for _, s := range chains {
		for i, e := range chainOf(s) {
			if i > 0 && e.State == stStarting {
				t.Errorf("a chain remembers a starting: %s", c8Key(s))
			}
		}
		for _, e := range c8Events() {
			next, effects, ok := transition(s, e, t0)
			if !ok {
				continue
			}
			window := windowInChain(s)
			// Closing the PR stops everything; a window closing at a moved
			// head stops the worker its restore drops (the window is gone).
			if window && slices.Contains(effects, efStopWorkers) && e.Kind != evClosed && e.Kind != evWindowClosed {
				t.Errorf("%s stops workers under an open window: %s", e.Kind, c8Key(s))
			}
			chain := chainOf(s)
			for i, el := range chain {
				// The running state a question remembers is the question's
				// own worker and goes with it.
				owner := el
				if i > 0 && el.State == stRunning && chain[i-1].State == stAwaiting && chain[i-1].Kind == kindQuestion {
					owner = chain[i-1]
				}
				if !keeps(el) || slices.Contains(effects, efStopWorkers) || workerShown(e, owner) {
					continue
				}
				if !deleted(next) && slices.ContainsFunc(chainOf(next), func(n PRState) bool { return sameElement(n, el) }) {
					continue
				}
				t.Errorf("%s drops %s without stopping its worker: %s -> %s", e.Kind, describe(el), c8Key(s), c8Key(next))
			}
		}
	}
}

// workerShown: the event shows the element's worker or window is done with,
// or is the launch that replaces it (a launch over a question stops the
// question's worker before it runs: TestALaunchReplacesAQuestionsWorker).
func workerShown(e Event, el PRState) bool {
	switch e.Kind {
	case evEnded, evBlocked, evResumed:
		return el.State != stHeld || el.Kind != kindWindow
	case evWindowClosed:
		return el.State == stHeld && el.Kind == kindWindow
	case evOwed, evClick:
		return el.State == stAwaiting && el.Kind == kindQuestion
	case evLaunched:
		// A launch happens only once Relaunch has stopped the session's
		// worker (a busy one gets no launch): the question's or a running.
		return el.State == stAwaiting && el.Kind == kindQuestion || el.State == stRunning
	case evLaunchCleared:
		return maybeAlive(el)
	default:
		return false
	}
}

func sameElement(a, b PRState) bool {
	return a.State == b.State && a.Kind == b.Kind && a.Round == b.Round && a.Head == b.Head
}
