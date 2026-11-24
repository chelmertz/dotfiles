package main

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"time"

	"github.com/chelmertz/dotfiles/claude-review/prlock"
)

// The tick host's side of the state machine: every change to a PR's state
// goes through move, and every row elly shows is render's, sent by show.

// prKey names pr's lock: its SessionID, whichever session it guards.
func prKey(pr PR) string { return pr.SessionID().String() }

// lockPR takes pr's lock through take; errLocked means a click (or the
// attach verb, or tick) holds it.
func lockPR(pr PR, take prlock.Taker) (prlock.Locked, error) {
	return prlock.Take(prKey(pr), take)
}

// stateOf is pr's stored state, read under its lock; a PR without one is
// idle.
func (h tickHost) stateOf(lk prlock.Locked, pr PR) (PRState, error) {
	if err := lk.Check(prKey(pr)); err != nil {
		return PRState{}, err
	}
	return h.peekState(pr)
}

// peekState is pr's stored state read without its lock, for planning
// only: whatever acts on it reads it again under the lock (stateOf).
func (h tickHost) peekState(pr PR) (PRState, error) {
	s, ok, err := h.ledger.PRState(pr)
	if !ok && err == nil {
		s = PRState{State: stIdle}
	}
	return s, err
}

// move applies e to pr's state: one step, committed whole (Commit), its
// row shown after. ok is false when no rule applies, and then nothing
// changes. Launch and re-accept are the caller's, done before the event
// that reports them.
func (h tickHost) move(lk prlock.Locked, pr PR, e Event) (PRState, error) {
	s, _, err := h.moved(lk, pr, e)
	return s, err
}

// moved is move, and whether a rule applied.
func (h tickHost) moved(lk prlock.Locked, pr PR, e Event) (PRState, bool, error) {
	return h.commit(lk, pr, e)
}

// commit plans one step of events over pr's state read under lk, each of
// which must find a rule, and carries it out (step).
func (h tickHost) commit(lk prlock.Locked, pr PR, events ...Event) (PRState, bool, error) {
	fs := make([]folded, len(events))
	for i, e := range events {
		fs[i] = must(e)
	}
	return h.step(lk, pr, nil, fs...)
}

// step plans one step of events over pr's state read under lk, with the
// step's own ledger writes, and carries it out in its phases: workers
// stopped first, under the lock, against that state; then the ledger's
// writes in one transaction (Ledger.Commit); then, once committed, the row
// sent to elly. A crash before the commit leaves the state as it was, the
// stopped worker included, which the next pass settles; after it, the next
// refresh sends the row. ok is false when a must event finds no rule, and
// then nothing changes, the writes included; a may event that finds none
// is skipped.
func (h tickHost) step(lk prlock.Locked, pr PR, writes []ledgerWrite, events ...folded) (PRState, bool, error) {
	s, err := h.stateOf(lk, pr)
	if err != nil {
		return s, false, err
	}
	p, ok := fold(s, h.now(), events...)
	if !ok {
		return s, false, nil
	}
	if len(p.decisions) == 0 && len(writes) == 0 {
		return s, true, nil
	}
	p.origin, p.writes = h.origin, writes
	if p.to.State == stAwaiting && p.to.Kind == kindApprove {
		p.window = windowOf(h.sess, h.ledger, pr)
	}
	what := fmt.Sprintf("move %s from %s to %s on %s", pr.URL(), describe(s), describe(p.to), p.eventNames())
	if len(p.decisions) == 0 {
		what = p.writeNames() + " of " + pr.URL()
	} else if len(writes) > 0 {
		what += "; " + p.writeNames()
	}
	err = h.do(what, func() error {
		if slices.Contains(p.effects, efStopWorkers) {
			if err := h.stopWorkers(pr); err != nil {
				return err
			}
		}
		if err := h.ledger.Commit(lk, pr, p); err != nil {
			return err
		}
		switch {
		case len(p.decisions) == 0:
			// The state did not move: its row stands.
		case deleted(p.to):
			// A row elly does not drop now keeps its receipt, which the
			// stale-row walk sends the DELETE for again next tick.
			h.warn(pr, h.clear(lk, pr))
		default:
			// The state is committed; elly refusing the row is said, never
			// the step's failure, and the next refresh sends it again.
			h.warn(pr, h.show(lk, pr, p.to, Hold{}))
		}
		return nil
	})
	return p.to, true, err
}

