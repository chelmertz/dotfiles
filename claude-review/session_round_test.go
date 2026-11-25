package main

import (
	"testing"
	"time"

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
		Roster: func() (Roster, error) { return Roster{}, nil },
		Window: func(id ids.SessionID) (int, error) {
			if id == old {
				return 100, nil
			}
			return 0, nil
		},
	}
	if windowOf(s, openTestLedger(t), widgetsPR) != winOpen {
		t.Error("a window on the last version's round session read as closed")
	}
}

// A ledger that cannot be read fails the round session rather than reading
// as "no round yet", which would name today's id beside a live worker.
func TestRoundSessionFailsOnAnUnreadableLedger(t *testing.T) {
	l := openTestLedger(t)
	if err := l.db.Close(); err != nil {
		t.Fatal(err)
	}
	if got, err := l.RoundSession(widgetsPR); err == nil {
		t.Errorf("a closed ledger read as session %s", got)
	}
}

// LastRoundStart is the newest round's start, to the second.
func TestLastRoundStart(t *testing.T) {
	l := openTestLedger(t)
	if got, err := l.LastRoundStart(widgetsPR); err != nil || !got.IsZero() {
		t.Errorf("no round: %v %v", got, err)
	}
	for _, at := range []time.Time{t0.Add(-2 * time.Hour), t0.Add(-time.Hour)} {
		if _, err := l.StartRound(widgetsPR, "h1", NewHead{}, at); err != nil {
			t.Fatal(err)
		}
	}
	if got, err := l.LastRoundStart(widgetsPR); err != nil || !got.Equal(t0.Add(-time.Hour)) {
		t.Errorf("got %v %v, want %v", got, err, t0.Add(-time.Hour))
	}
}
