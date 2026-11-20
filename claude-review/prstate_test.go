package main

import (
	"path/filepath"
	"testing"
)

func TestLedgerPRStateRoundTrip(t *testing.T) {
	l := openTestLedger(t)
	pr := widgetsPR
	if _, ok, err := l.PRState(pr); ok || err != nil {
		t.Fatalf("a PR with no state: ok=%v err=%v", ok, err)
	}
	running := PRState{State: stRunning, Kind: kindTick, Head: "aaa", Since: t0}
	s := PRState{State: stHeld, Kind: kindWindow, Head: "aaa", Detail: "d", Review: "https://github.com/o/r/pull/42#r1", Since: t0,
		Prev: &PRState{State: stAwaiting, Kind: kindQuestion, Detail: "why?", Since: t0, Prev: &running}}
	if err := l.SetPRState(pr, s); err != nil {
		t.Fatal(err)
	}
	got, ok, err := l.PRState(pr)
	if err != nil || !ok || got.State != stHeld || got.Kind != kindWindow || got.Detail != "d" || got.Head != "aaa" || got.Review != s.Review ||
		!got.Since.Equal(t0) || got.Prev == nil || got.Prev.Detail != "why?" || got.Prev.Prev == nil || got.Prev.Prev.Kind != kindTick {
		t.Fatalf("got %+v ok=%v err=%v", got, ok, err)
	}
	if err := l.DeletePRState(pr); err != nil {
		t.Fatal(err)
	}
	if _, ok, _ := l.PRState(pr); ok {
		t.Fatal("deleted state is still there")
	}
}

// Every annotation the old code wrote maps to a state, by state and note.
func TestMigratePRState(t *testing.T) {
	cases := []struct {
		name   string
		a      Annotation
		unread bool
		want   State
		kind   string
		detail string
	}{
		{"unread round wins", Annotation{State: stateHandled, Note: "round posted its review"}, true, stRunning, kindTick, ""},
		{"started by a click", Annotation{State: stateRunning, Note: "started by a click"}, false, stIdle, "", ""},
		{"question", Annotation{State: stateNeedsYou, Note: "the round has a question for you: why?"}, false, stAwaiting, kindQuestion, "why?"},
		{"bare question", Annotation{State: stateNeedsYou, Note: "the round has a question for you"}, false, stAwaiting, kindQuestion, ""},
		{"approve", Annotation{State: stateNeedsYou, Note: "LGTM at aaaaaaaaa; approve it if you agree"}, false, stAwaiting, kindApprove, ""},
		{"dismissed", Annotation{State: stateNeedsYou, Note: "new findings after your approval; your approval was dismissed"}, false, stAwaiting, kindDismiss, ""},
		{"drafts", Annotation{State: stateNeedsYou, Note: "the round left 3 unsent comments in a draft review; submit or discard it on GitHub"}, false, stAwaiting, kindDrafts, "3"},
		{"fresh look", Annotation{State: stateNeedsYou, Note: "the PR changed under its first look (force-push or new base); click claude review to look again"}, false, stHeld, kindFresh, ""},
		{"window", Annotation{State: stateNeedsYou, Note: legacyWindowNote}, false, stHeld, kindWindow, ""},
		{"paused", Annotation{State: stateNeedsYou, Note: "paused: the daily round limit for this PR is reached"}, false, stIdle, "", ""},
		{"refused, summary posted", Annotation{State: stateStuck, Note: "a post was refused over the limit; no more rounds on this PR until you start one again"}, false, stHandled, kindPosted, detailCapped},
		{"failed", Annotation{State: stateStuck, Note: "round stopped twice before posting its summary"}, false, stStuck, kindFailed, "round stopped twice before posting its summary"},
		{"posted", Annotation{State: stateHandled, Note: "round posted its review"}, false, stHandled, kindPosted, ""},
		{"nothing", Annotation{State: stateHandled, Note: "round found nothing to post"}, false, stHandled, kindNothing, ""},
		{"approval stands", Annotation{State: stateHandled, Note: "clean round; your approval stands"}, false, stHandled, kindApproved, ""},
		{"unknown", Annotation{State: stateNeedsYou, Note: "something old"}, false, stIdle, "", ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "ledger.db")
			l, err := OpenLedger(path)
			if err != nil {
				t.Fatal(err)
			}
			pr := widgetsPR
			c.a.Links = []Link{{Label: legacySummaryLabel, URL: "https://github.com/o/r/pull/42#r1"}}
			if err := l.Accept(pr, "aaa", "main", t0); err != nil {
				t.Fatal(err)
			}
			if err := l.SetAnnotation(pr, c.a); err != nil {
				t.Fatal(err)
			}
			if _, err := l.StartRound(pr, "bbb", NewHead{}, t0); err != nil {
				t.Fatal(err)
			}
			if !c.unread {
				if _, err := l.db.Exec(`update rounds set read_at = 1`); err != nil {
					t.Fatal(err)
				}
			}
			// The ledger as it was before pr_state existed.
			if _, err := l.db.Exec(`delete from pr_state`); err != nil {
				t.Fatal(err)
			}
			_ = l.Close()
			if l, err = OpenLedger(path); err != nil {
				t.Fatal(err)
			}
			defer func() { _ = l.Close() }()
			got, ok, err := l.PRState(pr)
			if err != nil || !ok || got.State != c.want || got.Kind != c.kind || got.Detail != c.detail || got.Head != "bbb" {
				t.Fatalf("got %+v ok=%v err=%v, want %s{%s} %q at bbb", got, ok, err, c.want, c.kind, c.detail)
			}
			if c.unread && !got.Since.Equal(t0) {
				t.Fatalf("running since %v, want the round's start %v", got.Since, t0)
			}
			if c.want == stHandled && got.Review != "https://github.com/o/r/pull/42#r1" {
				t.Fatalf("summary link lost: %+v", got)
			}
		})
	}
}

