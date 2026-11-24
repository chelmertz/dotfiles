package main

import (
	"slices"
	"testing"

	"github.com/chelmertz/dotfiles/claude-review/ids"
)

// recordUnlisted writes PR n's round at its head as a launch does before
// it, and nothing after: a crash between the row and the launch.
func (g *genWorld) recordUnlisted(t *testing.T, n int) ids.RoundID {
	t.Helper()
	pr := g.prs[n-1]
	lk := mustLock(t, g.h, pr)
	id, err := g.h.ledger.RecordRound(lk, pr, g.gh.head[n], Requested{At: g.now}, g.now, g.size[pr.RoundSessionID()])
	if err != nil {
		t.Fatal(err)
	}
	lk.Unlock()
	return id
}

// A round written before a launch that never happened (no worker, no
// transcript past its start) is taken back by settle, and the review it
// was for is owed again: the same pass launches it.
func TestSettleTakesBackAnUnlistedRoundThatNeverLaunched(t *testing.T) {
	g := newGenWorldT(t, 1)
	g.recordUnlisted(t, 1)
	if ran, err := g.h.ledger.RanAt(g.prs[0], g.gh.head[1], Requested{At: g.now}, g.now); err != nil || !ran {
		t.Fatalf("the unlisted round does not count as run: %v %v", ran, err)
	}
	g.passes(t, 1)
	key := g.prs[0].URL() + " " + g.gh.head[1]
	if n := g.launches[key]; n != 1 {
		t.Errorf("launched %d times, want once: the review is owed again once its row is taken back; journal:\n%s", n, g.out)
	}
	rounds, err := g.h.ledger.UnreadRounds()
	if err != nil {
		t.Fatal(err)
	}
	if len(rounds) != 1 || !rounds[0].Listed || rounds[0].Retries != 0 {
		t.Fatalf("unread rounds %+v, want the new launch's, listed and not a retry", rounds)
	}
	if s := g.state(t, 1); s.State != stRunning || s.Kind != kindTick || s.Round != rounds[0].ID {
		t.Errorf("the PR is %s on round %s, want running{tick} on %s", describe(s), s.Round, rounds[0].ID)
	}
}

// A round whose launch happened but whose listing step never committed
// (the process died after the launch) is listed by settle on its worker,
// the owed move made with its id, and read as any round from then on.
func TestSettleListsAnUnlistedRoundWithAWorker(t *testing.T) {
	g := newGenWorldT(t, 1)
	g.crash.arm(crashAfterLaunch, 1)
	g.mustTick(t)
	if !g.crashed {
		t.Fatal("no launch crashed")
	}
	rounds, err := g.h.ledger.UnreadRounds()
	if err != nil || len(rounds) != 1 || rounds[0].Listed {
		t.Fatalf("after the crash: %+v %v, want one unlisted round", rounds, err)
	}
	id := rounds[0].ID
	if s := g.state(t, 1); s.State == stRunning {
		t.Fatalf("the crash left %s: the listing step committed", describe(s))
	}
	g.passes(t, 1)
	if s := g.state(t, 1); s.State != stRunning || s.Kind != kindTick || s.Round != id {
		t.Errorf("settle left %s on round %s, want running{tick} on %s", describe(s), s.Round, id)
	}
	rounds, err = g.h.ledger.UnreadRounds()
	if err != nil || len(rounds) != 1 || rounds[0].ID != id || !rounds[0].Listed {
		t.Errorf("unread rounds %+v (%v), want round %s listed", rounds, err, id)
	}
	if n := g.launches[g.prs[0].URL()+" "+g.gh.head[1]]; n != 1 {
		t.Errorf("launched %d times, want once", n)
	}
	msg, _ := g.message(1, "nothing")
	g.finish(1, msg, "")
	g.passes(t, 1)
	if s := g.state(t, 1); s.State != stHandled || s.Kind != kindNothing {
		t.Errorf("the listed round did not settle as any round: %s", describe(s))
	}
}

