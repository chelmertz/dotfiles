package main

import (
	"os"
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
	if err := briefSession(s, t.TempDir(), "/etc", time.Now(), &b); err != nil {
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
