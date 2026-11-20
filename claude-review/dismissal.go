package main

import (
	"errors"
	"fmt"
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
// 404), sends it again.
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
		case st.Dismissed:
			if err := h.do("settle the dismissal of review "+d.Review.String()+" on "+d.PR, func() error { return h.ledger.SettleDismissal(d) }); err != nil {
				return err
			}
			if err := h.reshow(pr); err != nil {
				h.warn(pr, err)
			}
		case st.Merged || st.Archived:
			why := map[bool]string{true: "merged", false: "its repository was archived"}[st.Merged]
			if err := h.do("drop the dismissal of review "+d.Review.String()+" on "+d.PR+": "+why, func() error { return h.ledger.SettleDismissal(d) }); err != nil {
				return err
			}
			if h.notify != nil && !h.dry {
				h.notify(d.PR + ": " + why + " before your approval could be dismissed; it stands")
			}
		default:
			h.sendDismissal(d)
		}
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

// reshow sends pr's row again from its stored state.
func (h tickHost) reshow(pr PR) error {
	s, ok, err := h.ledger.PRState(pr)
	if err != nil || !ok {
		return err
	}
	return h.show(pr, s, "")
}
