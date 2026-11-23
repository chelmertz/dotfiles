package main

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/chelmertz/dotfiles/claude-review/prlock"
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
	if err != nil || !ok || got.State != stHeld || got.Kind != kindWindow || got.Detail != "" || got.Head != "aaa" || got.Review != s.Review ||
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
		{"failed", Annotation{State: stateStuck, Note: "round stopped twice before posting its summary"}, false, stStuck, kindFailed, ""}, // an old sentence: reason unknown
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
			defer func() { _ = l.Close() }()
			migrate(t, l)
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
	defer func() { _ = l.Close() }()
	migrate(t, l)
	if got, _, _ := l.PRState(widgetsPR); got.Kind != kindLaunch {
		t.Fatalf("overwritten: %+v", got)
	}
	if got, ok, _ := l.PRState(other); !ok || got.Kind != kindPosted || got.Head != "ccc" {
		t.Fatalf("not caught up: %+v ok=%v", got, ok)
	}
}

// A stuck{post_refused} stored before the cap counted as finished, with its
// summary posted, migrates to handled; one without a summary stays stuck.
func TestMigrationUncapsStuckWithASummary(t *testing.T) {
	l := openTestLedger(t)
	a, b := PR{Owner: "o", Repo: "r", Number: 1}, PR{Owner: "o", Repo: "r", Number: 2}
	_ = l.SetPRState(a, PRState{State: stStuck, Kind: kindRefused, Review: a.URL() + "#r"})
	_ = l.SetPRState(b, PRState{State: stStuck, Kind: kindRefused})
	migrate(t, l)
	sa, _, _ := l.PRState(a)
	sb, _, _ := l.PRState(b)
	if sa.State != stHandled || sa.Detail != detailCapped || sb.State != stStuck {
		t.Fatalf("a %+v, b %+v", sa, sb)
	}
}

// takeAny takes every lock; heldLock takes every lock but held's.
func takeAny(string) (func(), error) { return func() {}, nil }

func heldLock(held PR) prlock.Taker {
	return func(name string) (func(), error) {
		if name == prKey(held) {
			return nil, errLocked
		}
		return func() {}, nil
	}
}

// migrate runs the pr_state migrations as tick does, with no lock held.
func migrate(t *testing.T, l *Ledger) {
	t.Helper()
	if skipped, err := l.MigrateStatesOnce(takeAny); err != nil || skipped != 0 {
		t.Fatalf("migration: skipped %d, err %v", skipped, err)
	}
}

// An open leaves pr_state alone: only tick migrates, under the PR's lock.
func TestOpeningTheLedgerMigratesNothing(t *testing.T) {
	path := filepath.Join(t.TempDir(), "ledger.db")
	l, err := OpenLedger(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := l.Accept(widgetsPR, "aaa", "main", t0); err != nil {
		t.Fatal(err)
	}
	if err := l.SetAnnotation(widgetsPR, Annotation{State: stateHandled, Note: legacyPosted}); err != nil {
		t.Fatal(err)
	}
	_ = l.Close()
	if l, err = OpenLedger(path); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = l.Close() }()
	if s, ok, err := l.PRState(widgetsPR); err != nil || ok {
		t.Errorf("the open migrated %s (err %v)", describe(s), err)
	}
}

// A PR a click holds is skipped and the flag stays unset, so the next pass
// migrates it; the flag is set on the first pass that skips nothing.
func TestTheMigrationFlagIsSetOnlyWhenNoPRWasSkipped(t *testing.T) {
	l := openTestLedger(t)
	held, other := widgetsPR, PR{Owner: "acme", Repo: "widgets", Number: 7}
	for _, pr := range []PR{held, other} {
		if err := l.Accept(pr, "aaa", "main", t0); err != nil {
			t.Fatal(err)
		}
		if err := l.SetAnnotation(pr, Annotation{State: stateHandled, Note: legacyPosted}); err != nil {
			t.Fatal(err)
		}
	}
	skipped, err := l.MigrateStatesOnce(heldLock(held))
	if err != nil || skipped != 1 {
		t.Fatalf("a held PR: skipped %d, err %v", skipped, err)
	}
	if done, _ := l.Meta(metaStatesMigrated); done != "" {
		t.Error("the flag was set on a pass that skipped a PR")
	}
	if _, ok, _ := l.PRState(held); ok {
		t.Error("the held PR was migrated without its lock")
	}
	if s, ok, _ := l.PRState(other); !ok || s.Kind != kindPosted {
		t.Errorf("the free PR was not migrated: %s", describe(s))
	}
	migrate(t, l)
	if done, _ := l.Meta(metaStatesMigrated); done == "" {
		t.Error("the flag was not set on a pass that skipped nothing")
	}
	if s, ok, _ := l.PRState(held); !ok || s.Kind != kindPosted {
		t.Errorf("the once-held PR was not migrated: %s", describe(s))
	}
}

// After a completed pass, a later tick does not map rows again: a closed
// PR keeps its acceptance and row and loses its state, and stays without.
func TestASecondTickDoesNotRerunTheMigration(t *testing.T) {
	l := openTestLedger(t)
	h := tickHost{ledger: l, lock: takeAny}
	if err := l.Accept(widgetsPR, "aaa", "main", t0); err != nil {
		t.Fatal(err)
	}
	if err := l.SetAnnotation(widgetsPR, Annotation{State: stateNeedsYou, Note: "LGTM at aaaaaaaaa; approve it if you agree"}); err != nil {
		t.Fatal(err)
	}
	if err := h.migrateStates(); err != nil {
		t.Fatal(err)
	}
	if s, ok, _ := l.PRState(widgetsPR); !ok || s.Kind != kindApprove {
		t.Fatalf("the first pass did not migrate: %s", describe(s))
	}
	if err := l.DeletePRState(widgetsPR); err != nil { // closed
		t.Fatal(err)
	}
	if err := h.migrateStates(); err != nil {
		t.Fatal(err)
	}
	if s, ok, err := l.PRState(widgetsPR); err != nil || ok {
		t.Errorf("the second pass mapped the row again: %s (err %v)", describe(s), err)
	}
}

// A closed PR's outcome is a deletion: storing it fails rather than leaving
// a state no rule reads.
func TestSetPRStateRefusesADeletion(t *testing.T) {
	l := openTestLedger(t)
	if err := l.SetPRState(widgetsPR, PRState{}); err == nil {
		t.Error("the deletion outcome was stored")
	}
}

// States are written only through prstate.go (SetPRState, DeletePRState,
// the migration), which move and the converter use; a
// statement elsewhere that writes pr_state bypasses the rules. The scan
// reads SQL, not callers, so a new raw statement fails here too.
func TestOnlyPRStateWritesThePRStateTable(t *testing.T) {
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	write := regexp.MustCompile("(?i)(insert|replace|update|delete)[^`]*pr_state")
	for _, f := range files {
		if f == "prstate.go" || strings.HasSuffix(f, "_test.go") {
			continue
		}
		b, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		if m := write.Find(b); m != nil {
			t.Errorf("%s writes pr_state outside prstate.go: %q", f, m)
		}
	}
}
