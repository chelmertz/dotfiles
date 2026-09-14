package main

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// writeHandoff puts a HANDOFF.md in <root>/<path>/ and returns root.
func writeHandoff(t *testing.T, root, path, body string) {
	t.Helper()
	dir := filepath.Join(root, path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "HANDOFF.md"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// goodHandoff is a file every slot of which the parser reads. Tests bend one
// thing at a time away from it.
const goodHandoff = `# x handoff

Updated 2026-09-14 (session 1).
Last action: wrote the check.

## Now

- something is true.

## Next

Progress: 0/1.

- [ ] do the thing.

## Open decisions

- **Pick a colour.** Blue or green.

## Unverified

- that it works.
`

func kinds(ps []stateProblem) []string {
	var out []string
	for _, p := range ps {
		out = append(out, p.Kind)
	}
	return out
}

func TestCheckStateCleanFileHasNoProblems(t *testing.T) {
	root := t.TempDir()
	writeHandoff(t, root, "ns/clean", goodHandoff)
	if got := checkState(root, "ns/clean"); len(got) != 0 {
		t.Fatalf("clean handoff reported %v", got)
	}
}

func TestCheckStateReportsMissingHandoff(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "ns/bare"), 0o755); err != nil {
		t.Fatal(err)
	}
	got := checkState(root, "ns/bare")
	if len(got) != 1 || got[0].Kind != "missing" {
		t.Fatalf("want one missing problem, got %v", kinds(got))
	}
}

func TestCheckStateReportsHandoffOverCeiling(t *testing.T) {
	root := t.TempDir()
	body := goodHandoff + strings.Repeat("- filler.\n", handoffCeiling)
	writeHandoff(t, root, "ns/fat", body)
	got := checkState(root, "ns/fat")
	if len(got) != 1 || got[0].Kind != "oversized" {
		t.Fatalf("want one oversized problem, got %v", kinds(got))
	}
	// Both numbers have to be in the detail or the reader cannot tell how far
	// over it is, which is the only thing that decides whether to act now.
	n := strconv.Itoa(len(strings.Split(strings.TrimRight(body, "\n"), "\n")))
	if !strings.Contains(got[0].Detail, n) || !strings.Contains(got[0].Detail, strconv.Itoa(handoffCeiling)) {
		t.Fatalf("detail %q names neither the size %s nor the ceiling %d", got[0].Detail, n, handoffCeiling)
	}
}

func TestCheckStateAtCeilingIsNotOversized(t *testing.T) {
	root := t.TempDir()
	writeHandoff(t, root, "ns/exact", strings.Repeat("x\n", handoffCeiling))
	for _, p := range checkState(root, "ns/exact") {
		if p.Kind == "oversized" {
			t.Fatalf("%d lines is the ceiling, not over it", handoffCeiling)
		}
	}
}

// The two traps GOTCHAS.md records, plus the two its reasoning implies.

func TestDroppedSlotsWrappedLastAction(t *testing.T) {
	text := strings.Replace(goodHandoff, "Last action: wrote the check.",
		"Last action: wrote the check, which took the whole session and\nis still not wired into anything.", 1)
	got := droppedSlots(text)
	if len(got) != 1 || got[0].Kind != "dropped" {
		t.Fatalf("want the wrapped Last action flagged, got %v", kinds(got))
	}
	if !strings.Contains(got[0].Detail, "Last action") {
		t.Fatalf("detail %q does not name the slot", got[0].Detail)
	}
}

func TestDroppedSlotsProseBeforeProgressDigits(t *testing.T) {
	// The mfa-superadmin shape: read as no count at all until 2026-09-14.
	text := strings.Replace(goodHandoff, "Progress: 0/1.", "Progress: critical path to a test pilot, 5/13.", 1)
	got := droppedSlots(text)
	if len(got) != 1 || got[0].Kind != "dropped" {
		t.Fatalf("want the unparsed Progress flagged, got %v", kinds(got))
	}
	if !strings.Contains(got[0].Detail, "Progress") {
		t.Fatalf("detail %q does not name the slot", got[0].Detail)
	}
}

