package main

import (
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const sampleHandoff = `# demo handoff

Updated 2026-09-11.
Last action: split the handoff into ARCHITECTURE, GOTCHAS and JOURNAL.

## Now (re-check before trusting)

- something true

## Next

Progress: 7/24.

- [x] done one
- [ ] Drop the ceiling to ` + "`200k`" + ` once sessions are routinely
      short. One line in settings.json.
- [ ] a later item

## Open decisions

- **Nine commits sit unpushed.** Your call.
- When to drop the ceiling.

## Unverified

- Whether autoCompactWindow affects running sessions.
- A real tend round.
- The F5 flow end to end.
`

func TestParseHandoff(t *testing.T) {
	f := parseHandoff(sampleHandoff, 2*time.Hour)
	if !f.Present {
		t.Fatal("want Present")
	}
	if f.LastAction != "split the handoff into ARCHITECTURE, GOTCHAS and JOURNAL." {
		t.Errorf("LastAction = %q", f.LastAction)
	}
	if f.Done != 7 || f.Total != 24 || !f.HasCount {
		t.Errorf("progress = %d/%d (%v)", f.Done, f.Total, f.HasCount)
	}
	// the wrapped continuation line is folded back into one sentence, and the
	// checked item above it is skipped
	want := "Drop the ceiling to `200k` once sessions are routinely short. One line in settings.json."
	if f.NextStep != want {
		t.Errorf("NextStep = %q, want %q", f.NextStep, want)
	}
	if f.Decisions != 2 {
		t.Errorf("Decisions = %d, want 2", f.Decisions)
	}
	if f.Unverified != 3 {
		t.Errorf("Unverified = %d, want 3", f.Unverified)
	}
}

// A handoff missing every optional section still parses: the layout is a
// convention, and half the ~/p projects have not been backfilled onto it.
func TestParseHandoffSparse(t *testing.T) {
	f := parseHandoff("# bare\n\n## Now\n\n- a thing\n", time.Minute)
	if !f.Present || f.HasCount || f.NextStep != "" || f.Decisions != 0 || f.Unverified != 0 {
		t.Errorf("sparse handoff = %+v", f)
	}
}

// "## Next" must not swallow the sections after it.
func TestSectionStopsAtNextHeading(t *testing.T) {
	if got := mdSection(sampleHandoff, "Open decisions"); strings.Contains(got, "Unverified") {
		t.Errorf("section bled into the next heading: %q", got)
	}
}

func TestAgeText(t *testing.T) {
	for _, c := range []struct {
		d    time.Duration
		want string
	}{
		{30 * time.Second, "just now"},
		{90 * time.Second, "1m"},
		{2 * time.Hour, "2h"},
		{51 * time.Hour, "2d"},
	} {
		if got := ageText(c.d); got != c.want {
			t.Errorf("ageText(%s) = %q, want %q", c.d, got, c.want)
		}
	}
}

func TestRenderSessionBriefQuietWhenNothingChanged(t *testing.T) {
	var b strings.Builder
	renderSessionBrief("personal/demo", parseHandoff(sampleHandoff, 2*time.Hour), nil, "", &b)
	got := b.String()
	if strings.Contains(got, "/catchup") {
		t.Errorf("hinted /catchup with no changes:\n%s", got)
	}
	if strings.Contains(got, "Changed since") {
		t.Errorf("printed an empty changes block:\n%s", got)
	}
	for _, want := range []string{"handoff written 2h ago", "Progress:    7/24", "Decisions:   2 waiting"} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in:\n%s", want, got)
		}
	}
}

// "just now" already reads as a time; the header must not suffix it.
func TestRenderSessionBriefFreshHandoffReadsAsTime(t *testing.T) {
	var b strings.Builder
	renderSessionBrief("personal/demo", parseHandoff(sampleHandoff, 20*time.Second), nil, "", &b)
	if strings.Contains(b.String(), "just now ago") {
		t.Errorf("header doubled the suffix:\n%s", b.String())
	}
	if !strings.Contains(b.String(), "handoff written just now") {
		t.Errorf("missing the fresh-handoff header:\n%s", b.String())
	}
}

func TestRenderSessionBriefHintsWhenChanged(t *testing.T) {
	var b strings.Builder
	changes := []linkChange{{"https://github.com/o/r/pull/41", "merged 1h ago"}}
	renderSessionBrief("personal/demo", parseHandoff(sampleHandoff, 2*time.Hour), changes, "", &b)
	got := b.String()
	if !strings.Contains(got, "https://github.com/o/r/pull/41 merged 1h ago") {
		t.Errorf("missing the change:\n%s", got)
	}
	if !strings.Contains(got, "run /catchup") {
		t.Errorf("missing the /catchup hint:\n%s", got)
	}
}

