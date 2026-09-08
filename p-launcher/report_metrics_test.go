package main

import (
	"math"
	"reflect"
	"testing"
	"time"
)

func ts(s string) time.Time {
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		panic(err)
	}
	return t
}

func ev(sid, proj, kind, detail, at string) rawEvent {
	return rawEvent{SessionID: sid, Project: proj, Kind: kind, Detail: detail, At: ts(at)}
}

func TestAwayIntervals(t *testing.T) {
	// a 40-minute gap between prompts anywhere is away
	evs := []rawEvent{ev("a", "m/x", "prompt", "", "2026-09-01T10:00:00Z"), ev("b", "m/y", "prompt", "", "2026-09-01T10:40:00Z")}
	away := awayIntervals(evs, 15*time.Minute, ts("2026-09-01T10:41:00Z"))
	if len(away) != 1 || !away[0].from.Equal(ts("2026-09-01T10:00:00Z")) || !away[0].to.Equal(ts("2026-09-01T10:40:00Z")) {
		t.Fatalf("%+v", away)
	}
	// a short gap is not; lock/unlock is, regardless of length
	evs = []rawEvent{
		ev("a", "m/x", "prompt", "", "2026-09-01T10:00:00Z"),
		ev("b", "m/y", "prompt", "", "2026-09-01T10:05:00Z"),
		ev("desktop", "", "lock", "", "2026-09-01T10:06:00Z"),
		ev("desktop", "", "unlock", "", "2026-09-01T10:09:00Z"),
	}
	away = awayIntervals(evs, 15*time.Minute, ts("2026-09-01T10:10:00Z"))
	if len(away) != 1 || !away[0].from.Equal(ts("2026-09-01T10:06:00Z")) || !away[0].to.Equal(ts("2026-09-01T10:09:00Z")) {
		t.Fatalf("%+v", away)
	}
	// an unmatched lock runs to now
	evs = evs[:3]
	away = awayIntervals(evs, 15*time.Minute, ts("2026-09-01T10:10:00Z"))
	if len(away) != 1 || !away[0].to.Equal(ts("2026-09-01T10:10:00Z")) {
		t.Fatalf("%+v", away)
	}
}

func TestWaits(t *testing.T) {
	evs := []rawEvent{
		ev("a", "m/x", "prompt", "", "2026-09-01T10:00:00Z"),
		ev("a", "m/x", "stop", "", "2026-09-01T10:05:00Z"),
		ev("b", "m/y", "prompt", "", "2026-09-01T10:07:00Z"),
		ev("a", "m/x", "prompt", "", "2026-09-01T10:15:00Z"),
		ev("a", "m/x", "stop", "", "2026-09-01T10:20:00Z"),
		ev("a", "m/x", "session_end", "", "2026-09-01T10:21:00Z"),
	}
	ws := waits(evs, nil)
	if len(ws) != 1 || ws[0].dur != 10*time.Minute || ws[0].session != "a" || ws[0].kind != "stop" {
		t.Fatalf("%+v", ws)
	}
	away := []interval{{ts("2026-09-01T10:06:00Z"), ts("2026-09-01T10:14:00Z")}}
	ws = waits(evs, away)
	if ws[0].dur != 2*time.Minute {
		t.Fatalf("away not excluded: %v", ws[0].dur)
	}
	// permission notification starts a wait of kind permission; cap at 4h
	evs = []rawEvent{
		ev("a", "m/x", "notification", "permission_prompt", "2026-09-01T10:00:00Z"),
		ev("a", "m/x", "prompt", "", "2026-09-01T16:00:00Z"),
	}
	ws = waits(evs, nil)
	if len(ws) != 1 || ws[0].dur != 4*time.Hour || ws[0].kind != "permission" {
		t.Fatalf("%+v", ws)
	}
	// auth_success is not a wait
	evs = []rawEvent{ev("a", "m/x", "notification", "auth_success", "2026-09-01T10:00:00Z"), ev("a", "m/x", "prompt", "", "2026-09-01T10:01:00Z")}
	if ws = waits(evs, nil); len(ws) != 0 {
		t.Fatalf("%+v", ws)
	}
}

func TestHourConcurrency(t *testing.T) {
	evs := []rawEvent{
		ev("a", "m/x", "prompt", "", "2026-09-01T10:01:00Z"),
		ev("b", "m/y", "prompt", "", "2026-09-01T10:30:00Z"),
		ev("a", "m/x", "stop", "", "2026-09-01T11:10:00Z"),
		ev("desktop", "", "lock", "", "2026-09-01T11:20:00Z"),
	}
	c := hourConcurrency(evs)
	if c[ts("2026-09-01T10:00:00Z")] != 2 || c[ts("2026-09-01T11:00:00Z")] != 1 || len(c) != 2 {
		t.Fatalf("%v", c)
	}
}

