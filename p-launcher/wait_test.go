package main

import (
	"strings"
	"testing"
	"time"
)

func TestWaitOutcome(t *testing.T) {
	now := ts("2026-10-12T12:00:00Z")
	created := now.Add(-30 * time.Minute)
	base := waitRow{URL: "https://github.com/o/r/pull/1", Cond: "checks", Created: created}
	cases := []struct {
		name    string
		w       waitRow
		outcome string
		pending bool
	}{
		{"checks running", withWait(base, func(w *waitRow) { w.CheckState = "pending" }), "", true},
		{"stale failure from before the wait", withWait(base, func(w *waitRow) { w.CheckState, w.CheckAt = "failure", created.Add(-time.Minute) }), "", false},
		{"failure after the wait", withWait(base, func(w *waitRow) { w.CheckState, w.CheckAt = "failure", created.Add(time.Minute) }), "failure", false},
		{"success seen pending, no completion time", withWait(base, func(w *waitRow) { w.CheckState, w.SawPending = "success", true }), "success", false},
		{"unknown state", base, "", false},
		{"merged cancels checks", withWait(base, func(w *waitRow) { w.Merged = true; w.CheckState = "pending" }), "merged", false},
		{"closed", withWait(base, func(w *waitRow) { w.Closed = true }), "closed", false},
		{"expired", withWait(base, func(w *waitRow) { w.Created = now.Add(-49 * time.Hour) }), "expired", false},
		{"review by reviewer", withWait(base, func(w *waitRow) { w.Cond, w.LastCommenter, w.LastUpdated = "review", "jd", created.Add(time.Minute) }), "review", false},
		{"review by me", withWait(base, func(w *waitRow) { w.Cond, w.LastCommenter, w.LastUpdated = "review", "me", created.Add(time.Minute) }), "", false},
		{"review by bot", withWait(base, func(w *waitRow) {
			w.Cond, w.LastCommenter, w.LastUpdated = "review", "sonarcloud[bot]", created.Add(time.Minute)
		}), "", false},
		{"review before the wait", withWait(base, func(w *waitRow) { w.Cond, w.LastCommenter, w.LastUpdated = "review", "jd", created.Add(-time.Minute) }), "", false},
		{"merge wait open", withWait(base, func(w *waitRow) { w.Cond = "merge" }), "", false},
		{"merge wait merged", withWait(base, func(w *waitRow) { w.Cond, w.Merged = "merge", true }), "merged", false},
	}
	for _, c := range cases {
		got, pending := waitOutcome(c.w, "me", defaultBots, now)
		if got != c.outcome || pending != c.pending {
			t.Errorf("%s: got (%q, %v), want (%q, %v)", c.name, got, pending, c.outcome, c.pending)
		}
	}
}

func TestWaitActs(t *testing.T) {
	for _, c := range []struct {
		cond, outcome string
		want          bool
	}{
		{"checks", "failure", true}, {"checks", "success", true}, {"review", "review", true},
		{"merge", "merged", true}, {"checks", "merged", false}, {"review", "closed", false}, {"checks", "expired", false},
	} {
		if got := waitActs(c.cond, c.outcome); got != c.want {
			t.Errorf("%s/%s: %v", c.cond, c.outcome, got)
		}
	}
}

func TestAddWaitValidatesAndReplaces(t *testing.T) {
	s := openTestStore(t)
	root := t.TempDir()
	dir := mk(t, root, "m/a")
	if err := s.UpsertProjects(found("m/a")); err != nil {
		t.Fatal(err)
	}
	now := ts("2026-10-12T12:00:00Z")
	url := "https://github.com/o/r/pull/1"
	pending := func(string) (string, time.Time, error) { return "pending", time.Time{}, nil }
	for _, bad := range [][3]string{
		{root, url, "checks"},                              // not inside a project
		{dir, "https://github.com/o/r/issues/1", "checks"}, // not a PR
		{dir, url, "deploy"},                               // unknown condition
	} {
		if _, err := addWait(s, root, bad[0], bad[1], bad[2], "next", pending, now); err == nil {
			t.Errorf("accepted %v", bad)
		}
	}
	if _, err := addWait(s, root, dir, url, "checks", "  ", pending, now); err == nil {
		t.Error("accepted an empty next step")
	}
	if _, err := addWait(s, root, dir, url, "checks", "first", pending, now); err != nil {
		t.Fatal(err)
	}
	if _, err := addWait(s, root, dir, url, "checks", "second", nil, now); err != nil {
		t.Fatal(err)
	}
	ws, err := loadOpenWaits(s)
	if err != nil {
		t.Fatal(err)
	}
	if len(ws) != 1 || ws[0].Note != "second" {
		t.Fatalf("want one open wait with the newer note, got %+v", ws)
	}
	var links int
	if err := s.db.QueryRow(`select count(*) from link where url = ?`, url).Scan(&links); err != nil || links != 1 {
		t.Fatalf("link not registered: %d %v", links, err)
	}
}

