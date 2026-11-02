package main

import (
	"fmt"
	"time"
)

// ledgerSpend is the post budget in the runner's ledger, keyed by the PR's
// head on GitHub. Every session's posts count, clicked or unattended, so
// nothing about who started the session can leave a post uncounted, and
// any error reading the ledger refuses the post.
func ledgerSpend(d Deps) spendFunc {
	return func(pr PR, slot string, limit int) error {
		head, err := headSHA(d.Gh, fmt.Sprintf("repos/%s/%s", pr.Owner, pr.Repo), pr.Number)
		if err != nil {
			return err
		}
		l, err := OpenLedger(LedgerPath(d.Home))
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