func TestDroppedSlotsProseAfterProgressDigitsIsFine(t *testing.T) {
	// Every live handoff does this and the parser reads it correctly.
	text := strings.Replace(goodHandoff, "Progress: 0/1.", "Progress: 6/14 on the critical path to a test pilot.", 1)
	if got := droppedSlots(text); len(got) != 0 {
		t.Fatalf("prose after the digits parses fine; got %v", got)
	}
}

func TestDroppedSlotsIndentedNextItem(t *testing.T) {
	text := strings.Replace(goodHandoff, "- [ ] do the thing.", "  - [ ] do the thing.", 1)
	got := droppedSlots(text)
	if len(got) != 1 || got[0].Kind != "dropped" {
		t.Fatalf("want the indented Next item flagged, got %v", kinds(got))
	}
}

func TestDroppedSlotsIndentedDecisionItems(t *testing.T) {
	text := strings.Replace(goodHandoff, "- **Pick a colour.** Blue or green.", "  - **Pick a colour.** Blue or green.", 1)
	got := droppedSlots(text)
	if len(got) != 1 || got[0].Kind != "dropped" {
		t.Fatalf("want the uncounted decision flagged, got %v", kinds(got))
	}
	if !strings.Contains(got[0].Detail, "Open decisions") {
		t.Fatalf("detail %q does not name the section", got[0].Detail)
	}
}

// Every problem needs a row label short enough for rofi's third column, which
// already carries phrases like "reviewer waiting for your reply".
func TestStateProblemRowLabelsAreShort(t *testing.T) {
	for _, k := range []string{"missing", "oversized", "dropped"} {
		s := stateProblem{Kind: k}.short()
		if s == "" {
			t.Fatalf("kind %q has no row label", k)
		}
		if len(s) > 24 {
			t.Fatalf("row label %q is %d chars, too long for the row", s, len(s))
		}
	}
}

// The ceiling is prose in the project-state skill and a number here. Assert
// against the literal in that file, not against the constant, so a rename on
// either side cannot pass by comparing a value to itself.
func TestHandoffCeilingMatchesProjectStateSkill(t *testing.T) {
	if handoffCeiling != 120 {
		t.Fatalf("handoffCeiling is %d; the project-state skill says ~120, so change both or neither", handoffCeiling)
	}
	b, err := os.ReadFile("../claude/skills/project-state/SKILL.md")
	if errors.Is(err, fs.ErrNotExist) {
		// buildGoModule copies only this subdirectory into the sandbox, the
		// same reason TestStatuslineDriftThresholdMatches skips there.
		t.Skip("claude/ not in the build sandbox; run this from the repo checkout")
	}
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), "ceiling ~120 lines") {
		t.Fatal("project-state/SKILL.md no longer says \"ceiling ~120 lines\"; retune handoffCeiling in statecheck.go to match, or the launcher nags about a ceiling the skill never set")
	}
}

// Severity order, because the row shows only the first: a slot the brief
// silently drops misinforms every session, an oversized file only nags.
func TestCheckStateOrdersDroppedBeforeOversized(t *testing.T) {
	root := t.TempDir()
	body := strings.Replace(goodHandoff, "Progress: 0/1.", "Progress: two of five left, 3/5.", 1) +
		strings.Repeat("- filler.\n", handoffCeiling)
	writeHandoff(t, root, "ns/both", body)
	got := kinds(checkState(root, "ns/both"))
	if len(got) != 2 || got[0] != "dropped" || got[1] != "oversized" {
		t.Fatalf("want [dropped oversized], got %v", got)
	}
}

