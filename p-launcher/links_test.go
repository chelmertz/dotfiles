package main

import (
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

type linkRow struct {
	URL, Author, Title, GitHubState, Etag, Detail string
	Merged, ActionNeeded                          bool
	OpenedAt, ClosedAt, RefreshedAt               string
	Add, Del, Threads                             int
}

func readLink(t *testing.T, s *Store, url string) linkRow {
	t.Helper()
	var r linkRow
	var merged, action int
	err := s.db.QueryRow(`select url, author, title, github_state, etag, detail, merged, action_needed,
		coalesce(opened_at,''), coalesce(closed_at,''), coalesce(refreshed_at,''), coalesce(additions,0), coalesce(deletions,0), threads_actionable
		from link where url = ?`, url).Scan(&r.URL, &r.Author, &r.Title, &r.GitHubState, &r.Etag, &r.Detail, &merged, &action,
		&r.OpenedAt, &r.ClosedAt, &r.RefreshedAt, &r.Add, &r.Del, &r.Threads)
	if err != nil {
		t.Fatal(err)
	}
	r.Merged, r.ActionNeeded = merged == 1, action == 1
	return r
}

func linksFixture(t *testing.T) (*Store, time.Time) {
	t.Helper()
	s := openTestStore(t)
	if err := s.UpsertProjects(found("m/a")); err != nil {
		t.Fatal(err)
	}
	for _, u := range []string{"https://github.com/o/r/pull/1", "https://github.com/o/r/pull/2"} {
		if err := s.AddLink("m/a", u); err != nil {
			t.Fatal(err)
		}
	}
	return s, ts("2026-09-07T14:00:00Z")
}

func TestRefreshLinksHappy(t *testing.T) {
	s, now := linksFixture(t)
	calls := 0
	deps := linkDeps{me: "me", now: now,
		gh: func(url, etag string) (ghPR, int, string, error) {
			calls++
			if strings.HasSuffix(url, "/1") {
				return ghPR{State: "closed", Merged: true, Title: "One", Body: "body", Author: "me", Add: 10, Del: 2,
					CreatedAt: ts("2026-09-01T10:00:00Z"), ClosedAt: ts("2026-09-03T10:00:00Z"), MergedAt: ts("2026-09-03T10:00:00Z"), UpdatedAt: ts("2026-09-03T11:00:00Z")}, 200, `"e1"`, nil
			}
			return ghPR{State: "open", Title: "Two", Author: "jd", CreatedAt: ts("2026-09-04T10:00:00Z")}, 200, `"e2"`, nil
		},
		elly: func() (map[string]ellyPR, time.Time, error) {
			return map[string]ellyPR{"https://github.com/o/r/pull/2": {ReviewStatus: "", ThreadsActionable: 2, LastUpdated: now.Add(-40 * time.Minute)}}, now.Add(-3 * time.Minute), nil
		},
	}
	res, err := refreshLinks(s, deps)
	if err != nil {
		t.Fatal(err)
	}
	var ellyUpdated string
	if err := s.db.QueryRow(`select coalesce(elly_updated_at,'') from link where url like '%/2'`).Scan(&ellyUpdated); err != nil || ellyUpdated != now.Add(-40*time.Minute).UTC().Format(time.RFC3339) {
		t.Fatalf("elly_updated_at %q %v", ellyUpdated, err)
	}
	if links, err := loadTendLinks(s); err != nil || len(links) != 2 || !links[1].LastUpdated.Equal(now.Add(-40*time.Minute)) {
		t.Fatalf("tend must see elly's time: %+v %v", links, err)
	} else if !links[0].LastUpdated.Equal(ts("2026-09-03T11:00:00Z")) {
		t.Fatalf("a link elly does not list must carry GitHub's updated_at, never our refresh time: %+v", links[0])
	}
	if calls != 2 || res.Refreshed != 2 || res.Failed != 0 || res.EllyStale || res.EllyMissing {
		t.Fatalf("calls=%d %+v", calls, res)
	}
	one := readLink(t, s, "https://github.com/o/r/pull/1")
	if !one.Merged || one.GitHubState != "closed" || one.Author != "me" || one.Title != "One" || one.Add != 10 || one.Etag != `"e1"` ||
		one.OpenedAt != "2026-09-01T10:00:00Z" || one.ClosedAt != "2026-09-03T10:00:00Z" || one.RefreshedAt == "" || one.ActionNeeded {
		t.Fatalf("%+v", one)
	}
	two := readLink(t, s, "https://github.com/o/r/pull/2")
	if two.Merged || !two.ActionNeeded || two.Threads != 2 || two.Detail != "2 unresolved threads" || two.Author != "jd" || two.OpenedAt != "2026-09-04T10:00:00Z" {
		t.Fatalf("%+v", two)
	}
	if v, _ := s.kvGet("elly.last_fetched"); v != now.Add(-3*time.Minute).UTC().Format(time.RFC3339) {
		t.Fatalf("elly.last_fetched %q", v)
	}
	if v, _ := s.kvGet("links.last_refresh"); v == "" {
		t.Fatal("links.last_refresh unset")
	}
	// second run: the merged link is terminal and not fetched again; the open one hits 304
	calls = 0
	deps.gh = func(url, etag string) (ghPR, int, string, error) {
		calls++
		if etag != `"e2"` {
			t.Fatalf("etag not sent: %q", etag)
		}
		return ghPR{}, 304, etag, nil
	}
	deps.now = now.Add(time.Hour)
	res, err = refreshLinks(s, deps)
	if err != nil || calls != 1 || res.Unchanged != 1 || res.Refreshed != 0 {
		t.Fatalf("%v calls=%d %+v", err, calls, res)
	}
	two2 := readLink(t, s, "https://github.com/o/r/pull/2")
	if two2.Title != "Two" || two2.RefreshedAt == two.RefreshedAt {
		t.Fatalf("%+v", two2)
	}
}

func TestRefreshLinksGitHubDown(t *testing.T) {
	s, now := linksFixture(t)
	deps := linkDeps{me: "me", now: now,
		gh:   func(url, etag string) (ghPR, int, string, error) { return ghPR{}, 0, "", errors.New("rate limited") },
		elly: func() (map[string]ellyPR, time.Time, error) { return nil, now, nil },
	}
	res, err := refreshLinks(s, deps)
	if err != nil || res.Failed != 2 || res.Refreshed != 0 {
		t.Fatalf("%v %+v", err, res)
	}
	one := readLink(t, s, "https://github.com/o/r/pull/1")
	if one.Title != "" || one.RefreshedAt != "" || one.GitHubState != "" {
		t.Fatalf("values changed on failure: %+v", one)
	}
	if v, _ := s.kvGet("links.last_error"); !strings.Contains(v, "rate limited") {
		t.Fatalf("last_error %q", v)
	}
}

func TestRefreshLinksEllyMissingOrStale(t *testing.T) {
	s, now := linksFixture(t)
	if _, err := s.db.Exec(`update link set action_needed = 1, detail = 'old'`); err != nil {
		t.Fatal(err)
	}
	gh := func(url, etag string) (ghPR, int, string, error) {
		return ghPR{State: "open", CreatedAt: now.Add(-time.Hour)}, 200, `"e"`, nil
	}
	res, err := refreshLinks(s, linkDeps{me: "me", now: now, gh: gh,
		elly: func() (map[string]ellyPR, time.Time, error) { return nil, time.Time{}, os.ErrNotExist }})
	if err != nil || !res.EllyMissing {
		t.Fatalf("%v %+v", err, res)
	}
	if one := readLink(t, s, "https://github.com/o/r/pull/1"); !one.ActionNeeded || one.Detail != "old" {
		t.Fatalf("verdict must be untouched without elly: %+v", one)
	}
	// stale elly: verdicts are still written (they are elly's latest) but the run is marked stale
	res, err = refreshLinks(s, linkDeps{me: "me", now: now, gh: gh,
		elly: func() (map[string]ellyPR, time.Time, error) { return map[string]ellyPR{}, now.Add(-2 * time.Hour), nil }})
	if err != nil || !res.EllyStale {
		t.Fatalf("%v %+v", err, res)
	}
	if one := readLink(t, s, "https://github.com/o/r/pull/1"); one.ActionNeeded {
		t.Fatalf("elly no longer lists it as open: verdict must clear: %+v", one)
	}
}

func TestApiPath(t *testing.T) {
	cases := map[string]string{
		"https://github.com/o/r/pull/7":    "repos/o/r/pulls/7",
		"https://github.com/o/r/issues/12": "repos/o/r/issues/12",
		"https://github.com/o/r":           "",
	}
	for in, want := range cases {
		got, ok := apiPath(in)
		if (want == "") == ok || got != want {
			t.Errorf("%s: got %q %v", in, got, ok)
		}
	}
}

func TestRefreshIssueLink(t *testing.T) {
	s := openTestStore(t)
	if err := s.UpsertProjects(found("m/a")); err != nil {
		t.Fatal(err)
	}
	if err := s.AddLinkKind("m/a", "https://github.com/o/r/issues/12", "github_issue"); err != nil {
		t.Fatal(err)
	}
	now := ts("2026-09-08T10:00:00Z")
	calls, checks := 0, 0
	deps := linkDeps{me: "me", now: now,
		gh: func(url, etag string) (ghPR, int, string, error) {
			calls++
			return ghPR{State: "closed", Title: "Fix the flaky login", Author: "jd", CreatedAt: now.Add(-48 * time.Hour), ClosedAt: now.Add(-time.Hour)}, 200, `"i1"`, nil
		},
		elly:   func() (map[string]ellyPR, time.Time, error) { return map[string]ellyPR{}, now, nil },
		checks: func(url string) (string, time.Time, error) { checks++; return "success", now, nil },
	}
	if _, err := refreshLinks(s, deps); err != nil {
		t.Fatal(err)
	}
	var state, closed, title, desc string
	if err := s.db.QueryRow(`select l.github_state, coalesce(l.closed_at,''), l.title, p.description from link l join project p on p.id = l.project_id`).Scan(&state, &closed, &title, &desc); err != nil {
		t.Fatal(err)
	}
	if state != "closed" || closed == "" || title != "Fix the flaky login" || desc != "Fix the flaky login" {
		t.Fatalf("%q %q %q %q", state, closed, title, desc)
	}
	if checks != 0 {
		t.Fatal("CI checks must not run for issues")
	}
	// closed is terminal: not fetched again; an existing description is kept
	if err := s.SetDescription("m/a", "my own words"); err != nil {
		t.Fatal(err)
	}
	if _, err := refreshLinks(s, deps); err != nil || calls != 1 {
		t.Fatalf("%v calls=%d", err, calls)
	}
	ps, _ := s.ListProjects()
	if ps[0].Description != "my own words" {
		t.Fatalf("description overwritten: %q", ps[0].Description)
	}
}

func TestEllyVerdict(t *testing.T) {
	cases := []struct {
		pr     ellyPR
		me     string
		need   bool
		detail string
	}{
		{ellyPR{ThreadsActionable: 2}, "me", true, "2 unresolved threads"},
		{ellyPR{ThreadsActionable: 1}, "me", true, "1 unresolved thread"},
		{ellyPR{ReviewStatus: "CHANGES_REQUESTED", Author: "me"}, "me", true, "changes requested"},
		{ellyPR{ReviewStatus: "CHANGES_REQUESTED", Author: "jd"}, "me", false, ""},
		{ellyPR{ReviewStatus: "APPROVED", Author: "me"}, "me", false, ""},
	}
	for _, c := range cases {
		need, detail := ellyVerdict(c.pr, c.me)
		if need != c.need || detail != c.detail {
			t.Errorf("%+v: got %v %q", c.pr, need, detail)
		}
	}
}

func TestFreshnessGateInLoad(t *testing.T) {
	s, now := linksFixture(t)
	if _, err := s.db.Exec(`update link set action_needed = 1`); err != nil {
		t.Fatal(err)
	}
	if err := s.kvSet("elly.last_fetched", now.Add(-45*time.Minute).UTC().Format(time.RFC3339)); err != nil {
		t.Fatal(err)
	}
	raw, err := loadReportDataAt(s, now.AddDate(0, 0, -30), now)
	if err != nil {
		t.Fatal(err)
	}
	for _, l := range raw.Links {
		if l.ActionNeeded {
			t.Fatalf("stale verdict shown: %+v", l)
		}
	}
	if len(raw.Notes) == 0 || !strings.Contains(strings.Join(raw.Notes, " "), "elly") {
		t.Fatalf("no staleness note: %v", raw.Notes)
	}
	if err := s.kvSet("elly.last_fetched", now.Add(-5*time.Minute).UTC().Format(time.RFC3339)); err != nil {
		t.Fatal(err)
	}
	raw, _ = loadReportDataAt(s, now.AddDate(0, 0, -30), now)
	if !raw.Links[0].ActionNeeded {
		t.Fatal("fresh verdict hidden")
	}
}

func TestEllyVerdictRereview(t *testing.T) {
	// nothing unanswered, but the reviewer was never asked to look again
	need, why := ellyVerdict(ellyPR{Author: "me", ReviewStatus: "REVIEW_REQUIRED", RereviewFrom: "adam"}, "me")
	if !need || why != "ask adam to re-review" {
		t.Fatalf("%v %q", need, why)
	}
	// several reviewers: the row has one column, so count them
	if _, why := ellyVerdict(ellyPR{RereviewFrom: "adam,eve"}, "me"); why != "2 reviewers to re-review" && why != "ask 2 reviewers to re-review" {
		t.Fatalf("%q", why)
	}
	// unanswered threads outrank the nudge: answer first, then ask
	if _, why := ellyVerdict(ellyPR{ThreadsActionable: 2, RereviewFrom: "adam"}, "me"); why != "2 unresolved threads" {
		t.Fatalf("%q", why)
	}
	if need, _ := ellyVerdict(ellyPR{RereviewFrom: ""}, "me"); need {
		t.Fatal("no signal must not need action")
	}
}

// The launcher must keep working against an elly that predates the
// rereview_from column: the column is probed, not assumed.
func TestEllyReadOlderSchema(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_DATA_HOME", dir)
	if err := os.MkdirAll(filepath.Join(dir, "elly"), 0o755); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", filepath.Join(dir, "elly", "elly.db"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`create table prs (url text primary key, review_status text, threads_actionable integer, author text, last_updated text, last_pr_commenter text, is_draft integer);
		create table meta (key text, value text);
		insert into prs values ('https://github.com/o/r/pull/1', 'REVIEW_REQUIRED', 2, 'me', '2026-09-08T10:00:00Z', 'adam', 0);
		insert into meta values ('last_fetched', '2026-09-08T10:05:00Z')`); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	prs, fetched, err := ellyRead()
	if err != nil {
		t.Fatalf("an elly missing the newer columns must still be read: %v", err)
	}
	pr := prs["https://github.com/o/r/pull/1"]
	if len(prs) != 1 || pr.ThreadsActionable != 2 || pr.RereviewFrom != "" || fetched.IsZero() {
		t.Fatalf("%+v %v", prs, fetched)
	}
	// and with the column present it is read
	db, err = sql.Open("sqlite", filepath.Join(dir, "elly", "elly.db"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`alter table prs add column rereview_from text not null default ''; update prs set rereview_from = 'adam'`); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	if prs, _, err = ellyRead(); err != nil || prs["https://github.com/o/r/pull/1"].RereviewFrom != "adam" {
		t.Fatalf("%+v %v", prs, err)
	}
}

func TestHandoffRefsFindsEveryGitHubURLOnce(t *testing.T) {
	text := `# handoff

- https://github.com/o/r/pull/3605 is green and awaits a human merge.
- the same PR again: https://github.com/o/r/pull/3605
- an issue, https://github.com/o/r/issues/12, and a non-GitHub
  https://example.com/o/r/pull/9 that is not a ref.
- https://github.com/other/repo/pull/11339 wants marking ready first.
`
	got := handoffRefs(text)
	want := []ghRef{
		{Kind: "github_pr", Owner: "o", Repo: "r", Number: 3605, URL: "https://github.com/o/r/pull/3605"},
		{Kind: "github_issue", Owner: "o", Repo: "r", Number: 12, URL: "https://github.com/o/r/issues/12"},
		{Kind: "github_pr", Owner: "other", Repo: "repo", Number: 11339, URL: "https://github.com/other/repo/pull/11339"},
	}
	if len(got) != len(want) {
		t.Fatalf("got %d refs %v, want %d", len(got), got, len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("ref %d: got %+v, want %+v", i, got[i], want[i])
		}
	}
}

// The defect this closes: a PR URL written into a handoff was never a link
// row, so the brief's "changed since" block could not report it however wide
// the window was. https://github.com/matchiapp/webapp/pull/11339 merged
// unseen on 2026-09-14 for exactly this reason.
func TestAdoptHandoffLinksRegistersPRsTheHandoffNames(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "m/a"), 0o755); err != nil {
		t.Fatal(err)
	}
	handoff := "Next step: merge https://github.com/o/r/pull/11339, still draft.\n"
	if err := os.WriteFile(filepath.Join(root, "m/a", "HANDOFF.md"), []byte(handoff), 0o644); err != nil {
		t.Fatal(err)
	}
	s := openTestStore(t)
	if err := s.UpsertProjects(found("m/a")); err != nil {
		t.Fatal(err)
	}

	n, err := adoptHandoffLinks(s, root)
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("adopted %d links, want 1", n)
	}
	var kind string
	if err := s.db.QueryRow(`select kind from link where url = ?`, "https://github.com/o/r/pull/11339").Scan(&kind); err != nil {
		t.Fatalf("link not registered: %v", err)
	}
	if kind != "github_pr" {
		t.Errorf("kind = %q, want github_pr", kind)
	}

	// Idempotent: the timer runs this every 10 minutes.
	if n, err := adoptHandoffLinks(s, root); err != nil || n != 0 {
		t.Fatalf("second run adopted %d (err %v), want 0", n, err)
	}
}

// A project with no handoff, and a root that does not exist, are both ordinary
// states on this machine - four ~/p projects have no HANDOFF.md at all.
func TestAdoptHandoffLinksToleratesMissingFiles(t *testing.T) {
	s := openTestStore(t)
	if err := s.UpsertProjects(found("m/a")); err != nil {
		t.Fatal(err)
	}
	if n, err := adoptHandoffLinks(s, t.TempDir()); err != nil || n != 0 {
		t.Fatalf("missing handoff: got %d, %v; want 0, nil", n, err)
	}
	if n, err := adoptHandoffLinks(s, filepath.Join(t.TempDir(), "nope")); err != nil || n != 0 {
		t.Fatalf("missing root: got %d, %v; want 0, nil", n, err)
	}
}

// Adoption is worth nothing unless the thing that runs every 10 minutes calls
// it: the point is that a PR named only in a handoff gets picked up without
// anyone adding it. Asserts it is refreshed in the same pass, not the next.
func TestRefreshLinksAdoptsFromHandoffs(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "m/a"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "m/a", "HANDOFF.md"),
		[]byte("merge https://github.com/o/r/pull/11339 once it is ready\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	s := openTestStore(t)
	if err := s.UpsertProjects(found("m/a")); err != nil {
		t.Fatal(err)
	}
	now := ts("2026-09-14T18:00:00Z")
	fetched := map[string]bool{}
	deps := linkDeps{me: "me", now: now, root: root,
		gh: func(url, etag string) (ghPR, int, string, error) {
			fetched[url] = true
			return ghPR{State: "closed", Merged: true, Title: "telemetry", Author: "me",
				CreatedAt: ts("2026-09-13T10:00:00Z"), ClosedAt: ts("2026-09-14T16:04:40Z"),
				MergedAt: ts("2026-09-14T16:04:40Z"), UpdatedAt: ts("2026-09-14T16:04:42Z")}, 200, `"e"`, nil
		},
		elly:   func() (map[string]ellyPR, time.Time, error) { return nil, now, nil },
		checks: func(string) (string, time.Time, error) { return "", time.Time{}, nil },
	}
	res, err := refreshLinks(s, deps)
	if err != nil {
		t.Fatal(err)
	}
	if res.Adopted != 1 {
		t.Errorf("Adopted = %d, want 1", res.Adopted)
	}
	if !fetched["https://github.com/o/r/pull/11339"] {
		t.Error("adopted link was not refreshed in the same pass")
	}
	if got := readLink(t, s, "https://github.com/o/r/pull/11339"); !got.Merged {
		t.Error("adopted link did not pick up its merged state")
	}
}

// A mirrored issue's title is p-launcher's own output, counter and all.
// Adopting it as the project description would describe m/claude-billing as
// "claude-billing (0/6)"; a hand-linked issue still seeds the description.
func TestRefreshKeepsGeneratedTitlesOutOfDescriptions(t *testing.T) {
	s := openTestStore(t)
	if err := s.UpsertProjects(found("m/a", "m/b")); err != nil {
		t.Fatal(err)
	}
	if err := s.SetPrimaryIssue("m/a", "https://github.com/o/r/issues/1"); err != nil {
		t.Fatal(err)
	}
	if err := s.AddLinkKind("m/b", "https://github.com/o/r/issues/2", "github_issue"); err != nil {
		t.Fatal(err)
	}
	now := ts("2026-09-19T12:00:00Z")
	deps := linkDeps{me: "me", now: now,
		gh: func(url, etag string) (ghPR, int, string, error) {
			if strings.HasSuffix(url, "/1") {
				return ghPR{State: "open", Title: "a (1/2)", Author: "me", CreatedAt: now}, 200, `"e"`, nil
			}
			return ghPR{State: "open", Title: "Reputation rollout", Author: "me", CreatedAt: now}, 200, `"e"`, nil
		},
		elly: func() (map[string]ellyPR, time.Time, error) { return map[string]ellyPR{}, now, nil },
	}
	if _, err := refreshLinks(s, deps); err != nil {
		t.Fatal(err)
	}
	desc := func(path string) string {
		var d string
		if err := s.db.QueryRow(`select description from project where path = ?`, path).Scan(&d); err != nil {
			t.Fatal(err)
		}
		return d
	}
	if got := desc("m/a"); got != "" {
		t.Errorf("mirrored title leaked into the description: %q", got)
	}
	if got := desc("m/b"); got != "Reputation rollout" {
		t.Errorf("hand-linked issue did not seed the description: %q", got)
	}
}

// Comments are the human channel on a mirrored issue, and the brief reads
// them from the row the refresh fills. Only a mirrored issue is asked for.
func TestRefreshStoresLastCommentForMirroredIssues(t *testing.T) {
	s := openTestStore(t)
	if err := s.UpsertProjects(found("m/a")); err != nil {
		t.Fatal(err)
	}
	if err := s.SetPrimaryIssue("m/a", "https://github.com/o/r/issues/1"); err != nil {
		t.Fatal(err)
	}
	if err := s.AddLink("m/a", "https://github.com/o/r/pull/9"); err != nil {
		t.Fatal(err)
	}
	now := ts("2026-09-19T12:00:00Z")
	var asked []string
	deps := linkDeps{me: "me", now: now,
		gh: func(url, etag string) (ghPR, int, string, error) {
			return ghPR{State: "open", Title: "t", Author: "me", CreatedAt: now, UpdatedAt: now}, 200, `"e"`, nil
		},
		elly: func() (map[string]ellyPR, time.Time, error) { return map[string]ellyPR{}, now, nil },
		lastComment: func(url string) (string, string, time.Time, error) {
			asked = append(asked, url)
			return "tobias", "Looks right to me", now.Add(-time.Hour), nil
		},
	}
	if _, err := refreshLinks(s, deps); err != nil {
		t.Fatal(err)
	}
	if len(asked) != 1 || !strings.HasSuffix(asked[0], "/issues/1") {
		t.Fatalf("comments fetched for %v, want the mirrored issue only", asked)
	}
	var author, body, at string
	if err := s.db.QueryRow(`select last_comment_author, last_comment_body, coalesce(last_comment_at,'')
		from link where url = 'https://github.com/o/r/issues/1'`).Scan(&author, &body, &at); err != nil {
		t.Fatal(err)
	}
	if author != "tobias" || body != "Looks right to me" || at == "" {
		t.Errorf("stored %q %q %q", author, body, at)
	}
	if got, _ := s.kvGet("gh.login"); got != "me" {
		t.Errorf("gh.login = %q, want the refresh to cache it for the offline brief", got)
	}
}
