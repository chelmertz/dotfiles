package main

import (
	"errors"
	"io/fs"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestRows(t *testing.T) {
	ps := []Project{
		{Path: "m/reputation", Name: "reputation", Label: "matchi", Ball: "you"},
		{Path: "m/dependabot", Name: "dependabot", Label: "matchi", Ball: "claude"},
		{Path: "m/stale", Name: "stale", Label: "matchi", Ball: "you"}, // no window: stale state, shown closed
		{Path: "personal/health", Name: "health", Label: "personal"},
		{Path: "oss/a&b", Name: "a&b", Label: "oss"},
	}
	open := map[string]bool{"p:m/dependabot": true, "p:m/reputation": true, "p:personal/health": true}
	rows := Rows(ps, open, false)
	var lines, paths, icons []string
	for _, r := range rows {
		lines = append(lines, r.Text)
		paths = append(paths, r.Path)
		icons = append(icons, r.Icon)
	}
	// Columns are tab-separated; the rofi theme's tab-stops align them.
	muted := func(l string) string { return `<span alpha="45%">` + l + `</span>` }
	state := func(s string) string { return "\t" + `<span alpha="60%">` + s + `</span>` }
	wantLines := []string{
		"reputation\t" + muted("matchi") + state("finished, waiting for you"),
		"dependabot\t" + muted("matchi") + state("working"),
		"stale\t" + muted("matchi"), // closed: no state text either
		"health\t" + muted("personal") + state("terminal open, no Claude"),
		"a&amp;b\t" + muted("oss"),
	}
	wantIcons := []string{"you", "claude", "", "idle", ""}
	wantPaths := []string{"m/reputation", "m/dependabot", "m/stale", "personal/health", "oss/a&b"}
	if !reflect.DeepEqual(lines, wantLines) {
		t.Fatalf("lines %q", lines)
	}
	if !reflect.DeepEqual(paths, wantPaths) {
		t.Fatalf("paths %q", paths)
	}
	if !reflect.DeepEqual(icons, wantIcons) {
		t.Fatalf("icons %q", icons)
	}
}

func TestStateText(t *testing.T) {
	now := ts("2026-09-08T10:00:00Z")
	since := now.Add(-2*time.Hour - 14*time.Minute)
	cases := []struct {
		p    Project
		open bool
		want string
	}{
		{Project{Ball: "you", Reason: "stop", Since: since}, true, "finished, waiting for you · 2h 14m"},
		{Project{Ball: "you", Reason: "idle_prompt", Since: since}, true, "finished, waiting for you · 2h 14m"},
		{Project{Ball: "you", Reason: "question", Since: since}, true, "asked you a question · 2h 14m"},
		{Project{Ball: "you", Reason: "permission_prompt", Since: since}, true, "needs your permission · 2h 14m"},
		{Project{Ball: "you", Reason: "elicitation_dialog", Since: since}, true, "asked you for input · 2h 14m"},
		{Project{Ball: "claude", Since: now.Add(-37 * time.Second)}, true, "working · 37s"},
		{Project{Review: true}, false, "reviewer waiting for your reply"},
		{Project{Snoozed: true}, false, "postponed"},
		{Project{}, true, "terminal open, no Claude"},
		{Project{}, false, ""},
		{Project{Ball: "you", Reason: "stop", Since: since}, false, ""}, // stale state, no window
	}
	for _, c := range cases {
		if got := stateText(c.p, c.open, now); got != c.want {
			t.Errorf("%+v open=%v: got %q want %q", c.p, c.open, got, c.want)
		}
	}
	rows := rowsAt([]Project{{Path: "m/a", Name: "a", Label: "matchi", Ball: "claude", Since: now.Add(-time.Minute)}}, map[string]bool{"p:m/a": true}, false, now)
	if want := "a\t" + `<span alpha="45%">matchi</span>` + "\t" + `<span alpha="60%">working · 1m 00s</span>`; rows[0].Text != want {
		t.Fatalf("got %q", rows[0].Text)
	}
}

func TestRowsDescriptionSubtitle(t *testing.T) {
	rows := Rows([]Project{{Path: "m/a", Name: "a", Label: "matchi", Description: "keep <deps> fresh"}}, nil, false)
	want := "a\t" + `<span alpha="45%">matchi</span>` + "\n" + `<span size="small" alpha="60%">keep &lt;deps&gt; fresh</span>`
	if rows[0].Text != want {
		t.Fatalf("got %q", rows[0].Text)
	}
}

func TestRowsEmpty(t *testing.T) {
	if got := Rows(nil, nil, false); len(got) != 0 {
		t.Fatalf("got %+v", got)
	}
}

// The drift threshold is a literal in two languages and no compiler checks
// either against the other. Both assertions are against the number 75 written
// out here, not against each other: a test that compares driftCtxPct to a
// value parsed out of the script would still pass if someone changed both, and
// a test that compares a constant to itself passes through the rename it was
// meant to catch.
func TestStatuslineDriftThresholdMatches(t *testing.T) {
	if driftCtxPct != 75 {
		t.Fatalf("driftCtxPct is %d; statusline.sh turns /handoff on at 75, so change both or neither", driftCtxPct)
	}
	b, err := os.ReadFile("../claude/statusline.sh")
	if errors.Is(err, fs.ErrNotExist) {
		// buildGoModule copies only this subdirectory into the sandbox, the
		// same reason TestLinksTimerIntervalMatchesStaleness skips there. It
		// still runs on every local `go test ./...`, the gate before a switch.
		t.Skip("claude/ not in the build sandbox; run this from the repo checkout")
	}
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), "handoff_at=75") {
		t.Fatal("claude/statusline.sh no longer sets handoff_at=75; retune driftCtxPct in rows.go to match, or the launcher flags projects the statusline never nagged about")
	}
}

func TestRowsShowDrift(t *testing.T) {
	now := time.Date(2026, 9, 13, 12, 0, 0, 0, time.UTC)
	ps := []Project{
		{Path: "m/a", Name: "a", Label: "matchi", Drifted: true},
		// A pending state outranks it: drift is about a file, the ball is
		// about someone waiting.
		{Path: "m/b", Name: "b", Label: "matchi", Drifted: true, Ball: "you", Reason: "question"},
		{Path: "m/c", Name: "c", Label: "matchi"},
	}
	rows := rowsAt(ps, map[string]bool{tagFor("m/b"): true}, false, now)
	if len(rows) != 3 {
		t.Fatalf("rows %d", len(rows))
	}
	if !strings.Contains(rows[0].Text, "handoff owed") {
		t.Fatalf("drifted project has no marker: %q", rows[0].Text)
	}
	if strings.Contains(rows[1].Text, "handoff owed") || !strings.Contains(rows[1].Text, "asked you a question") {
		t.Fatalf("a pending question should outrank drift: %q", rows[1].Text)
	}
	if strings.Contains(rows[2].Text, "handoff owed") {
		t.Fatalf("undrifted project marked: %q", rows[2].Text)
	}
}