func TestScanStateListsOneRowPerProblemAcrossProjects(t *testing.T) {
	root := t.TempDir()
	writeHandoff(t, root, "ns/clean", goodHandoff)
	if err := os.MkdirAll(filepath.Join(root, "ns/bare"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeHandoff(t, root, "zz/both",
		strings.Replace(goodHandoff, "Progress: 0/1.", "Progress: three left, 1/4.", 1)+
			strings.Repeat("- filler.\n", handoffCeiling))

	rows, affected, scanned := scanState(root, nil)
	if scanned != 3 {
		t.Fatalf("scanned %d projects, want 3", scanned)
	}
	if affected != 2 {
		t.Fatalf("%d projects with problems, want 2 (clean one must not count)", affected)
	}
	// Sorted by path so the table is stable between runs, and every problem
	// gets its own row: a project with two is two lines of work, not one.
	want := []StateFileRow{
		{Path: "ns/bare", Kind: "missing"},
		{Path: "zz/both", Kind: "dropped"},
		{Path: "zz/both", Kind: "oversized"},
	}
	if len(rows) != len(want) {
		t.Fatalf("got %d rows, want %d: %v", len(rows), len(want), rows)
	}
	for i, w := range want {
		if rows[i].Path != w.Path || rows[i].Kind != w.Kind {
			t.Fatalf("row %d is %s/%s, want %s/%s", i, rows[i].Path, rows[i].Kind, w.Path, w.Kind)
		}
		if rows[i].Detail == "" {
			t.Fatalf("row %d has no detail to act on", i)
		}
	}
}

// The friction panel is a fixed height, so the table is capped the way the
// live section already caps its rows. Worst first, so the cap never hides a
// dropped slot behind a merely oversized file.
func TestCapStateRowsKeepsWorstAndCountsTheRest(t *testing.T) {
	rows := []StateFileRow{
		{Path: "a/1", Kind: "oversized"},
		{Path: "a/2", Kind: "missing"},
		{Path: "a/3", Kind: "oversized"},
		{Path: "a/4", Kind: "dropped"},
	}
	kept, hidden := capStateRows(rows, 2)
	if hidden != 2 {
		t.Fatalf("hidden %d, want 2", hidden)
	}
	if kept[0].Kind != "dropped" || kept[1].Kind != "missing" {
		t.Fatalf("kept %v, want the dropped slot then the missing handoff", kinds2(kept))
	}
}

func TestCapStateRowsUnderTheCapHidesNothing(t *testing.T) {
	rows := []StateFileRow{{Path: "a/1", Kind: "missing"}}
	kept, hidden := capStateRows(rows, 6)
	if hidden != 0 || len(kept) != 1 {
		t.Fatalf("kept %d hidden %d, want 1 and 0", len(kept), hidden)
	}
}

func kinds2(rs []StateFileRow) []string {
	var out []string
	for _, r := range rs {
		out = append(out, r.Kind)
	}
	return out
}

// Found in personal/1password-systemauth on 2026-09-14: its decisions are an
// ordered list, so mdItems counted none and the brief said zero decisions
// while three sat waiting. Ordered items look like items to every reader
// except the parser.
func TestDroppedSlotsOrderedListItemsAreNotCounted(t *testing.T) {
	text := strings.Replace(goodHandoff, "- **Pick a colour.** Blue or green.", "1. **Pick a colour.** Blue or green.\n2. **Pick a font.** Serif or not.", 1)
	got := droppedSlots(text)
	if len(got) != 1 || got[0].Kind != "dropped" {
		t.Fatalf("want the ordered decisions flagged, got %v", kinds(got))
	}
	if !strings.Contains(got[0].Detail, "Open decisions") {
		t.Fatalf("detail %q does not name the section", got[0].Detail)
	}
}

// An archived project is finished work: its handoff is a record, not a task,
// so nagging about it is noise that never clears. Folders never move on
// archive (lifecycle.go), so Discover still finds it and only the DB knows.
func TestScanStateSkipsArchivedProjects(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "ns/bare"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "ns/done"), 0o755); err != nil {
		t.Fatal(err)
	}
	rows, affected, scanned := scanState(root, map[string]bool{"ns/done": true})
	if scanned != 1 {
		t.Fatalf("scanned %d, want 1: an archived project is not scanned", scanned)
	}
	if affected != 1 || len(rows) != 1 || rows[0].Path != "ns/bare" {
		t.Fatalf("affected %d rows %v", affected, rows)
	}
}

func TestMarkFileStateSkipsArchivedProjects(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "ns/done"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "ns/live"), 0o755); err != nil {
		t.Fatal(err)
	}
	ps := []Project{{Path: "ns/done", Archived: true}, {Path: "ns/live"}}
	markFileState(ps, root, nil)
	if len(ps[0].StateProblems) != 0 {
		t.Fatalf("archived project still nags: %v", ps[0].StateProblems)
	}
	if len(ps[1].StateProblems) != 1 {
		t.Fatalf("live project lost its problem: %v", ps[1].StateProblems)
	}
}
