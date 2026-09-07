package main

import (
	"bytes"
	"strings"
	"testing"
	"time"
)

func TestRenderDemo(t *testing.T) {
	r := demoReport(ts("2026-09-07T14:32:07Z"), themes["dark"])
	var buf bytes.Buffer
	if err := renderReport(&buf, r); err != nil {
		t.Fatal(err)
	}
	s := buf.String()
	for _, want := range []string{
		"<!doctype html>", "How many things should I run at once?", "What did I finish, what is still open?",
		"What friction should I remove?", "What is waiting on me right now?", "demo data", "Bash(gh pr view *)",
		`class="chip other demo"`, "away time", `href="https://github.com/matchi/matchi-web/pull/401"`,
		"Court booking: block double-submit", "reviewer waiting · matchi/matchi-web#401", "(1 not shown)",
		`href="report-7d.html"`, `class="rng on" href="report-30d.html"`, "#0f1113",
	} {
		if !strings.Contains(s, want) {
			t.Fatalf("missing %q", want)
		}
	}
	for _, bad := range []string{"<no value>", "NaN", "ZgotmplZ", "%!"} {
		if strings.Contains(s, bad) {
			t.Fatalf("template leaked %q", bad)
		}
	}
	// one hatched pattern per strip, ids unique
	if n := strings.Count(s, `<pattern id="away-`); n != 6 { // 5 strips + legend swatch
		t.Fatalf("patterns = %d", n)
	}
	if len(s) > 600_000 {
		t.Fatalf("report too large: %d bytes", len(s))
	}
}

func TestRenderEmptyStore(t *testing.T) {
	s := openTestStore(t)
	now := time.Now()
	raw, err := loadReportData(s, now.AddDate(0, 0, -30), now)
	if err != nil {
		t.Fatal(err)
	}
	r := computeReport(raw, "30d", now.AddDate(0, 0, -30), now, themes["light"])
	var buf bytes.Buffer
	if err := renderReport(&buf, r); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	if !strings.Contains(out, "no events in range") || !strings.Contains(out, "#f3f3f0") {
		t.Fatal("empty state or light theme missing")
	}
	if strings.Contains(out, "NaN") || strings.Contains(out, "ZgotmplZ") {
		t.Fatal("leak")
	}
}

func TestDemoStripDeterministic(t *testing.T) {
	a, b := demoStrip("you", 100), demoStrip("you", 100)
	total := 0
	for i := range a {
		if a[i] != b[i] {
			t.Fatal("not deterministic")
		}
		total += a[i].Mins
	}
	if total != 60 || a[len(a)-1].State != "you" {
		t.Fatalf("%+v", a)
	}
	// the away window shows up
	away := 0
	for _, s := range a {
		if s.State == "away" {
			away += s.Mins
		}
	}
	if away == 0 {
		t.Fatalf("no away minutes: %+v", a)
	}
}
