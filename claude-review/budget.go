package main

import (
	"errors"
	"fmt"
	"time"
)

// ledgerSpend is the post budget in the runner's ledger, keyed by the PR's
// head on GitHub. Every session's posts count, clicked or unattended, so
// nothing about who started the session can leave a post uncounted, and
// any error reading the ledger refuses the post. A PR that is not open
// takes no post at all. A round runs this beside tick, unlocked, so it
// opens the ledger only as OpenLedgerBudget: no migration, and no write
// beyond posts and post_refusals.
func ledgerSpend(d Deps) spendFunc {
	return func(pr PR, slot string, limit int) error {
		head, err := openHead(d.Gh, pr)
		if err != nil {
			return err
		}
		l, err := OpenLedgerBudget(LedgerPath(d.StateDir))
		if err != nil {
			return err
		}
		defer func() { _ = l.Close() }()
		ok, err := l.SpendPost(pr, head, slot, limit, time.Now())
		if err != nil {
			return fmt.Errorf("the post budget: %w", err)
		}
		if !ok {
			next := "end the round with the question line"
			if slot == postSlotNew {
				next = "list the remaining findings in your summary review (`claude-review post <pr> review`), which has its own budget, then end the round"
			}
			return fmt.Errorf("the limit of %d posts (%s) at commit %.9s is reached; %s", limit, slot, head, next)
		}
		return nil
	}
}

// openHead is the head of pr while GitHub reports it open. A merged or
// closed PR, or one GitHub does not answer for, is an error, so no post
// reaches it in the minutes before tick's sweep stops its round. Not a
// post_refusals row: that one means a round over its limit, and holds the
// PR back from rounds.
func openHead(gh Gh, pr PR) (string, error) {
	p, err := GitHub{Gh: gh}.pull(pr)
	switch {
	case err != nil:
		return "", fmt.Errorf("reading the PR: %w", err)
	case pullState(p) != prOpen:
		return "", fmt.Errorf("%s is merged or closed; end the round", pr.URL())
	case p.Head.SHA == "":
		return "", errors.New("reading the PR: empty head sha")
	}
	return p.Head.SHA, nil
}

// ledgerGate holds a round that only answers mentions (its newest round's
// trigger is OwnMention, on the user's own PR) to answers: no inline
// comment, and a review only to send replies already queued in the draft.
// Read from the round, not the PR's author, so the user's own click on the
// PR reviews as before. A ledger it cannot read refuses.
func ledgerGate(d Deps) postGate {
	return func(pr PR, kind string, draftOpen bool) error {
		l, err := OpenLedgerBudget(LedgerPath(d.StateDir))
		if err != nil {
			return err
		}
		defer func() { _ = l.Close() }()
		t, err := l.NewestTrigger(pr)
		if err != nil {
			return err
		}
		return mentionsOnlyPost(t, kind, draftOpen)
	}
}

// mentionsOnlyPost is the rule ledgerGate applies.
func mentionsOnlyPost(t Trigger, kind string, draftOpen bool) error {
	if !mentionsOnly(t) {
		return nil
	}
	switch {
	case kind == kindInline:
		return errors.New("this round answers mentions on the user's own PR and posts no inline comment")
	case kind == kindReview && !draftOpen:
		return errors.New("this round answers mentions on the user's own PR and posts no review; a review here only sends queued replies")
	}
	return nil
}