func TestBuckets(t *testing.T) {
	conc := map[time.Time]int{ts("2026-09-01T10:00:00Z"): 2, ts("2026-09-01T11:00:00Z"): 2, ts("2026-09-01T12:00:00Z"): 7}
	links := []rawLink{{Merged: true, MergedAt: ts("2026-09-01T10:30:00Z")}, {Merged: true, MergedAt: ts("2026-09-01T12:30:00Z")}, {}}
	ws := []wait{
		{start: ts("2026-09-01T10:10:00Z"), dur: 2 * time.Minute, kind: "stop"},
		{start: ts("2026-09-01T11:10:00Z"), dur: 4 * time.Minute, kind: "permission"},
	}
	b, knee := buckets(conc, links, ws)
	if len(b) != 6 || b[1].Hours != 2 || b[1].Merged != 1 || b[1].PRsPerHour != 0.5 || b[1].WaitSec != 180 || b[1].ReplySec != 120 || b[5].Hours != 1 || b[5].Merged != 1 || b[5].PRsPerHour != 1 || knee != 5 {
		t.Fatalf("%+v knee=%d", b, knee)
	}
	if _, knee := buckets(nil, nil, nil); knee != -1 {
		t.Fatalf("empty knee = %d", knee)
	}
	// hours but no merges: no knee either
	if _, knee := buckets(conc, nil, ws); knee != -1 {
		t.Fatalf("no-merge knee = %d", knee)
	}
}

func TestWeeks(t *testing.T) {
	now := ts("2026-09-07T14:00:00Z") // Monday, ISO W37
	from := now.AddDate(0, 0, -14)    // Aug 24, W35
	evs := []rawEvent{
		ev("a", "m/x", "prompt", "", "2026-08-25T10:00:00Z"),
		ev("a", "m/x", "prompt", "", "2026-08-25T11:00:00Z"),
		ev("b", "m/y", "prompt", "", "2026-08-25T11:30:00Z"),
		ev("a", "m/x", "prompt", "", "2026-09-07T10:00:00Z"),
	}
	links := []rawLink{{Merged: true, MergedAt: ts("2026-08-26T10:00:00Z")}}
	ws := []wait{{start: ts("2026-08-25T10:30:00Z"), dur: time.Minute, kind: "stop"}}
	w := weeks(from, now, evs, links, ws, hourConcurrency(evs))
	if len(w) != 3 || w[0].Label != "W35" || w[0].Merged != 1 || w[0].Prompts != 3 || w[0].Steer != 3 || w[0].ConcMax != 2 || w[0].WaitSec != 60 {
		t.Fatalf("%+v", w[0])
	}
	if !math.IsNaN(w[1].Steer) || w[1].Label != "W36" {
		t.Fatalf("%+v", w[1])
	}
	if !w[2].Partial || w[2].Label != "W37" || w[2].Prompts != 1 {
		t.Fatalf("%+v", w[2])
	}
}

func TestProjectRows(t *testing.T) {
	now := ts("2026-09-07T14:00:00Z")
	evs := []rawEvent{
		ev("a", "m/x", "prompt", "", "2026-09-01T10:00:00Z"),
		ev("b", "m/x", "prompt", "", "2026-09-03T10:00:00Z"),
		ev("c", "m/y", "prompt", "", "2026-08-01T10:00:00Z"),
	}
	links := []rawLink{
		{Project: "m/x", URL: "https://github.com/o/r/pull/1", Author: "me", OpenedAt: ts("2026-09-02T10:00:00Z"), Merged: true, MergedAt: ts("2026-09-03T10:00:00Z")},
		{Project: "m/x", URL: "https://github.com/o/r/pull/2", Author: "jd", OpenedAt: ts("2026-09-04T10:00:00Z")},
		{Project: "m/x", URL: "https://github.com/o/r/pull/3", Author: "me", OpenedAt: ts("2026-09-04T11:00:00Z"), ClosedAt: ts("2026-09-05T10:00:00Z")},
	}
	pe := []rawProjectEvent{
		{"m/y", "archived", "done", ts("2026-08-10T10:00:00Z")},
		{"m/y", "reopened", "", ts("2026-08-20T10:00:00Z")},
		{"m/y", "archived", "scrapped", ts("2026-08-25T10:00:00Z")},
	}
	// chips are newest first, like the mockup
	rows, tot := projectRows([]string{"m/y", "m/x"}, evs, links, pe, "me", now)
	if len(rows) != 2 || rows[0].Path != "m/x" {
		t.Fatalf("ongoing must sort first: %+v", rows)
	}
	x, y := rows[0], rows[1]
	if x.Status != "ongoing" || x.Merged != 1 || x.Open != 1 || x.Closed != 1 || x.LeadDays != 2 || x.Sessions != 2 || x.FlightDays != 6 || len(x.PRs) != 3 || !x.PRs[2].Mine || x.PRs[1].Author != "jd" || x.PRs[1].Mine || x.PRs[2].Short != "o/r#1" || x.PRs[2].State != "merged" || x.PRs[0].State != "closed" {
		t.Fatalf("%+v", x)
	}
	if y.Status != "archived" || y.Rounds != 1 || y.FlightDays != 24 || !math.IsNaN(y.LeadDays) {
		t.Fatalf("%+v", y)
	}
	if tot.Ongoing != 1 || tot.Archived != 1 || tot.Merged != 1 || tot.Open != 1 || tot.Closed != 1 {
		t.Fatalf("%+v", tot)
	}
	// latest event reopened → status reopened
	pe = append(pe, rawProjectEvent{"m/y", "reopened", "", ts("2026-09-01T10:00:00Z")})
	rows, tot = projectRows([]string{"m/y"}, evs, nil, pe, "me", now)
	if rows[0].Status != "reopened" || rows[0].Rounds != 2 || tot.Reopened != 1 {
		t.Fatalf("%+v %+v", rows[0], tot)
	}
}

