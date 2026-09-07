package main

import (
	"strings"
	"testing"
	"time"
)

func TestTendDecide(t *testing.T) {
	now := ts("2026-09-07T14:00:00Z")
	base := tendLink{ID: 1, URL: "https://github.com/o/r/pull/1", Project: "m/a", Author: "me", LastCommenter: "jd",
		ActionNeeded: true, Detail: "2 unresolved threads", Open: true, LastUpdated: now.Add(-10 * time.Minute)}
	cfg := tendCfg{Enabled: true, DailyCap: 4, Me: "me", Bots: defaultBots}
	dirs := func(string) bool { return true }
	noDir := func(string) bool { return false }
	cases := []struct {
		name   string
		link   tendLink
		cfg    tendCfg
		live   map[string]bool
		dir    func(string) bool
		kind   string
		why    string
		reason string
	}{
		{"happy", base, cfg, nil, dirs, "act", "", "2 unresolved threads"},
		{"live session", base, cfg, map[string]bool{"m/a": true}, dirs, "notify", "live session", "2 unresolved threads"},
		{"disabled", base, tendCfg{Enabled: false, DailyCap: 4, Me: "me"}, nil, dirs, "skip", "disabled", ""},
		{"daily cap", base, tendCfg{Enabled: true, DailyCap: 4, UsedToday: 4, Me: "me"}, nil, dirs, "notify", "daily cap", ""},
		{"not mine", with(base, func(l *tendLink) { l.Author = "jd" }), cfg, nil, dirs, "skip", "not my PR", ""},
		{"draft", with(base, func(l *tendLink) { l.IsDraft = true }), cfg, nil, dirs, "skip", "draft", ""},
		{"closed", with(base, func(l *tendLink) { l.Open = false }), cfg, nil, dirs, "skip", "not open", ""},
		{"nothing to do", with(base, func(l *tendLink) { l.ActionNeeded = false }), cfg, nil, dirs, "skip", "no feedback", ""},
		{"no new activity", with(base, func(l *tendLink) { l.TendedAt = now.Add(-5 * time.Minute) }), cfg, nil, dirs, "skip", "no new activity", ""},
		{"new activity after round", with(base, func(l *tendLink) { l.TendedAt = now.Add(-20 * time.Minute) }), cfg, nil, dirs, "act", "", "2 unresolved threads"},
		{"last commenter is me", with(base, func(l *tendLink) { l.LastCommenter = "me" }), cfg, nil, dirs, "skip", "last commenter is me", ""},
		{"own signed reply", with(base, func(l *tendLink) { l.LastBody = "done, see commit\n--claude" }), cfg, nil, dirs, "skip", "own reply", ""},
		{"bot comment", with(base, func(l *tendLink) { l.LastCommenter = "dependabot[bot]" }), cfg, nil, dirs, "skip", "bot comment", ""},
		{"bot comment but check failed", with(base, func(l *tendLink) {
			l.LastCommenter = "github-actions[bot]"
			l.CheckState = "failure"
			l.CheckAt = now.Add(-2 * time.Minute)
		}), cfg, nil, dirs, "act", "", "check failed"},
		{"check failed, no review", with(base, func(l *tendLink) {
			l.ActionNeeded = false
			l.Detail = ""
			l.CheckState = "failure"
			l.CheckAt = now.Add(-2 * time.Minute)
		}), cfg, nil, dirs, "act", "", "check failed"},
		{"changes requested wording", with(base, func(l *tendLink) { l.Detail = "changes requested" }), cfg, nil, dirs, "act", "", "changes requested"},
		{"dir missing", base, cfg, nil, noDir, "skip", "dir missing", ""},
	}
	for _, c := range cases {
		ds := tendDecide([]tendLink{c.link}, c.cfg, c.live, c.dir, now)
		if len(ds) != 1 {
			t.Fatalf("%s: %d decisions", c.name, len(ds))
		}
		d := ds[0]
		if d.Kind != c.kind || d.Why != c.why || (c.reason != "" && !strings.Contains(d.Reason, c.reason)) {
			t.Errorf("%s: got %s/%q/%q want %s/%q/%q", c.name, d.Kind, d.Why, d.Reason, c.kind, c.why, c.reason)
		}
	}
	// daily cap counts acts within one run too: two candidates, cap 1 → one act, one notify
	two := []tendLink{base, with(base, func(l *tendLink) { l.ID = 2; l.URL = "https://github.com/o/r/pull/2"; l.Project = "m/b" })}
	ds := tendDecide(two, tendCfg{Enabled: true, DailyCap: 1, Me: "me"}, nil, dirs, now)
	if ds[0].Kind != "act" || ds[1].Kind != "notify" || ds[1].Why != "daily cap" {
		t.Fatalf("%+v", ds)
	}
	// stable order by project
	ds = tendDecide([]tendLink{two[1], two[0]}, cfg, nil, dirs, now)
	if ds[0].Project() != "m/a" {
		t.Fatalf("%+v", ds)
	}
}

