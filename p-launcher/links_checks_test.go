package main

import (
	"errors"
	"strings"
	"testing"
	"time"
)

func TestMigrate006TendColumns(t *testing.T) {
	db := openTestDB(t)
	if err := migrate(db); err != nil {
		t.Fatal(err)
	}
	for _, col := range []string{"last_commenter", "is_draft", "check_state", "check_at", "tended_at", "tend_rounds", "notified_at"} {
		if _, err := db.Exec(`select ` + col + ` from link limit 1`); err != nil {
			t.Fatalf("link.%s missing: %v", col, err)
		}
	}
}

func TestRefreshLinksCommenterDraftChecks(t *testing.T) {
	s, now := linksFixture(t)
	checked := map[string]bool{}
	deps := linkDeps{me: "me", now: now,
		gh: func(url, etag string) (ghPR, int, string, error) {
			author := "me"
			if strings.HasSuffix(url, "/2") {
				author = "jd"
			}
			return ghPR{State: "open", Author: author, CreatedAt: now.Add(-time.Hour)}, 200, `"e"`, nil
		},
		elly: func() (map[string]ellyPR, time.Time, error) {
			return map[string]ellyPR{
				"https://github.com/o/r/pull/1": {ThreadsActionable: 1, LastCommenter: "jd", IsDraft: true},
			}, now, nil
		},
		checks: func(url string) (string, time.Time, error) {
			checked[url] = true
			return "failure", now.Add(-10 * time.Minute), nil
		},
	}
	if _, err := refreshLinks(s, deps); err != nil {
		t.Fatal(err)
	}
	one := readLinkTend(t, s, "https://github.com/o/r/pull/1")
	if one.LastCommenter != "jd" || !one.IsDraft || one.CheckState != "failure" || one.CheckAt == "" {
		t.Fatalf("%+v", one)
	}
	// checks run only for the user's own open PRs
	if !checked["https://github.com/o/r/pull/1"] || checked["https://github.com/o/r/pull/2"] {
		t.Fatalf("checked %v", checked)
	}
	// a failing checks call keeps the old values and is noted, not counted as a failed link
	deps.checks = func(url string) (string, time.Time, error) { return "", time.Time{}, errors.New("gh: boom") }
	deps.now = now.Add(time.Hour)
	res, err := refreshLinks(s, deps)
	if err != nil || res.Failed != 0 {
		t.Fatalf("%v %+v", err, res)
	}
	if again := readLinkTend(t, s, "https://github.com/o/r/pull/1"); again.CheckState != "failure" {
		t.Fatalf("check state lost: %+v", again)
	}
	if v, _ := s.kvGet("links.last_error"); !strings.Contains(v, "boom") {
		t.Fatalf("last_error %q", v)
	}
}

type linkTendRow struct {
	LastCommenter, CheckState, CheckAt string
	IsDraft                            bool
}

func readLinkTend(t *testing.T, s *Store, url string) linkTendRow {
	t.Helper()
	var r linkTendRow
	var draft int
	if err := s.db.QueryRow(`select last_commenter, is_draft, check_state, coalesce(check_at,'') from link where url = ?`, url).Scan(&r.LastCommenter, &draft, &r.CheckState, &r.CheckAt); err != nil {
		t.Fatal(err)
	}
	r.IsDraft = draft == 1
	return r
}

func TestFoldChecks(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{`[{"conclusion":"SUCCESS","completedAt":"2026-09-07T10:00:00Z"},{"conclusion":"SUCCESS","completedAt":"2026-09-07T10:05:00Z"}]`, "success"},
		{`[{"conclusion":"SUCCESS"},{"conclusion":"FAILURE","completedAt":"2026-09-07T10:05:00Z"}]`, "failure"},
		{`[{"conclusion":"SUCCESS"},{"status":"IN_PROGRESS"}]`, "pending"},
		{`[{"conclusion":"CANCELLED"}]`, "failure"},
		{`[]`, ""},
	}
	for _, c := range cases {
		state, at := foldChecks([]byte(c.in))
		if state != c.want {
			t.Errorf("%s: got %q want %q", c.in, state, c.want)
		}
		if c.want == "success" && at.IsZero() {
			t.Errorf("%s: no completion time", c.in)
		}
	}
}
