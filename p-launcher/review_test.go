package main

import (
	"testing"
	"time"
)

// A fresh elly verdict on one of the user's PRs makes the project need-input
// in the menu; a stale verdict does not.
func TestListProjectsReview(t *testing.T) {
	s := openTestStore(t)
	if err := s.UpsertProjects(found("m/a", "m/b")); err != nil {
		t.Fatal(err)
	}
	t0 := time.Date(2026, 9, 7, 10, 0, 0, 0, time.UTC)
	// m/b is the MRU-newer project
	if err := s.recordAt("m/b", "launch", t0); err != nil {
		t.Fatal(err)
	}
	if err := s.AddLink("m/a", "https://github.com/o/r/pull/1"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`update link set action_needed = 1`); err != nil {
		t.Fatal(err)
	}
	// no elly data yet: not fresh, MRU order
	ps, err := s.listProjectsAt(false, t0.Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if ps[0].Path != "m/b" || ps[1].Review {
		t.Fatalf("stale verdict must not sort or flag: %+v", ps)
	}
	if err := s.kvSet("elly.last_fetched", t0.Add(55*time.Minute).UTC().Format(time.RFC3339)); err != nil {
		t.Fatal(err)
	}
	ps, err = s.listProjectsAt(false, t0.Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if ps[0].Path != "m/a" || !ps[0].Review || ps[1].Review {
		t.Fatalf("fresh verdict must sort first and flag: %+v", ps)
	}
	// 45 minutes later elly has not fetched: stale again
	ps, _ = s.listProjectsAt(false, t0.Add(100*time.Minute))
	if ps[0].Path != "m/b" || ps[1].Review {
		t.Fatalf("stale again: %+v", ps)
	}
}

func TestStateIconReview(t *testing.T) {
	cases := []struct {
		p    Project
		open bool
		want string
	}{
		{Project{Review: true}, false, "review"},
		{Project{Review: true}, true, "review"},
		{Project{Review: true, Ball: "you"}, true, "you"},
		{Project{Review: true, Ball: "claude"}, true, "review"},
		{Project{Ball: "claude"}, true, "claude"},
		{Project{}, true, "idle"},
		{Project{}, false, ""},
	}
	for _, c := range cases {
		if got := stateIcon(c.p, c.open); got != c.want {
			t.Errorf("%+v open=%v: got %q want %q", c.p, c.open, got, c.want)
		}
	}
}