func TestChangedSince(t *testing.T) {
	s := openTestStore(t)
	if err := s.UpsertProjects([]Found{{Namespace: "personal", Name: "demo", Path: "personal/demo"}}); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 9, 11, 18, 0, 0, 0, time.UTC)
	since := now.Add(-2 * time.Hour) // when the handoff was written
	rfc := func(d time.Duration) string { return now.Add(d).UTC().Format(time.RFC3339) }
	for _, u := range []string{"u/merged", "u/stale", "u/review", "u/checks", "u/active"} {
		if err := s.AddLink("personal/demo", u); err != nil {
			t.Fatal(err)
		}
	}
	exec := func(q string, args ...any) {
		t.Helper()
		if _, err := s.db.Exec(q, args...); err != nil {
			t.Fatal(err)
		}
	}
	exec(`update link set merged = 1, closed_at = ? where url = 'u/merged'`, rfc(-time.Hour))
	// merged before the handoff was written: already known, not a change
	exec(`update link set merged = 1, closed_at = ? where url = 'u/stale'`, rfc(-5*time.Hour))
	exec(`update link set action_needed = 1, detail = '2 unresolved threads', elly_updated_at = ? where url = 'u/review'`, rfc(-30*time.Minute))
	exec(`update link set check_state = 'failure', check_at = ? where url = 'u/checks'`, rfc(-10*time.Minute))
	exec(`update link set github_updated_at = ?, last_commenter = 'adam' where url = 'u/active'`, rfc(-30*time.Second))

	got, err := changedSince(s.db, "personal/demo", since, now)
	if err != nil {
		t.Fatal(err)
	}
	var lines []string
	for _, c := range got {
		lines = append(lines, c.URL+" "+c.What)
	}
	want := []string{
		"u/merged merged 1h ago",
		"u/review 2 unresolved threads, waiting on you since 30m ago",
		"u/checks checks failing 10m ago",
		"u/active new activity from adam just now",
	}
	if strings.Join(lines, "\n") != strings.Join(want, "\n") {
		t.Errorf("changes:\n%s\nwant:\n%s", strings.Join(lines, "\n"), strings.Join(want, "\n"))
	}
}

func TestLinksStalenessSilentWhenFresh(t *testing.T) {
	s := openTestStore(t)
	now := time.Now()
	if err := s.kvSet("links.last_refresh", now.Add(-3*time.Minute).UTC().Format(time.RFC3339)); err != nil {
		t.Fatal(err)
	}
	if got := linksStaleness(s, now); got != "" {
		t.Errorf("fresh data spoke up: %q", got)
	}
	if err := s.kvSet("links.last_refresh", now.Add(-47*time.Minute).UTC().Format(time.RFC3339)); err != nil {
		t.Fatal(err)
	}
	if got := linksStaleness(s, now); !strings.Contains(got, "47m old") {
		t.Errorf("stale data = %q, want it to name the age", got)
	}
}

// A cwd outside ~/p prints nothing at all, so the hook can be registered
// globally without noise in every other repo on the machine.
func TestBriefSessionSilentOutsideProjects(t *testing.T) {
	s := openTestStore(t)
	var b strings.Builder
	if err := briefSession(s, t.TempDir(), "/etc", false, time.Now(), &b); err != nil {
		t.Fatal(err)
	}
	if b.String() != "" {
		t.Errorf("spoke outside ~/p: %q", b.String())
	}
}

// linksStaleAfter must stay above the timer's own interval, or the brief
// reports the timer as broken every time it is merely between ticks. The
// literal lives in nix/p-launcher.nix, which no compiler checks against this
// file, so assert against the literal rather than against a shared constant.
func TestLinksTimerIntervalMatchesStaleness(t *testing.T) {
	b, err := os.ReadFile("../nix/p-launcher.nix")
	if errors.Is(err, fs.ErrNotExist) {
		// buildGoModule copies only this subdirectory into the sandbox, so
		// the nix tree is out of reach there. The check still runs on every
		// local `go test ./...`, which is the gate before a switch.
		t.Skip("nix/ not in the build sandbox; run this from the repo checkout")
	}
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), `OnUnitActiveSec = "10m"`) {
		t.Fatalf(`nix/p-launcher.nix no longer sets OnUnitActiveSec = "10m"; retune linksStaleAfter (now %s) in session_brief.go`, linksStaleAfter)
	}
	if linksStaleAfter <= 10*time.Minute {
		t.Errorf("linksStaleAfter = %s, must exceed the 10m timer interval", linksStaleAfter)
	}
}