// Transcript text past where the round's lines start says it launched
// too, with no worker listed any more: it is listed and read as any round,
// here one cut off before its summary, so retried, never launched anew.
func TestSettleListsAnUnlistedRoundWithTranscriptText(t *testing.T) {
	g := newGenWorldT(t, 1)
	id := g.recordUnlisted(t, 1)
	g.size[g.prs[0].RoundSessionID()]++ // its prompt, its worker gone
	g.passes(t, 1)
	if s := g.state(t, 1); s.State != stRunning || s.Kind != kindRetry || s.Round != id {
		t.Errorf("the PR is %s on round %s, want running{retry} on %s", describe(s), s.Round, id)
	}
	// Listed with the owed move its launch reports, then read.
	var owed int
	if err := g.h.ledger.db.QueryRow(`select count(*) from decisions where json_extract(event, '$.kind') = 'owed' and json_extract(event, '$.round') = ?`, id).Scan(&owed); err != nil || owed != 1 {
		t.Errorf("owed decisions on round %s: %d (%v), want 1", id, owed, err)
	}
	rounds, err := g.h.ledger.UnreadRounds()
	if err != nil || len(rounds) != 1 || rounds[0].ID != id || !rounds[0].Listed || rounds[0].Retries != 1 {
		t.Errorf("unread rounds %+v (%v), want round %s listed and retried once", rounds, err, id)
	}
}

// The dismissal and the token revoke commit in their own step, logged
// failsafe, whatever the rule table says of the ended move after it: here
// it has no rule for it at all, and that step commits nothing.
func TestFailsafeCommitsWhenTheEndedMoveFindsNoRule(t *testing.T) {
	g := newGenWorldT(t, 1)
	pr := g.prs[0]
	g.mustTick(t)
	running := g.state(t, 1)
	if running.State != stRunning {
		t.Fatalf("no round: %s", describe(running))
	}
	if _, err := g.h.ledger.MintToken(pr, g.gh.head[1]); err != nil {
		t.Fatal(err)
	}
	all := rules
	t.Cleanup(func() { rules = all })
	rules = slices.DeleteFunc(slices.Clone(all), func(r rule) bool { return r.event == evEnded })
	lk := mustLock(t, g.h, pr)
	review := reviewID(9)
	r := Round{ID: running.Round, PR: pr.URL(), Head: g.gh.head[1]}
	if _, err := g.h.apply(lk, pr, r, Dismiss{Review: review, State: PRState{State: stIdle}}, true); err != nil {
		t.Fatal(err)
	}
	if pending, err := g.h.ledger.DismissalPending(pr); err != nil || !pending {
		t.Errorf("the dismissal was not recorded: %v %v", pending, err)
	}
	if live, err := g.h.ledger.HasLiveToken(pr); err != nil || live {
		t.Errorf("a token stayed live: %v %v", live, err)
	}
	if s := g.state(t, 1); !sameState(s, running) {
		t.Errorf("the ended move with no rule moved %s to %s", describe(running), describe(s))
	}
	if g.unread(t, 1) != 1 {
		t.Errorf("the round was read though its move committed nothing")
	}
	var n int
	if err := g.h.ledger.db.QueryRow(`select count(*) from decisions where step = 'failsafe' and pr = ? and event is null and facts_kind = 'dismissal'`, pr.URL()).Scan(&n); err != nil || n != 1 {
		t.Errorf("failsafe rows: %d (%v), want 1", n, err)
	}
}

// A breaker trip is its own transaction: the PR's step that fails beside
// it (a duplicate launch's failure, refused here by the ledger) does not
// take the trip back.
func TestABreakerTripOutlivesAFailedPRCommit(t *testing.T) {
	w := newTickWorld(t)
	pr := w.accept(t, 42)
	if err := w.h.ledger.SetPRState(pr, PRState{State: stIdle, Head: "h1"}); err != nil {
		t.Fatal(err)
	}
	if _, err := w.h.ledger.db.Exec(`create trigger refuse_pr_state before insert on pr_state begin select raise(abort, 'refused'); end`); err != nil {
		t.Fatal(err)
	}
	lk := mustLock(t, w.h, pr)
	_, err := w.h.afterRelaunch(lk, pr, RelaunchResult{Kind: relaunchDuplicate, Detail: "two workers"}, nil, &launchRecord{failed: w.h.failedToStart(lk, pr)})
	if err == nil {
		t.Fatal("the duplicate reported no error")
	}
	if s, _ := w.h.peekState(pr); s.State != stIdle {
		t.Fatalf("the refused PR commit stored %s", describe(s))
	}
	if reason, err := w.h.ledger.Tripped(); err != nil || causeOf(reason) != causeDuplicate {
		t.Errorf("tripped %q (%v), want the duplicate's trip to stand", reason, err)
	}
	var n int
	if err := w.h.ledger.db.QueryRow(`select count(*) from decisions where step = 'global' and pr is null and facts_kind = 'breaker'`).Scan(&n); err != nil || n != 1 {
		t.Errorf("global rows: %d (%v), want 1", n, err)
	}
}
