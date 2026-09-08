package main

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func briefFixture(t *testing.T) (*Store, map[string]ellyPR, time.Time) {
	t.Helper()
	s := openTestStore(t)
	if err := s.UpsertProjects(found("m/a", "m/parked")); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 9, 9, 8, 30, 0, 0, time.UTC)
	if err := s.AddLink("m/a", "https://github.com/o/r/pull/1"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`update link set detail = '2 unresolved threads', action_needed = 1`); err != nil {
		t.Fatal(err)
	}
	if err := s.ProjectEvent("m/parked", "snoozed", now.AddDate(0, 0, 3).Format(time.RFC3339)); err != nil {
		t.Fatal(err)
	}
	prs := map[string]ellyPR{
		"https://github.com/o/r/pull/1": {Author: "me", Title: "mine", Repo: "r", ThreadsActionable: 2,
			LastCommenter: "adam", LastUpdated: now.AddDate(0, 0, -2), Additions: 10, Deletions: 3},
		"https://github.com/o/r/pull/2": {Author: "me", Title: "nudge", Repo: "r", RereviewFrom: "adam",
			ReviewStatus: "REVIEW_REQUIRED", LastUpdated: now.AddDate(0, 0, -9)},
		"https://github.com/o/r/pull/3": {Author: "adam", Title: "theirs", Repo: "r", ReviewRequested: "me"},
		"https://github.com/o/r/pull/9": {Author: "me", Title: "hidden", Repo: "r", Buried: true},
	}
	return s, prs, now
}

func TestBuildBriefInput(t *testing.T) {
	s, prs, now := briefFixture(t)
	in, err := buildBriefInput(s, prs, now.Add(-4*time.Minute), now, "me")
	if err != nil {
		t.Fatal(err)
	}
	if len(in.PRs) != 3 {
		t.Fatalf("a buried PR must be left out: %d PRs", len(in.PRs))
	}
	if in.PRs[0].URL != "https://github.com/o/r/pull/1" || !in.PRs[0].Mine || in.PRs[2].Mine {
		t.Fatalf("own PRs must sort first: %+v", in.PRs)
	}
	if in.PRs[0].Project != "m/a" || in.PRs[0].Verdict != "2 unresolved threads" {
		t.Fatalf("launcher state must travel with the PR: %+v", in.PRs[0])
	}
	if in.PRs[0].IdleDays != 2 || in.PRs[1].IdleDays != 9 {
		t.Fatalf("idle days: %+v", in.PRs)
	}
	if in.PRs[1].AskToReview != "adam" {
		t.Fatalf("re-review signal must reach the brief: %+v", in.PRs[1])
	}
	if strings.Join(in.Postponed, ",") != "m/parked" {
		t.Fatalf("postponed projects: %v", in.Postponed)
	}
	if in.Me != "me" || in.Date != "2026-09-09" || in.EllyAge == "" {
		t.Fatalf("%+v", in)
	}
	if in.Previous != nil {
		t.Fatalf("no brief yet, so nothing carried over: %+v", in.Previous)
	}
	// the prompt must carry the payload and forbid writing
	p := briefPrompt("PAYLOAD")
	for _, want := range []string{"PAYLOAD", "Read only", "full URL"} {
		if !strings.Contains(p, want) {
			t.Errorf("prompt lacks %q", want)
		}
	}
}

func TestParseBriefOut(t *testing.T) {
	good := `{"summary":"s","items":[{"url":"https://github.com/o/r/pull/1","action":"merge","why":"green","also_urls":["https://github.com/o/r/pull/4"]}],"skip":"rest"}`
	out, err := parseBriefOut(good)
	if err != nil || out.Summary != "s" || len(out.Items) != 1 || out.Items[0].Action != "merge" {
		t.Fatalf("%+v %v", out, err)
	}
	if len(out.Items[0].Also) != 1 || out.Items[0].Also[0] != "https://github.com/o/r/pull/4" {
		t.Fatalf("grouped URLs: %+v", out.Items[0])
	}
	// the prompt must ask for that shape rather than URLs inside the action
	if p := briefPrompt("x"); !strings.Contains(p, "also_urls") || !strings.Contains(p, "no URL in it") {
		t.Error("prompt does not constrain the action shape")
	}
	// a fence or a sentence around the object must not break it
	if _, err := parseBriefOut("Here you go:\n```json\n" + good + "\n```\n"); err != nil {
		t.Fatalf("wrapped JSON: %v", err)
	}
	for _, bad := range []string{"", "no json here", "{", `{"summary":""}`, `{"items":`} {
		if _, err := parseBriefOut(bad); err == nil {
			t.Errorf("%q must not parse", bad)
		}
	}
}

