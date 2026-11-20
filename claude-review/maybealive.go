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

// recoverStarting resolves a "starting" a click left behind (the click
// process died between its row and its launch) once it is older than
// startingLifetime, under the PR's lock; a live click holds that lock until
// it has launched, so a locked PR is skipped. A round recorded since the
// click means it launched; a worker with no round may be its launch; an
// unreadable roster leaves that open; nothing at all puts back what the
// click replaced.
func (h tickHost) recoverStarting() error {
	states, err := h.ledger.PRStates()
	if err != nil {
		return err
	}
	for url, s := range states {
		if s.State != stStarting || h.now().Sub(s.Since) < startingLifetime {
			continue
		}
		pr, err := ParseTarget(url)
		if err != nil {
			continue
		}
		unlock, err := h.lock(pr.SessionID().String())
		if errors.Is(err, errLocked) {
			continue
		}
		if err != nil {
			return err
		}
		e, err := h.startingOutcome(pr, s)
		if err == nil {
			_, err = h.move(pr, e)
		}
		unlock()
		if err != nil {
			h.warn(pr, err)
		}
	}
	return nil
}

func (h tickHost) startingOutcome(pr PR, s PRState) (Event, error) {
	rounds, err := h.ledger.UnreadRounds()
	if err != nil {
		return Event{}, err
	}
	for _, r := range rounds {
		if r.PR == pr.URL() && !r.StartedAt.Before(s.Since) {
			return Event{Kind: evLaunched}, nil
		}
	}
	for e := s.Prev; e != nil; e = e.Prev {
		if e.State == stAwaiting && e.Kind == kindQuestion {
			// A worker now is the question's own, waiting on the user.
			return Event{Kind: evNotStarted}, nil
		}
	}
	all, err := h.sess.Roster()
	if err != nil {
		// Unknown is an outcome: the roster re-check resolves it later.
		return Event{Kind: evNotStarted, Detail: launchUnknown}, nil //nolint:nilerr // see above
	}
	if len(h.workersOf(pr, all)) > 0 {
		return Event{Kind: evNotStarted, Detail: launchUnlisted}, nil
	}
	return Event{Kind: evNotStarted}, nil
}
