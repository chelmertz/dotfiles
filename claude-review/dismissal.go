package main

import (
	"errors"
	"fmt"

	"github.com/chelmertz/dotfiles/claude-review/ids"
	"github.com/chelmertz/dotfiles/claude-review/prlock"
)

// dismissalMessage is the reason posted with a dismissal.
const dismissalMessage = "A later review round found new problems at the current head, so this approval no longer covers it."

// sendDismissal asks GitHub to dismiss d's review. A refusal is not the
// tick's failure: the record stays, the next tick retries, and the user hears
// about it once.
func (h tickHost) sendDismissal(d Dismissal) {
	pr, err := ParseTarget(d.PR)
	if err != nil {
		h.warn(PR{}, err)
		return
	}
	err = h.do(fmt.Sprintf("dismiss review %s on %s", d.Review, d.PR), func() error { return h.gh.Dismiss(pr, d.Review, dismissalMessage) })
	if err != nil {
		h.warn(pr, err)
		h.notifyDismissal(d, "could not dismiss your approval; tick retries it")
	}
}

// settleDismissals reads back every pending dismissal, closed PRs' too. One
// seen dismissed by the user settles; one whose PR merged or whose repository
// was archived is dropped, said once; any other answer, an error included
// (a 404 is not "gone": a private repository the token cannot see answers
// 404), sends it again. Settling runs under the PR's lock, since minting
// reads the pending dismissal; a PR a click holds is read back next pass.
func (h tickHost) settleDismissals() error {
	all, err := h.ledger.PendingDismissals()
	if err != nil {
		return err
	}
	for _, d := range all {
		pr, err := ParseTarget(d.PR)
		if err != nil {
			h.warn(PR{}, err)
			continue
		}
		st, err := h.gh.DismissalStatus(pr, d.Review)
		switch {
		case err != nil:
			h.warn(pr, fmt.Errorf("reading back the dismissal of review %s: %w", d.Review, err))
			h.notifyDismissal(d, "could not check the dismissal of your approval; tick retries it")
		case st.Dismissed || st.Merged || st.Archived:
			if err := h.settleDismissal(pr, d, st); err != nil {
				return err
			}
		default:
			h.sendDismissal(d)
		}
	}
	return nil
}

// settleDismissal forgets d, seen dismissed (and pr's row sent again) or
// dropped (and said), under pr's lock; a PR a click holds is left.
func (h tickHost) settleDismissal(pr PR, d Dismissal, st DismissalStatus) error {
	lk, err := lockPR(pr, h.lock)
	if errors.Is(err, errLocked) {
		return nil
	}
	if err != nil {
		return err
	}
	defer lk.Unlock()
	if st.Dismissed {
		if err := h.do("settle the dismissal of review "+d.Review.String()+" on "+d.PR, func() error { return h.ledger.SettleDismissal(lk, d) }); err != nil {
			return err
		}
		h.warn(pr, h.reshow(lk, pr))
		return nil
	}
	why := map[bool]string{true: "merged", false: "its repository was archived"}[st.Merged]
	if err := h.do("drop the dismissal of review "+d.Review.String()+" on "+d.PR+": "+why, func() error { return h.ledger.SettleDismissal(lk, d) }); err != nil {
		return err
	}
	if h.notify != nil && !h.dry {
		h.notify(d.PR + ": " + why + " before your approval could be dismissed; it stands")
	}
	return nil
}

// notifyDismissal says why once per record; the mark survives a restart.
func (h tickHost) notifyDismissal(d Dismissal, why string) {
	if d.Notified || h.notify == nil || h.dry {
		return
	}
	h.notify(d.PR + ": " + why)
	h.warn(PR{}, errors.Join(h.ledger.MarkDismissalNotified(d)))
}

// reshow sends pr's row again from its state, read under lk.
func (h tickHost) reshow(lk prlock.Locked, pr PR) error {
	if err := lk.Check(prKey(pr)); err != nil {
		return err
	}
	s, ok, err := h.ledger.PRState(pr)
	if err != nil || !ok {
		return err
	}
	return h.show(lk, pr, s, Hold{})
}

// beingDismissed: review is the one a pending dismissal on pr targets.
func (h tickHost) beingDismissed(pr PR, review ids.ReviewID) (bool, error) {
	all, err := h.ledger.PendingDismissals()
	if err != nil {
		return false, err
	}
	for _, d := range all {
		if d.PR == pr.URL() && d.Review == review {
			return true, nil
		}
	}
	return false, nil
}
