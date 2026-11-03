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
// takes no post at all.
func ledgerSpend(d Deps) spendFunc {
	return func(pr PR, slot string, limit int) error {
		head, err := openHead(d.Gh, pr)
		if err != nil {
			return err
		}
		l, err := OpenLedger(LedgerPath(d.StateDir))
		if err != nil {
			return err
		}
		defer func() { _ = l.Close() }()
		ok, err := l.SpendPost(pr, head, slot, limit, time.Now())
		if err != nil {
			return fmt.Errorf("the post budget: %w", err)
		}
		if !ok {
			return fmt.Errorf("the limit of %d posts (%s) at commit %.9s is reached; end the round with the question line", limit, slot, head)
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