// stopWorkers stops pr's workers, the one effect a step performs before
// its commit.
func (h tickHost) stopWorkers(pr PR) error {
	if h.sess.Roster == nil {
		return errors.New("this host cannot list workers, so it cannot stop them")
	}
	all, err := h.sess.Roster()
	if err != nil {
		return err
	}
	mine, err := h.workersOf(pr, all)
	if err != nil {
		return err
	}
	for _, w := range mine {
		if err := h.sess.stopAndWait(w); err != nil {
			return err
		}
	}
	return nil
}

// show sends pr's row for s to elly, hold naming why an owed round waits
// this pass. A state without a row deletes it. s must be read under lk.
func (h tickHost) show(lk prlock.Locked, pr PR, s PRState, hold Hold) error {
	if err := lk.Check(prKey(pr)); err != nil {
		return err
	}
	v, err := h.viewFor(pr, s, hold)
	if err != nil {
		return err
	}
	a := render(pr, s, v)
	if a.State == "" {
		if _, had, err := h.ledger.Annotation(pr); err != nil || !had {
			return err
		}
		return h.clear(lk, pr)
	}
	expires, send := h.refreshedExpiry(a)
	if expires.IsZero() {
		return h.clear(lk, pr) // a running row nothing settled in its lifetime
	}
	if h.elly != nil && send {
		if err := h.put(lk, pr, a, expires); err != nil {
			return err
		}
	}
	return h.setRow(lk, pr, a, notifyIdentity(pr, s, a))
}

// notifyIdentity is what a notification is about: the state and a hash of
// its row's note. A window keeps the identity of the needs-the-user state
// it stands over, so opening and closing it on an unanswered question
// notifies nothing new.
func notifyIdentity(pr PR, s PRState, a Annotation) string {
	if isWindow(s) {
		for _, under := range chain(s) {
			if isWindow(under.PRState) {
				continue
			}
			if ua := renderRow(pr, under.PRState, View{}); needsUser(ua.State) {
				return notifyIdentity(pr, under.PRState, ua)
			}
			break
		}
	}
	return describe(s) + " " + hashOf(a.Note)
}

// viewFor is what render needs beyond s: the hold, whether a dismissal is
// pending, and for an Approve its token, read under pr's lock. The token
// is there only while wantToken holds, and render offers Approve only with
// one: a row whose token was spent (a click whose approve failed,
// revokePreC7Tokens) or held back (a mention since cleared) gets a fresh
// one rather than a link nothing accepts, and a window open or unknown
// hides the link.
func (h tickHost) viewFor(pr PR, s PRState, hold Hold) (View, error) {
	v := View{Hold: hold}
	dismissing, err := h.ledger.DismissalPending(pr)
	if err != nil {
		return v, err
	}
	v.Dismissing = dismissing
	if v.Mentions, err = h.ledger.MentionGate(pr); err != nil {
		return v, err
	}
	if s.State != stAwaiting || s.Kind != kindApprove {
		return v, nil
	}
	if !wantToken(s, windowOf(h.sess, h.ledger, pr), v.Mentions, dismissing) {
		return v, nil
	}
	tok, has, err := h.ledger.TokenFor(pr, s.Head)
	if err == nil && !has {
		tok, err = h.ledger.MintToken(pr, s.Head)
	}
	v.Token = tok
	return v, err
}

// put sends a to elly. Nothing is recorded or notified for a row elly did
// not take: a PR it does not list yet gets it once refresh sees it there,
// and a failure is sent again next pass.
func (h tickHost) put(lk prlock.Locked, pr PR, a Annotation, expires time.Time) error {
	if err := h.elly.Put(pr.URL(), a, expires); err != nil {
		if refusedWith(err) == http.StatusBadRequest {
			err = errors.Join(err, h.hidden(lk, pr, a))
		}
		return err
	}
	return h.ledger.DeleteMeta(metaHiddenPrefix + pr.URL())
}

// hidden handles a row elly refused as invalid, a bug in claude-review: the
// row it last accepted is taken down, since it no longer says what is
// true, and the user hears once per refused row that one is hidden.
func (h tickHost) hidden(lk prlock.Locked, pr PR, a Annotation) error {
	if _, had, err := h.ledger.Annotation(pr); err != nil || had {
		if err := errors.Join(err, h.clear(lk, pr)); err != nil {
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
