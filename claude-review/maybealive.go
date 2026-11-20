package main

import (
	"errors"
	"fmt"
)

// recheckLaunches moves every launch whose worker may have been alive to
// "nothing started" once the roster lists no worker for its PR and no round
// of the PR is unread. A roster that cannot be read leaves them all.
func (h tickHost) recheckLaunches() error {
	states, err := h.ledger.PRStates()
	if err != nil {
		return err
	}
	var pending []PR
	for url, s := range states {
		if !maybeAlive(s) {
			continue
		}
		if pr, err := ParseTarget(url); err == nil {
			pending = append(pending, pr)
		}
	}
	if len(pending) == 0 {
		return nil
	}
	all, err := h.sess.Roster()
	if err != nil {
		fmt.Fprintf(h.out, "claude-review tick: unclear launches stay unclear: %v\n", err)
		return nil
	}
	rounds, err := h.ledger.UnreadRounds()
	if err != nil {
		return err
	}
	unread := map[string]bool{}
	for _, r := range rounds {
		unread[r.PR] = true
	}
	for _, pr := range pending {
		if unread[pr.URL()] || len(h.workersOf(pr, all)) > 0 {
			continue
		}
		unlock, err := h.lock(pr.SessionID().String())
		if errors.Is(err, errLocked) {
			continue
		}
		if err != nil {
			return err
		}
		_, err = h.move(pr, Event{Kind: evLaunchCleared, Detail: launchNotStarted})
		unlock()
		if err != nil {
			h.warn(pr, err)
		}
	}
	return nil
}