func TestStoreBriefCountsRepeats(t *testing.T) {
	s, prs, now := briefFixture(t)
	first := briefOut{Summary: "day one", Items: []briefItem{
		{URL: "https://github.com/o/r/pull/1", Action: "answer adam", Why: "2 threads", Also: []string{"https://github.com/o/r/pull/7"}},
		{URL: "https://github.com/o/r/pull/2", Action: "ask adam to re-review", Why: "9 days"},
	}}
	if _, err := s.storeBrief(first, "raw1", now); err != nil {
		t.Fatal(err)
	}
	// the next brief sees both, with what is still open
	in, err := buildBriefInput(s, prs, now, now.AddDate(0, 0, 1), "me")
	if err != nil {
		t.Fatal(err)
	}
	if len(in.Previous) != 2 || in.Previous[0].Action != "answer adam" || !in.Previous[0].StillOpen || in.Previous[0].Age != 1 {
		t.Fatalf("%+v", in.Previous)
	}
	// a PR that left elly's list counts as no longer open
	delete(prs, "https://github.com/o/r/pull/2")
	if in, err = buildBriefInput(s, prs, now, now.AddDate(0, 0, 1), "me"); err != nil {
		t.Fatal(err)
	}
	if in.Previous[1].StillOpen {
		t.Fatalf("a closed PR must not read as still open: %+v", in.Previous[1])
	}
	// repeating an item raises its count; a new URL starts at one
	second := briefOut{Summary: "day two", Items: []briefItem{
		{URL: "https://github.com/o/r/pull/1", Action: "answer adam", Why: "still 2 threads"},
		{URL: "https://github.com/o/r/pull/3", Action: "review it", Why: "small"},
	}}
	if _, err := s.storeBrief(second, "raw2", now.AddDate(0, 0, 1)); err != nil {
		t.Fatal(err)
	}
	var repeats1, repeats3 int
	if err := s.db.QueryRow(`select repeats from brief_item where brief_id = (select max(id) from brief) and url like '%/1'`).Scan(&repeats1); err != nil {
		t.Fatal(err)
	}
	if err := s.db.QueryRow(`select repeats from brief_item where brief_id = (select max(id) from brief) and url like '%/3'`).Scan(&repeats3); err != nil {
		t.Fatal(err)
	}
	if repeats1 != 2 || repeats3 != 1 {
		t.Fatalf("repeats: %d and %d", repeats1, repeats3)
	}
	if n, err := s.briefRunCount(); err != nil || n != 2 {
		t.Fatalf("%d %v", n, err)
	}
	// the newest brief is what --show reads
	out, at, err := s.LatestBrief()
	if err != nil || out.Summary != "day two" || len(out.Items) != 2 || !at.Equal(now.AddDate(0, 0, 1)) {
		t.Fatalf("%+v %v %v", out, at, err)
	}
	// grouped URLs survive the round trip
	if _, err := s.storeBrief(first, "raw3", now.AddDate(0, 0, 2)); err != nil {
		t.Fatal(err)
	}
	if got, _, err := s.LatestBrief(); err != nil || len(got.Items[0].Also) != 1 || got.Items[0].Also[0] != "https://github.com/o/r/pull/7" {
		t.Fatalf("%+v %v", got.Items, err)
	}
	if txt := briefText(out, at); !strings.Contains(txt, "1. answer adam") || !strings.Contains(txt, "https://github.com/o/r/pull/3") {
		t.Fatalf("%q", txt)
	}
}