func TestFriction(t *testing.T) {
	from, now := ts("2026-08-08T00:00:00Z"), ts("2026-09-07T14:00:00Z")
	evs := []rawEvent{
		ev("a", "m/x", "prompt", "", "2026-09-01T10:00:00Z"),
		ev("a", "m/x", "notification", "permission_prompt", "2026-09-01T10:01:00Z"),
		ev("a", "m/x", "notification", "idle_prompt", "2026-09-01T10:02:00Z"),
		ev("a", "m/x", "pre_compact", "auto", "2026-09-01T10:03:00Z"),
		ev("a", "m/x", "pre_compact", "manual", "2026-09-01T10:04:00Z"),
		ev("b", "m/y", "prompt", "", "2026-09-02T10:00:00Z"),
		ev("b", "m/y", "prompt", "", "2026-09-02T10:05:00Z"),
		ev("c", "", "prompt", "", "2026-09-02T10:00:00Z"),
		// a question dialog: Claude Code also sends permission_prompt and idle
		// notifications while it is open; they must not count as approvals or
		// extra waits. The next prompt closes it; a later permission counts.
		ev("a", "m/x", "question", "", "2026-09-01T10:10:00Z"),
		ev("a", "m/x", "notification", "permission_prompt", "2026-09-01T10:10:01Z"),
		ev("a", "m/x", "notification", "idle_prompt", "2026-09-01T10:11:00Z"),
		ev("a", "m/x", "prompt", "", "2026-09-01T10:12:00Z"),
		ev("a", "m/x", "notification", "permission_prompt", "2026-09-01T10:13:00Z"),
		// prior window: one approval → delta 1 (two approvals now)
		ev("z", "m/x", "notification", "permission_prompt", "2026-07-20T10:00:00Z"),
	}
	links := []rawLink{{Project: "m/x", OpenedAt: ts("2026-09-03T10:00:00Z")}}
	perms := []rawPerm{{"a", "m/x", "Bash", "Bash(go test *)", ts("2026-09-01T10:01:00Z")}}
	f := friction(from, now, evs, links, perms)
	if f.Approvals != 2 || f.UserWaits != 2 || f.ApprovalDelta != 1 || f.CompTotal != 2 || f.CompAuto != 1 || f.CompManual != 1 {
		t.Fatalf("%+v", f)
	}
	if f.NoPRSessions != 1 || f.SessionsTotal != 2 || f.NoPRMedianPrompts != 2 {
		t.Fatalf("waste: %+v", f)
	}
	if len(f.Perms) != 1 || f.Perms[0].Rule != "Bash(go test *)" || f.Perms[0].N != 1 || len(f.Perms[0].Spark) < 4 {
		t.Fatalf("perms: %+v", f.Perms)
	}
	if len(f.Compactions) != 1 || f.Compactions[0].Path != "m/x" || f.Compactions[0].Auto != 1 || f.Compactions[0].Manual != 1 {
		t.Fatalf("compactions: %+v", f.Compactions)
	}
}