// The migration never overwrites a state, and catches up a PR accepted
// without one.
func TestMigratePRStateNeverOverwrites(t *testing.T) {
	path := filepath.Join(t.TempDir(), "ledger.db")
	l, err := OpenLedger(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := l.Accept(widgetsPR, "aaa", "main", t0); err != nil {
		t.Fatal(err)
	}
	if err := l.SetPRState(widgetsPR, PRState{State: stHeld, Kind: kindLaunch, Head: "aaa"}); err != nil {
		t.Fatal(err)
	}
	other := PR{Owner: "o", Repo: "r", Number: 7}
	if err := l.Accept(other, "ccc", "main", t0); err != nil {
		t.Fatal(err)
	}
	if err := l.SetAnnotation(other, Annotation{State: stateHandled, Note: legacyPosted}); err != nil {
		t.Fatal(err)
	}
	_ = l.Close()
	if l, err = OpenLedger(path); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = l.Close() }()
	if got, _, _ := l.PRState(widgetsPR); got.Kind != kindLaunch {
		t.Fatalf("overwritten: %+v", got)
	}
	if got, ok, _ := l.PRState(other); !ok || got.Kind != kindPosted || got.Head != "ccc" {
		t.Fatalf("not caught up: %+v ok=%v", got, ok)
	}
}

// A stuck{post_refused} stored before the cap counted as finished, with its
// summary posted, opens as handled; one without a summary stays stuck.
func TestOpenUncapsStuckWithASummary(t *testing.T) {
	path := t.TempDir() + "/l.db"
	l, err := OpenLedger(path)
	if err != nil {
		t.Fatal(err)
	}
	a, b := PR{Owner: "o", Repo: "r", Number: 1}, PR{Owner: "o", Repo: "r", Number: 2}
	_ = l.SetPRState(a, PRState{State: stStuck, Kind: kindRefused, Review: a.URL() + "#r"})
	_ = l.SetPRState(b, PRState{State: stStuck, Kind: kindRefused})
	_ = l.Close()
	if l, err = OpenLedger(path); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = l.Close() }()
	sa, _, _ := l.PRState(a)
	sb, _, _ := l.PRState(b)
	if sa.State != stHandled || sa.Detail != detailCapped || sb.State != stStuck {
		t.Fatalf("a %+v, b %+v", sa, sb)
	}
}