// Called by a hook, the brief must return JSON carrying the same text twice:
// systemMessage is what the user sees in the terminal, additionalContext is
// what the model reads. Plain stdout reaches only the model.
func TestBriefSessionHookJSON(t *testing.T) {
	s := openTestStore(t)
	root := t.TempDir()
	dir := filepath.Join(root, "personal", "demo")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "HANDOFF.md"), []byte(sampleHandoff), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := s.UpsertProjects([]Found{{Namespace: "personal", Name: "demo", Path: "personal/demo"}}); err != nil {
		t.Fatal(err)
	}
	var b strings.Builder
	if err := briefSession(s, root, dir, true, time.Now(), &b); err != nil {
		t.Fatal(err)
	}
	var got hookBrief
	if err := json.Unmarshal([]byte(b.String()), &got); err != nil {
		t.Fatalf("hook output is not JSON: %v\n%s", err, b.String())
	}
	if got.Specific.HookEventName != "SessionStart" {
		t.Errorf("hookEventName = %q", got.Specific.HookEventName)
	}
	if got.SystemMessage == "" || got.SystemMessage != got.Specific.AdditionalContext {
		t.Errorf("user and model must get the same text:\n%q\n%q", got.SystemMessage, got.Specific.AdditionalContext)
	}
	if !strings.Contains(got.SystemMessage, "Progress:    7/24") {
		t.Errorf("systemMessage missing the brief:\n%s", got.SystemMessage)
	}
	// a human at a shell gets plain text, not JSON
	var plain strings.Builder
	if err := briefSession(s, root, dir, false, time.Now(), &plain); err != nil {
		t.Fatal(err)
	}
	if strings.HasPrefix(strings.TrimSpace(plain.String()), "{") {
		t.Errorf("plain mode emitted JSON:\n%s", plain.String())
	}
}

// The defect: the window started at HANDOFF.md's mtime, which /handoff itself
// writes, so handing off moved the window past anything that had just
// happened. On 2026-09-14 two PRs merged at 16:04Z, a handoff was written at
// 16:15Z, and the next brief reported nothing changed. The window now starts
// at the last time links were actually checked, which only /catchup advances.
func TestBriefWindowIsNotResetByWritingTheHandoff(t *testing.T) {
	s := openTestStore(t)
	if err := s.UpsertProjects([]Found{{Namespace: "m", Name: "demo", Path: "m/demo"}}); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 9, 14, 16, 20, 0, 0, time.UTC)
	handoffAge := 5 * time.Minute // written at 16:15Z, after the merge

	// Nothing checked yet: fall back to the handoff's own age, as before.
	if got := briefWindow(s, "m/demo", handoffAge, now); !got.Equal(now.Add(-handoffAge)) {
		t.Errorf("with no check recorded, window = %v, want the handoff age %v", got, now.Add(-handoffAge))
	}

	// Links last actually checked yesterday: the window reaches back to then,
	// so a merge at 16:04Z is inside it.
	seen := time.Date(2026, 9, 13, 9, 0, 0, 0, time.UTC)
	if err := s.SetLinksSeen("m/demo", seen); err != nil {
		t.Fatal(err)
	}
	got := briefWindow(s, "m/demo", handoffAge, now)
	if !got.Equal(seen) {
		t.Fatalf("window = %v, want the last check %v", got, seen)
	}
	merged := time.Date(2026, 9, 14, 16, 4, 40, 0, time.UTC)
	if !merged.After(got) {
		t.Errorf("a merge at %v still falls outside the window starting %v", merged, got)
	}
}

// A catch-up that just verified every PR narrows the window to now, so the
// next brief is quiet until something actually moves.
func TestSetLinksSeenNarrowsTheWindow(t *testing.T) {
	s := openTestStore(t)
	if err := s.UpsertProjects([]Found{{Namespace: "m", Name: "demo", Path: "m/demo"}}); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 9, 14, 16, 20, 0, 0, time.UTC)
	if err := s.SetLinksSeen("m/demo", now); err != nil {
		t.Fatal(err)
	}
	if got := briefWindow(s, "m/demo", 48*time.Hour, now); !got.Equal(now) {
		t.Errorf("window = %v, want %v", got, now)
	}
}

// The catchup skill tells a session to run `p-launcher links seen <ns/name>`,
// and that command is the only thing that narrows the brief's window. Nothing
// compiles the skill, so a rename here would leave the skill instructing a
// command that no longer exists and every window silently wide. Asserts
// against the literal on both sides, not against a shared constant.
func TestCatchupSkillNamesTheLinksSeenCommand(t *testing.T) {
	const literal = "p-launcher links seen"
	b, err := os.ReadFile("../claude/skills/catchup/SKILL.md")
	if errors.Is(err, fs.ErrNotExist) {
		t.Skip("claude/ not in the build sandbox; run this from the repo checkout")
	}
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), literal) {
		t.Fatalf("catchup/SKILL.md no longer says %q, so nothing advances links_seen_at and every brief window stays as wide as the last check", literal)
	}
	src, err := os.ReadFile("main.go")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(src), `args[1] == "seen"`) || !strings.Contains(string(src), "links seen <ns/name>") {
		t.Fatalf("main.go no longer accepts or documents `links seen`, but catchup/SKILL.md still tells sessions to run %q", literal)
	}
}