func TestLiveAndStrip(t *testing.T) {
	now := ts("2026-09-07T14:32:00Z")
	evs := []rawEvent{
		ev("a", "m/x", "prompt", "", "2026-09-07T13:40:00Z"),
		ev("a", "m/x", "stop", "", "2026-09-07T14:00:00Z"),
		ev("a", "m/x", "prompt", "", "2026-09-07T14:10:00Z"),
		ev("a", "m/x", "notification", "permission_prompt", "2026-09-07T14:30:00Z"),
		ev("q", "m/q", "prompt", "", "2026-09-07T14:20:00Z"),
	}
	states := []rawState{{"a", "m/x", "you", ts("2026-09-07T14:30:00Z")}, {"q", "m/q", "claude", ts("2026-09-07T14:20:00Z")}}
	links := []rawLink{{Project: "m/y", URL: "https://github.com/o/r/pull/9", Author: "jd", ActionNeeded: true, OpenedAt: now.Add(-3 * time.Hour), RefreshedAt: now.Add(-12 * time.Minute)}}
	open := map[string]bool{"p:m/x": true, "p:m/z": true, "p:m/q": true}
	l := live([]string{"m/x", "m/y", "m/z", "m/q"}, evs, states, links, open, nil, now, 5)
	if l.hidden != 0 || l.needInput != 2 || l.working != 1 || l.idle != 1 || len(l.rows) != 4 {
		t.Fatalf("%+v", l)
	}
	r := l.rows
	if r[0].State != "you" || r[0].Path != "m/x" || r[0].For != "2m 00s" || r[0].Why != "waiting for approval" {
		t.Fatalf("%+v", r[0])
	}
	if r[1].State != "review" || r[1].Path != "m/y" || r[1].Why != "reviewer waiting · o/r#9" || r[1].For != "3h 00m" {
		t.Fatalf("%+v", r[1])
	}
	// elly's last activity, when known, is the better "waiting since"
	links[0].EllyUpdatedAt = now.Add(-25 * time.Minute)
	if l2 := live([]string{"m/y"}, nil, nil, links, nil, nil, now, 5); l2.rows[0].For != "25m 00s" {
		t.Fatalf("%+v", l2.rows[0])
	}
	if r[2].State != "claude" || r[2].Path != "m/q" || r[2].Why != "turn 1 · started 14:20" {
		t.Fatalf("%+v", r[2])
	}
	if r[3].State != "idle" || r[3].Path != "m/z" {
		t.Fatalf("%+v", r[3])
	}
	// strip for m/x: 13:32–14:32 → idle 8, claude 20, you 10, claude 20, you 2
	want := []StripSeg{{"idle", 8}, {"claude", 20}, {"you", 10}, {"claude", 20}, {"you", 2}}
	if !reflect.DeepEqual(r[0].Strip, want) {
		t.Fatalf("%+v", r[0].Strip)
	}
	// away overrides user-side minutes; cap hides overflow
	away := []interval{{ts("2026-09-07T14:02:00Z"), ts("2026-09-07T14:06:00Z")}}
	l = live([]string{"m/x", "m/y", "m/z", "m/q"}, evs, states, links, open, away, now, 2)
	if l.hidden != 2 || len(l.rows) != 2 {
		t.Fatalf("%+v", l)
	}
	if l.rows[0].Strip[2] != (StripSeg{"you", 2}) || l.rows[0].Strip[3] != (StripSeg{"away", 4}) || l.rows[0].Strip[4] != (StripSeg{"you", 4}) {
		t.Fatalf("%+v", l.rows[0].Strip)
	}
	// no i3: idle rows omitted, everything else shown
	l = live([]string{"m/x", "m/z"}, evs, states, nil, nil, nil, now, 5)
	if len(l.rows) != 1 || l.rows[0].Path != "m/x" {
		t.Fatalf("%+v", l.rows)
	}
}

func TestFmtDurAndMedian(t *testing.T) {
	for in, want := range map[int]string{5: "5s", 65: "1m 05s", 3661: "1h 01m", 0: "0s"} {
		if got := fmtDur(in); got != want {
			t.Errorf("%d: %q", in, got)
		}
	}
	if median(nil) != 0 || median([]int{3}) != 3 || median([]int{1, 5, 3}) != 3 || median([]int{1, 2, 3, 10}) != 2 {
		t.Fatal("median")
	}
}

func TestComputeReportEmpty(t *testing.T) {
	now := ts("2026-09-07T14:32:00Z")
	r := computeReport(rawData{}, "30d", now.AddDate(0, 0, -30), now, themes["dark"])
	if r.Knee != -1 || len(r.Weeks) == 0 || r.EventCount != 0 || len(r.Live) != 0 || r.Range != "30d" {
		t.Fatalf("%+v", r)
	}
}
