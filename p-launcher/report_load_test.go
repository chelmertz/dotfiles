package main

import (
	"testing"
	"time"
)

func TestLoadReportDataEmptyAndSmoke(t *testing.T) {
	s := openTestStore(t)
	now := time.Now()
	raw, err := loadReportData(s, now.AddDate(0, 0, -30), now)
	if err != nil {
		t.Fatal(err)
	}
	if len(raw.Events) != 0 || len(raw.Links) != 0 || len(raw.Projects) != 0 {
		t.Fatalf("%+v", raw)
	}
	// a few rows round-trip through the parsers
	if err := s.UpsertProjects(found("m/a")); err != nil {
		t.Fatal(err)
	}
	if err := s.RecordSessionEvent(SessionEvent{SessionID: "s1", Path: "m/a", Cwd: "/x", Kind: "prompt"}); err != nil {
		t.Fatal(err)
	}
	if err := s.SetSessionState("s1", "m/a", "claude"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`insert into link (project_id, url, author, opened_at, closed_at, merged) select id, 'https://github.com/o/r/pull/1', 'me', ?, ?, 1 from project where path = 'm/a'`, rfc(now.Add(-2*time.Hour)), rfc(now.Add(-time.Hour))); err != nil {
		t.Fatal(err)
	}
	raw, err = loadReportData(s, now.AddDate(0, 0, -30), now)
	if err != nil {
		t.Fatal(err)
	}
	if len(raw.Events) != 1 || raw.Events[0].Project != "m/a" || raw.Events[0].At.IsZero() {
		t.Fatalf("%+v", raw.Events)
	}
	if len(raw.Links) != 1 || !raw.Links[0].Merged || raw.Links[0].MergedAt.IsZero() || raw.Links[0].Author != "me" {
		t.Fatalf("%+v", raw.Links)
	}
	if len(raw.States) != 1 || raw.States[0].State != "claude" || raw.Projects[0] != "m/a" {
		t.Fatalf("%+v %v", raw.States, raw.Projects)
	}
}
