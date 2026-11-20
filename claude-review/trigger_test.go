package main

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"
)

var tAt = time.Date(2026, 11, 20, 9, 30, 0, 0, time.UTC)

// Each variant's old text, key, kind and cap rule, pinned against literals:
// an older binary reads the text column, so the renderer must not drift.
func TestTriggerVariants(t *testing.T) {
	for _, c := range []struct {
		tr                    Trigger
		text, kind, key, from string
		counts                bool
	}{
		{Requested{At: tAt}, "review_requested@2026-11-20T09:30:00Z", "requested", "2026-11-20T09:30:00Z", "", true},
		{Replies{At: tAt}, "replies@2026-11-20T09:30:00Z", "replies", "2026-11-20T09:30:00Z", "", true},
		{Answered{At: tAt}, "answered@2026-11-20T09:30:00Z", "answered", "2026-11-20T09:30:00Z", "", true},
		{NewHead{Head: "abc"}, "new_head", "new_head", "abc", "", true},
		{Click{At: tAt, From: "elly"}, "click@2026-11-20T09:30:00Z?from=elly", "click", "2026-11-20T09:30:00Z", "elly", false},
	} {
		if got := c.tr.Text(); got != c.text {
			t.Errorf("%T text %q, want %q", c.tr, got, c.text)
		}
		if c.tr.Kind() != c.kind || c.tr.DedupeKey() != c.key || c.tr.CountsTowardCap() != c.counts || triggerFrom(c.tr) != c.from {
			t.Errorf("%T: kind %q key %q counts %v from %q", c.tr, c.tr.Kind(), c.tr.DedupeKey(), c.tr.CountsTowardCap(), triggerFrom(c.tr))
		}
		back, err := parseTrigger(c.text, "abc")
		if err != nil || back != c.tr {
			t.Errorf("%q parses back to %#v, %v", c.text, back, err)
		}
	}
}

// Every kind is listed once in the cap kinds exactly when it counts.
func TestCapKindsFollowTheVariants(t *testing.T) {
	want := []string{"requested", "replies", "answered", "new_head"}
	if got := capKinds(); strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("cap kinds %v, want %v", got, want)
	}
	if capKindsSQL() != "'requested','replies','answered','new_head'" {
		t.Errorf("sql %s", capKindsSQL())
	}
}

func TestParseTriggerBackfill(t *testing.T) {
	for in, want := range map[string]Trigger{
		"rereview_requested@2026-11-20T09:30:00Z": Requested{At: tAt},
		"review_requested@2026-11-20T09:30:00Z":   Requested{At: tAt},
		"click@2026-11-20T09:30:00Z":              Click{At: tAt},
		"click@2026-11-20T09:30:00Z?from=":        Click{At: tAt},
		"new_head":                                NewHead{Head: "h9"},
	} {
		got, err := parseTrigger(in, "h9")
		if err != nil || got != want {
			t.Errorf("%q: %#v %v, want %#v", in, got, err, want)
		}
	}
	// A request and a re-request at one time are one key.
	a, _ := parseTrigger("rereview_requested@2026-11-20T09:30:00Z", "")
	b, _ := parseTrigger("review_requested@2026-11-20T09:30:00Z", "")
	if a.Kind() != b.Kind() || a.DedupeKey() != b.DedupeKey() {
		t.Error("a request and a re-request at one time map to two keys")
	}
	for _, bad := range []string{"", "replies", "replies@yesterday", "nonsense@2026-11-20T09:30:00Z"} {
		if _, err := parseTrigger(bad, "h"); err == nil {
			t.Errorf("%q parsed", bad)
		}
	}
}

// Only the renderer and the backfill know the old text: anything else
// matching it would break when the text column goes.
func TestNothingElseMatchesOldTriggerText(t *testing.T) {
	old := regexp.MustCompile(`"(re)?review_requested|"replies@|"answered@|"click@|'click@|"new_head"`)
	files, _ := filepath.Glob("*.go")
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") || f == "trigger.go" {
			continue
		}
		b, err := os.ReadFile(f) //nolint:gosec // a source file of this package
		if err != nil {
			t.Fatal(err)
		}
		for i, line := range strings.Split(string(b), "\n") {
			if old.MatchString(line) {
				t.Errorf("%s:%d matches old trigger text: %s", f, i+1, strings.TrimSpace(line))
			}
		}
	}
}

// Rows with only the old text, as a pre-typed or rolled-back binary writes
// them, are read on open: a re-request and a request at one time are one
// key, a click is free of the cap, an unreadable row is counted and counts.
func TestOpenBackfillsOldTriggerText(t *testing.T) {
	path := filepath.Join(t.TempDir(), "ledger.db")
	l, err := OpenLedger(path)
	if err != nil {
		t.Fatal(err)
	}
	at := tAt.Format(time.RFC3339)
	for _, text := range []string{"rereview_requested@" + at, "click@" + at + "?from=elly", "garbage"} {
		if _, err := l.db.Exec(`insert into rounds (pr, head, trigger, started_at) values (?, 'h1', ?, ?)`, widgetsPR.URL(), text, tAt.Unix()); err != nil {
			t.Fatal(err)
		}
	}
	_ = l.Close()
	if l, err = OpenLedger(path); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = l.Close() }()
	if l.Unparsed != 1 {
		t.Errorf("unparsed %d, want 1", l.Unparsed)
	}
	if ran, _ := l.RanAt(widgetsPR, "h1", Requested{At: tAt}, tAt.Add(time.Hour)); !ran {
		t.Error("a backfilled re-request does not stop the same request")
	}
	if n, _ := l.RoundsSince(widgetsPR, tAt.Add(-time.Hour)); n != 2 {
		t.Errorf("cap counts %d, want 2: the request and the unreadable row, not the click", n)
	}
	var text string
	if err := l.db.QueryRow(`select trigger from rounds where trigger_kind = 'requested'`).Scan(&text); err != nil || text != "rereview_requested@"+at {
		t.Errorf("the stored text changed to %q (%v); an older binary matches its own rows by it", text, err)
	}
	src, _ := l.RoundSources(tAt.Add(-time.Hour))
	if src["elly"] != 1 {
		t.Errorf("sources %v", src)
	}
}

// RanAt: a round of the same kind and key at the head, or any round at the
// head since the PR's last update.
func TestRanAtKeepsBothHalves(t *testing.T) {
	l := openTestLedger(t)
	if _, err := l.StartRound(widgetsPR, "h1", Click{At: tAt}, tAt); err != nil {
		t.Fatal(err)
	}
	if ran, _ := l.RanAt(widgetsPR, "h1", Requested{At: tAt.Add(-time.Minute)}, tAt.Add(-time.Minute)); !ran {
		t.Error("a click after the last update did not suppress an owed request")
	}
	if ran, _ := l.RanAt(widgetsPR, "h1", Requested{At: tAt.Add(time.Minute)}, tAt.Add(time.Minute)); ran {
		t.Error("an older round of another kind suppressed a request")
	}
}
