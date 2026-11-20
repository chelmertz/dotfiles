package main

import (
	"testing"

	"github.com/chelmertz/dotfiles/claude-review/ids"
)

// oldVersionSession is pr's round session under the policy version before
// this one: what a round started before a deploy that bumped it ran in.
func oldVersionSession(pr PR) ids.SessionID {
	return ids.SessionFor(pr.URL() + " round-policy 3")
}

// roundIn records an unread round of pr that ran in session.
func roundIn(t *testing.T, l *Ledger, pr PR, session ids.SessionID) ids.RoundID {
	t.Helper()
	id, err := l.StartRound(pr, "h1", Requested{At: t0}, t0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := l.db.Exec(`update rounds set session = ? where id = ?`, session.String(), id); err != nil {
		t.Fatal(err)
	}
	return id
}

// A round's readers name its session from the round's row, never from the
// binary's policy version: a deploy that bumps it must not read a live
// worker as ended or launch a second one beside it.
func TestARoundStartedUnderTheLastVersionIsReadUnderTheNext(t *testing.T) {
	w := newTickWorld(t)
	pr := w.accept(t, 42)
	old := oldVersionSession(pr)
	if got, _ := w.h.ledger.RoundSession(pr); got != pr.RoundSessionID() {
		t.Errorf("no round: session %s, want today's", got)
	}
	r := roundIn(t, w.h.ledger, pr, old)
	if got, _ := w.h.ledger.RoundSession(pr); got != old {
		t.Errorf("an unread round's session read as %s, want the one it ran in", got)
	}
	w.live[old] = true
	if not, err := w.h.startOne(Planned{PR: pr, Head: "h1", Trigger: Requested{At: t0}}, 0); err != nil || not != skipRunning {
		t.Errorf("a live worker under the last version: skip %q err %v, want %q", not, err, skipRunning)
	}
	if err := w.h.ledger.MarkRead(r); err != nil {
		t.Fatal(err)
	}
	if got, _ := w.h.ledger.RoundSession(pr); got != old {
		t.Errorf("the newest settled round's session read as %s, want the one it ran in", got)
	}
}

// A window on any id the PR's workers may run under is a window, the last
// version's included.
func TestAWindowOnTheLastVersionsSessionIsOpen(t *testing.T) {
	old := oldVersionSession(widgetsPR)
	s := Sessions{
		Roster: func() ([]Worker, error) { return nil, nil },
		Window: func(id ids.SessionID) int {
			if id == old {
				return 100
			}
			return 0
		},
	}
	if !windowOpen(s, openTestLedger(t), widgetsPR) {
		t.Error("a window on the last version's round session read as closed")
	}
}