func with(l tendLink, f func(*tendLink)) tendLink {
	f(&l)
	return l
}

func TestTendPrompt(t *testing.T) {
	p := tendPrompt("https://github.com/o/r/pull/1", "2 unresolved threads")
	for _, want := range []string{"https://github.com/o/r/pull/1", "2 unresolved threads", "--claude", "Do not approve, merge", "verify"} {
		if !strings.Contains(p, want) {
			t.Fatalf("prompt missing %q:\n%s", want, p)
		}
	}
}

func TestLaunchArgs(t *testing.T) {
	argv, env := launchArgs("/home/x/p/m/a", "p:m/a", "")
	if strings.Join(argv, " ") != "ghostty --x11-instance-name=p:m/a --working-directory=/home/x/p/m/a -e zsh -ic claude; exec zsh" {
		t.Fatalf("%q", argv)
	}
	for _, e := range env {
		if strings.HasPrefix(e, "P_LAUNCHER_PROMPT=") {
			t.Fatal("prompt env set without a prompt")
		}
	}
	argv, env = launchArgs("/home/x/p/m/a", "p:m/a", "fix it \"now\"")
	if argv[len(argv)-1] != `claude "$P_LAUNCHER_PROMPT"; exec zsh` {
		t.Fatalf("%q", argv)
	}
	found := false
	for _, e := range env {
		if e == `P_LAUNCHER_PROMPT=fix it "now"` {
			found = true
		}
	}
	if !found {
		t.Fatalf("prompt not in env: %v", env)
	}
}

func TestTendApplyRecords(t *testing.T) {
	s := openTestStore(t)
	root := t.TempDir()
	mk(t, root, "m/a")
	if err := s.UpsertProjects(found("m/a")); err != nil {
		t.Fatal(err)
	}
	if err := s.AddLink("m/a", "https://github.com/o/r/pull/1"); err != nil {
		t.Fatal(err)
	}
	now := ts("2026-09-07T14:00:00Z")
	var launched []string
	var notified []string
	deps := tendDeps{
		launch: func(dir, tag, prompt string) error {
			launched = append(launched, dir+"|"+tag+"|"+prompt[:20])
			return nil
		},
		notify: func(msg string) { notified = append(notified, msg) },
		now:    now,
	}
	ds := []decision{
		{Link: tendLink{ID: 1, URL: "https://github.com/o/r/pull/1", Project: "m/a"}, Kind: "act", Reason: "changes requested"},
	}
	if err := tendApply(s, root, ds, deps); err != nil {
		t.Fatal(err)
	}
	if len(launched) != 1 || !strings.HasPrefix(launched[0], root+"/m/a|p:m/a|") {
		t.Fatalf("%v", launched)
	}
	var tended string
	var rounds int
	if err := s.db.QueryRow(`select coalesce(tended_at,''), tend_rounds from link where id = 1`).Scan(&tended, &rounds); err != nil || tended == "" || rounds != 1 {
		t.Fatalf("%q %d %v", tended, rounds, err)
	}
	if v, _ := s.kvGet("tend.used." + now.Format("2006-01-02")); v != "1" {
		t.Fatalf("used %q", v)
	}
	var n int
	if err := s.db.QueryRow(`select count(*) from session_event where kind = 'tend' and detail = 'https://github.com/o/r/pull/1'`).Scan(&n); err != nil || n != 1 {
		t.Fatalf("tend event %d %v", n, err)
	}
	// notify: once per activity burst
	ds = []decision{{Link: tendLink{ID: 1, URL: "https://github.com/o/r/pull/1", Project: "m/a", LastUpdated: now}, Kind: "notify", Reason: "changes requested", Why: "live session"}}
	if err := tendApply(s, root, ds, deps); err != nil {
		t.Fatal(err)
	}
	if err := tendApply(s, root, ds, deps); err != nil {
		t.Fatal(err)
	}
	if len(notified) != 1 || !strings.Contains(notified[0], "o/r#1") || !strings.Contains(notified[0], "live session") {
		t.Fatalf("%v", notified)
	}
	// new activity after the notification → notify again
	ds[0].Link.LastUpdated = now.Add(time.Hour)
	deps.now = now.Add(2 * time.Hour)
	if err := tendApply(s, root, ds, deps); err != nil {
		t.Fatal(err)
	}
	if len(notified) != 2 {
		t.Fatalf("%v", notified)
	}
}