// One failed check after a registered wait opens exactly one session: the
// wait's. tend's own pass sees the same red check and must stand down.
func TestRunTendWaitActsOnceAndTendStandsDown(t *testing.T) {
	s := openTestStore(t)
	root := t.TempDir()
	dir := mk(t, root, "m/a")
	if err := s.UpsertProjects(found("m/a")); err != nil {
		t.Fatal(err)
	}
	now := ts("2026-10-12T12:00:00Z")
	url := "https://github.com/o/r/pull/1"
	if _, err := addWait(s, root, dir, url, "checks", "fix the lint failure", nil, now.Add(-20*time.Minute)); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`update link set author = 'me', check_state = 'failure', check_at = ?, github_updated_at = ? where url = ?`,
		now.Add(-5*time.Minute).UTC().Format(time.RFC3339), now.Add(-30*time.Minute).UTC().Format(time.RFC3339), url); err != nil {
		t.Fatal(err)
	}
	if err := s.kvSet("tend.enabled", "1"); err != nil {
		t.Fatal(err)
	}
	var prompts, notes []string
	deps := tendDeps{now: now, me: "me",
		launch: func(dir, tag, prompt string) error { prompts = append(prompts, prompt); return nil },
		notify: func(m string) { notes = append(notes, m) },
		checks: func(string) (string, time.Time, error) { return "failure", now.Add(-5 * time.Minute), nil },
	}
	var out strings.Builder
	if err := runTend(s, root, false, 2, deps, nil, &out); err != nil {
		t.Fatal(err)
	}
	if len(prompts) != 1 || !strings.Contains(prompts[0], "checks failed") || !strings.Contains(prompts[0], "fix the lint failure") {
		t.Fatalf("want one wait session, got %d: %q\n%s", len(prompts), prompts, out.String())
	}
	if !strings.Contains(prompts[0], "--json state") {
		t.Error("checks prompt lacks the closed-PR guard")
	}
	if len(notes) != 0 {
		t.Errorf("unexpected notifications: %v", notes)
	}
	ws, _ := loadOpenWaits(s)
	if len(ws) != 0 {
		t.Errorf("wait still open: %+v", ws)
	}
	// a second run has nothing new: no session, no notification
	prompts = nil
	out.Reset()
	if err := runTend(s, root, false, 2, deps, nil, &out); err != nil {
		t.Fatal(err)
	}
	if len(prompts) != 0 {
		t.Fatalf("second run opened %d sessions\n%s", len(prompts), out.String())
	}
}

func TestRunWaitsNotifiesWhenLiveOrNothingToFix(t *testing.T) {
	s := openTestStore(t)
	root := t.TempDir()
	dir := mk(t, root, "m/a")
	if err := s.UpsertProjects(found("m/a")); err != nil {
		t.Fatal(err)
	}
	now := ts("2026-10-12T12:00:00Z")
	if _, err := addWait(s, root, dir, "https://github.com/o/r/pull/1", "review", "answer jd", nil, now.Add(-time.Hour)); err != nil {
		t.Fatal(err)
	}
	if _, err := addWait(s, root, dir, "https://github.com/o/r/pull/2", "checks", "rebase", nil, now.Add(-time.Hour)); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`update link set last_commenter = 'jd', github_updated_at = ? where url like '%/1'`, now.Add(-time.Minute).UTC().Format(time.RFC3339)); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`update link set closed_at = ? where url like '%/2'`, now.Add(-time.Minute).UTC().Format(time.RFC3339)); err != nil {
		t.Fatal(err)
	}
	var launched, notes []string
	deps := tendDeps{now: now,
		launch: func(dir, tag, prompt string) error { launched = append(launched, prompt); return nil },
		notify: func(m string) { notes = append(notes, m) },
	}
	cfg := tendCfg{Enabled: true, DailyCap: 4, Me: "me", Bots: defaultBots}
	var out strings.Builder
	if _, err := runWaits(s, root, false, cfg, map[string]bool{"m/a": true}, deps, &out); err != nil {
		t.Fatal(err)
	}
	if len(launched) != 0 || len(notes) != 2 {
		t.Fatalf("launched %d, notified %v\n%s", len(launched), notes, out.String())
	}
	if !strings.Contains(out.String(), "(live session)") || !strings.Contains(out.String(), "the PR was closed") {
		t.Errorf("output lacks the reasons:\n%s", out.String())
	}
}

func withWait(w waitRow, f func(*waitRow)) waitRow {
	f(&w)
	return w
}
