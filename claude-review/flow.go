package main

import (
	"errors"
	"fmt"
)

// The tick host's side of the state machine: every change to a PR's state
// goes through move, and every row elly shows is render's, sent by show.

// stateOf is pr's stored state; a PR without one is idle.
func (h tickHost) stateOf(pr PR) (PRState, error) {
	s, ok, err := h.ledger.PRState(pr)
	if !ok && err == nil {
		s = PRState{State: stIdle}
	}
	return s, err
}

// move applies e to pr's state. It runs the transition's effects, stores the
// new state and shows its row; ok is false when no rule applies, and then
// nothing changes. Launch and re-accept are the caller's, done before the
// event that reports them.
func (h tickHost) move(pr PR, e Event) (PRState, error) {
	s, err := h.stateOf(pr)
	if err != nil {
		return s, err
	}
	next, effects, ok := transition(s, e, h.now())
	if !ok {
		return s, nil
	}
	err = h.do(fmt.Sprintf("move %s from %s to %s on %s", pr.URL(), describe(s), describe(next), e.Kind), func() error {
		for _, ef := range effects {
			if err := h.effect(pr, ef); err != nil {
				return err
			}
		}
		if next.State == stGone {
			return errors.Join(h.ledger.DeletePRState(pr), h.clear(pr))
		}
		if err := h.mintFor(pr, next); err != nil {
			return err
		}
		if err := h.ledger.SetPRState(pr, next); err != nil {
			return err
		}
		// The state is stored; elly refusing the row is said, never the
		// move's failure, and the next refresh sends it again.
		h.warn(pr, h.show(pr, next, ""))
		return nil
	})
	return next, err
}

// mintFor mints the Approve link's token when s is the first state to need
// one at its head.
func (h tickHost) mintFor(pr PR, s PRState) error {
	if s.State != stAwaiting || s.Kind != kindApprove {
		return nil
	}
	if dismissing, err := h.ledger.DismissalPending(pr); err != nil || dismissing {
		return err
	}
	if _, has, err := h.ledger.TokenFor(pr, s.Head); err != nil || has {
		return err
	}
	_, err := h.ledger.MintToken(pr, s.Head)
	return err
}

// performedEffects are the effects effect runs; a rule lists no other.
var performedEffects = []Effect{efStopWorkers, efRevokeTokens, efRoundsRead}

func (h tickHost) effect(pr PR, ef Effect) error {
	switch ef {
	case efStopWorkers:
		if h.sess.Roster == nil {
			return nil // a click's host: it never stops workers
		}
		all, err := h.sess.Roster()
		if err != nil {
			return err
		}
		for _, w := range h.workersOf(pr, all) {
			if err := h.sess.stopAndWait(w); err != nil {
				return err
			}
		}
	case efRevokeTokens:
		return h.ledger.RevokeTokens(pr)
	case efRoundsRead:
		return h.ledger.MarkPRRead(pr, h.now())
	default:
		return fmt.Errorf("effect %s is not one move performs", ef)
	}
	return nil
}

// show sends pr's row for s to elly, paused naming why an owed round is held
// back this tick. A state without a row deletes it.
func (h tickHost) show(pr PR, s PRState, paused string) error {
	v := View{Paused: paused}
	dismissing, err := h.ledger.DismissalPending(pr)
	if err != nil {
		return err
	}
	v.Dismissing = dismissing
	if s.State == stAwaiting && s.Kind == kindApprove {
		// A row whose tokens were all spent (revokePreC7Tokens) gets a
		// fresh one rather than a link nothing accepts.
		tok, has, err := h.ledger.TokenFor(pr, s.Head)
		if err == nil && !has && !dismissing {
			tok, err = h.ledger.MintToken(pr, s.Head)
		}
		if err != nil {
			return err
		}
		v.Token = tok
	}
	a := render(pr, s, v)
	if a.State == "" {
		if _, had, err := h.ledger.Annotation(pr); err != nil || !had {
			return err
		}
		return h.clear(pr)
	}
	expires, send := h.refreshedExpiry(a)
	if expires.IsZero() {
		return h.clear(pr) // a running row nothing settled in its lifetime
	}
	if err := h.setRow(pr, a); err != nil || h.elly == nil {
		return err
	}
	if !send {
		return nil
	}
	return h.elly.Put(pr.URL(), a, expires)
}

// warn reports err about pr without failing what it happened in: to the
// journal, or through the click's own channel.
func (h tickHost) warn(pr PR, err error) {
	switch {
	case err == nil:
	case h.say != nil:
		h.say(err)
	case h.out != nil:
		fmt.Fprintf(h.out, "claude-review tick: %s: %v\n", pr.URL(), err)
	}
}

func describe(s PRState) string {
	if s.Kind == "" {
		return string(s.State)
	}
	return string(s.State) + "{" + s.Kind + "}"
}
