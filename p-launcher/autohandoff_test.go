package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Every case here is a refusal except one. A background process that rewrites
// a state file has to be wrong rarely, so the gates are what get asserted.
func TestHandoffDecide(t *testing.T) {
	root := t.TempDir()
	dir := mk(t, root, "m/h")
	handoff := filepath.Join(dir, "HANDOFF.md")
	early := time.Date(2026, 9, 13, 10, 0, 0, 0, time.UTC)
	late := early.Add(2 * time.Hour)
	if err := os.WriteFile(handoff, []byte("state"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(handoff, early, early); err != nil {
		t.Fatal(err)
	}
	tr := filepath.Join(root, "transcript.jsonl")
	if err := os.WriteFile(tr, []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}
	// A session that ended past the handoff line, with the handoff older.
	p := Project{Path: "m/h", CtxPeak: 90, LastSessionAt: late}
	on := handoffCfg{Enabled: true, DailyCap: 4}

	for _, c := range []struct {
		name     string
		p        Project
		tr       string
		live     bool
		cfg      handoffCfg
		wantKind string
		wantWhy  string
	}{
		{"acts when everything lines up", p, tr, false, on, "write", "past the handoff line"},
		{"disabled by default", p, tr, false, handoffCfg{DailyCap: 4}, "skip", "handoff.enabled is unset"},
		{"another live session", p, tr, true, on, "skip", "another session is live"},
		{"handoff already current", Project{Path: "m/h", CtxPeak: 90, LastSessionAt: early.Add(-time.Hour)}, tr, false, on, "skip", "not behind the work"},
		{"small session", Project{Path: "m/h", CtxPeak: 20, LastSessionAt: late}, tr, false, on, "skip", "not behind the work"},
		{"no transcript path", p, "", false, on, "skip", "no transcript path"},
		{"transcript gone", p, filepath.Join(root, "nope.jsonl"), false, on, "skip", "is gone"},
		{"daily cap reached", p, tr, false, handoffCfg{Enabled: true, DailyCap: 2, UsedToday: 2}, "cap", "daily cap 2"},
	} {
		t.Run(c.name, func(t *testing.T) {
			d := handoffDecide(c.p, root, c.tr, c.live, c.cfg)
			if d.Kind != c.wantKind || !strings.Contains(d.Reason, c.wantWhy) {
				t.Fatalf("got %s/%q, want %s containing %q", d.Kind, d.Reason, c.wantKind, c.wantWhy)
			}
		})
	}
}

// The disabled path must still say what it would have done: that log is the
// only evidence available before the feature is trusted enough to switch on.
func TestHandoffDecideLogsWhatItWouldDo(t *testing.T) {
	root := t.TempDir()
	dir := mk(t, root, "m/h")
	if err := os.WriteFile(filepath.Join(dir, "HANDOFF.md"), []byte("s"), 0o644); err != nil {
		t.Fatal(err)
	}
	old := time.Date(2026, 9, 13, 9, 0, 0, 0, time.UTC)
	if err := os.Chtimes(filepath.Join(dir, "HANDOFF.md"), old, old); err != nil {
		t.Fatal(err)
	}
	tr := filepath.Join(root, "t.jsonl")
	if err := os.WriteFile(tr, []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}
	d := handoffDecide(Project{Path: "m/h", CtxPeak: 80, LastSessionAt: old.Add(time.Hour)}, root, tr, false, handoffCfg{DailyCap: 4})
	if d.Kind != "skip" || !strings.Contains(d.Reason, "would write") {
		t.Fatalf("a disabled run must report the decision it withheld, got %s/%q", d.Kind, d.Reason)
	}
	if !strings.Contains(d.String(), "m/h") {
		t.Fatalf("log line should name the project: %q", d.String())
	}
}
