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
		"Court booking: block double-submit", "reviewer waiting · matchi/matchi-web#401", "(1 not shown)", "auto ×2", "started by tend <b>2</b>",
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

func TestRefreshDue(t *testing.T) {
	now := ts("2026-09-08T10:00:00Z")
	if !refreshDue("", now) || !refreshDue("2026-09-08T09:40:00Z", now) || refreshDue("2026-09-08T09:55:00Z", now) {
		t.Fatal("refreshDue")
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

func TestRenderStateFilesTable(t *testing.T) {
	r := demoReport(ts("2026-09-07T14:32:07Z"), themes["dark"])
	r.StateFiles = []StateFileRow{
		{Path: "m/claude-billing", Kind: "dropped", Detail: "`Last action:` wraps onto the next line"},
		{Path: "personal/health", Kind: "missing", Detail: "no HANDOFF.md"},
	}
	r.StateHidden, r.StateAffected, r.StateScanned = 3, 5, 15
	var buf bytes.Buffer
	if err := renderReport(&buf, r); err != nil {
		t.Fatal(err)
	}
	s := buf.String()
	for _, want := range []string{
		"m/claude-billing", "personal/health", "dropped", "missing",
		"Last action:", // the detail is the whole point: it says what to do
		"5 of 15",      // a denominator, so a backlog is not read as a convention breaking
		"3 not shown",  // the same cap wording the live section uses
	} {
		if !strings.Contains(s, want) {
			t.Fatalf("missing %q", want)
		}
	}
	for _, bad := range []string{"<no value>", "ZgotmplZ", "%!"} {
		if strings.Contains(s, bad) {
			t.Fatalf("template leaked %q", bad)
		}
	}
}

func TestRenderNoStateFilesSaysSo(t *testing.T) {
	r := demoReport(ts("2026-09-07T14:32:07Z"), themes["dark"])
	r.StateFiles, r.StateAffected, r.StateScanned = nil, 0, 15
	var buf bytes.Buffer
	if err := renderReport(&buf, r); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(buf.String(), "all 15 projects") {
		t.Fatal("a clean scan has to say so; a blank panel reads as a check that never ran")
	}
}

func TestDemoReportCarriesStateFiles(t *testing.T) {
	// --demo is what the screenshots and the design review use; a section that
	// is empty there reads as a section that does nothing.
	r := demoReport(ts("2026-09-07T14:32:07Z"), themes["dark"])
	if len(r.StateFiles) == 0 || r.StateScanned == 0 {
		t.Fatalf("demo has %d state rows over %d projects", len(r.StateFiles), r.StateScanned)
	}
	if r.StateAffected == 0 || r.StateAffected > r.StateScanned {
		t.Fatalf("affected %d of scanned %d", r.StateAffected, r.StateScanned)
	}
}