func TestRunBrief(t *testing.T) {
	s, prs, now := briefFixture(t)
	dir := t.TempDir()
	var asked, notified string
	deps := briefDeps{now: now, me: "me",
		elly: func() (map[string]ellyPR, time.Time, error) { return prs, now, nil },
		ask: func(p string) (string, error) {
			asked = p
			return `{"summary":"one thing matters","items":[{"url":"https://github.com/o/r/pull/1","action":"answer adam","why":"2 threads"}],"waiting_on_others":[{"url":"https://github.com/o/r/pull/2","reviewer":"adam","days":9}],"skip":"the rest"}`, nil
		},
		notify: func(m string) { notified = m },
	}
	var stdout bytes.Buffer
	if err := runBrief(s, deps, false, dir, &stdout); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(asked, "https://github.com/o/r/pull/1") {
		t.Fatal("the payload never reached the prompt")
	}
	if notified != "brief: 1 action · answer adam" {
		t.Fatalf("%q", notified)
	}
	page, err := os.ReadFile(filepath.Join(dir, "brief.html"))
	if err != nil {
		t.Fatal(err)
	}
	html := string(page)
	for _, want := range []string{"one thing matters", "answer adam", "o/r#1", "adam", "the rest", "<!doctype html>"} {
		if !strings.Contains(html, want) {
			t.Errorf("page lacks %q", want)
		}
	}
	if !strings.Contains(stdout.String(), "brief.html") {
		t.Fatalf("stdout: %q", stdout.String())
	}

	// a dry run prints the prompt and stores nothing
	s2, prs2, _ := briefFixture(t)
	stdout.Reset()
	deps2 := deps
	deps2.elly = func() (map[string]ellyPR, time.Time, error) { return prs2, now, nil }
	deps2.ask = func(string) (string, error) { t.Fatal("dry run must not ask claude"); return "", nil }
	if err := runBrief(s2, deps2, true, dir, &stdout); err != nil {
		t.Fatal(err)
	}
	if n, err := s2.briefRunCount(); err != nil || n != 0 {
		t.Fatalf("dry run stored %d briefs (%v)", n, err)
	}
	if !strings.Contains(stdout.String(), "Read only") {
		t.Fatalf("dry run must print the prompt: %q", stdout.String())
	}

	// an unparseable answer is kept, and the error surfaces
	s3, prs3, _ := briefFixture(t)
	deps3 := deps
	deps3.elly = func() (map[string]ellyPR, time.Time, error) { return prs3, now, nil }
	deps3.ask = func(string) (string, error) { return "I could not do that", nil }
	if err := runBrief(s3, deps3, false, dir, &stdout); err == nil {
		t.Fatal("an unparseable answer must be reported")
	}
	var raw string
	if err := s3.db.QueryRow(`select raw from brief order by id desc limit 1`).Scan(&raw); err != nil || raw != "I could not do that" {
		t.Fatalf("the raw answer must be kept: %q %v", raw, err)
	}

	// elly missing: the brief fails loudly rather than inventing a queue
	s4, _, _ := briefFixture(t)
	deps4 := deps
	deps4.elly = func() (map[string]ellyPR, time.Time, error) { return nil, time.Time{}, errors.New("no elly.db") }
	if err := runBrief(s4, deps4, false, dir, &stdout); err == nil || !strings.Contains(err.Error(), "elly") {
		t.Fatalf("%v", err)
	}
}

func TestBriefDueOncePerDay(t *testing.T) {
	now := time.Date(2026, 9, 9, 8, 0, 0, 0, time.UTC)
	if !briefDue("", now) {
		t.Fatal("no previous run must be due")
	}
	if briefDue(now.Add(-2*time.Hour).UTC().Format(time.RFC3339), now) {
		t.Fatal("a run earlier the same day must not be due")
	}
	if !briefDue(now.AddDate(0, 0, -1).UTC().Format(time.RFC3339), now) {
		t.Fatal("yesterday's run must be due")
	}
}

func TestBriefHeadline(t *testing.T) {
	if got := briefHeadline(briefOut{}); got != "brief: nothing to act on today" {
		t.Errorf("%q", got)
	}
	two := briefOut{Items: []briefItem{{Action: "merge"}, {Action: "ping"}}}
	if got := briefHeadline(two); got != "brief: 2 actions · first: merge" {
		t.Errorf("%q", got)
	}
	long := briefOut{Items: []briefItem{{Action: strings.Repeat("verbose ", 20)}}}
	if got := briefHeadline(long); len(got) > 100 || !strings.HasSuffix(got, "…") {
		t.Errorf("a long action must be clipped: %q", got)
	}
}
