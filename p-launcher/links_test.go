package main

import (
	"errors"
	"os"
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
					CreatedAt: ts("2026-09-01T10:00:00Z"), ClosedAt: ts("2026-09-03T10:00:00Z"), MergedAt: ts("2026-09-03T10:00:00Z")}, 200, `"e1"`, nil
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
