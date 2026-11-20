package main

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"time"
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
	s, _, err := h.moved(pr, e)
	return s, err
}

// moved is move, and whether a rule applied.
func (h tickHost) moved(pr PR, e Event) (PRState, bool, error) {
	s, err := h.stateOf(pr)
	if err != nil {
		return s, false, err
	}
	next, effects, ok := transition(s, e, h.now())
	if !ok {
		return s, false, nil
	}
	err = h.do(fmt.Sprintf("move %s from %s to %s on %s", pr.URL(), describe(s), describe(next), e.Kind), func() error {
		for _, ef := range effects {
			if err := h.effect(pr, ef); err != nil {
				return err
			}
		}
		if deleted(next) {
			if err := h.ledger.DeletePRState(pr); err != nil {
				return err
			}
			// A row elly does not drop now keeps its receipt, which the
			// stale-row walk sends the DELETE for again next tick.
			h.warn(pr, h.clear(pr))
			return nil
		}
		if err := h.mintFor(pr, next); err != nil {
			return err
		}
		if err := h.ledger.SetPRState(pr, next); err != nil {
			return err
		}
		// The state is stored; elly refusing the row is said, never the
		// move's failure, and the next refresh sends it again.
		h.warn(pr, h.show(pr, next, Hold{}))
		return nil
	})
	return next, true, err
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
			return errors.New("this host cannot list workers, so it cannot stop them")
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

// show sends pr's row for s to elly, hold naming why an owed round waits
// this pass. A state without a row deletes it.
func (h tickHost) show(pr PR, s PRState, hold Hold) error {
	v, err := h.viewFor(pr, s, hold)
	if err != nil {
		return err
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
	if h.elly != nil && send {
		if err := h.put(pr, a, expires); err != nil {
			return err
		}
	}
	return h.setRow(pr, a, notifyIdentity(pr, s, a))
}

// notifyIdentity is what a notification is about: the state and a hash of
// its row's note. A window keeps the identity of the needs-the-user state
// it stands over, so opening and closing it on an unanswered question
// notifies nothing new.
func notifyIdentity(pr PR, s PRState, a Annotation) string {
	if s.State == stHeld && s.Kind == kindWindow {
		under := s.Prev
		for under != nil && under.State == stHeld && under.Kind == kindWindow {
			under = under.Prev
		}
		if under != nil {
			if ua := renderRow(pr, *under, View{}); needsUser(ua.State) {
				return notifyIdentity(pr, *under, ua)
			}
		}
	}
	return describe(s) + " " + hashOf(a.Note)
}

// viewFor is what render needs beyond s: the hold, whether a dismissal is
// pending, and for an Approve its token. A row whose tokens were all spent
// (revokePreC7Tokens) gets a fresh one rather than a link nothing accepts.
func (h tickHost) viewFor(pr PR, s PRState, hold Hold) (View, error) {
	v := View{Hold: hold}
	dismissing, err := h.ledger.DismissalPending(pr)
	if err != nil {
		return v, err
	}
	v.Dismissing = dismissing
	if s.State != stAwaiting || s.Kind != kindApprove {
		return v, nil
	}
	tok, has, err := h.ledger.TokenFor(pr, s.Head)
	if err == nil && !has && !dismissing {
		tok, err = h.ledger.MintToken(pr, s.Head)
	}
	v.Token = tok
	return v, err
}

// put sends a to elly. Nothing is recorded or notified for a row elly did
// not take: a PR it does not list yet gets it once refresh sees it there,
// and a failure is sent again next pass.
func (h tickHost) put(pr PR, a Annotation, expires time.Time) error {
	if err := h.elly.Put(pr.URL(), a, expires); err != nil {
		if refusedWith(err) == http.StatusBadRequest {
			err = errors.Join(err, h.hidden(pr, a))
		}
		return err
	}
	return h.ledger.DeleteMeta(metaHiddenPrefix + pr.URL())
}

// hidden handles a row elly refused as invalid, a bug in claude-review: the
// row it last accepted is taken down, since it no longer says what is
// true, and the user hears once per refused row that one is hidden.
func (h tickHost) hidden(pr PR, a Annotation) error {
	if _, had, err := h.ledger.Annotation(pr); err != nil || had {
		if err := errors.Join(err, h.clear(pr)); err != nil {
			return err
		}
	}
	key, id := metaHiddenPrefix+pr.URL(), a.State+" "+hashOf(a.Note)
	if seen, err := h.ledger.Meta(key); err != nil || seen == id {
		return err
	}
	if h.notify != nil && needsUser(a.State) {
		h.notify(pr.URL() + ": a row that needs you is hidden; elly refused it")
	}
	return h.ledger.SetMeta(key, id)
}

// metaHiddenPrefix keys the last row of a PR elly refused, so its notice is
// given once.
const metaHiddenPrefix = "row-hidden "

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

// hashOf names text by a short hash, for keys and logs that must not hold
// it.
func hashOf(text string) string {
	sum := sha256.Sum256([]byte(text))
	return hex.EncodeToString(sum[:8])
}
