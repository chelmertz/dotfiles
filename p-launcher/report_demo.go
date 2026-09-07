package main

import (
	"math"
	"time"
)

// demoReport is the mockup's placeholder data (report-design/gen.mjs),
// ported so the layout can be reviewed before real data exists and stays
// reviewable after. Strips come from a port of the seeded generator.
func demoReport(now time.Time, T Theme) Report {
	r := Report{GeneratedAt: now, Range: "30d", From: now.AddDate(0, 0, -30), To: now, Demo: true, Theme: T,
		ActiveHours: 365, MergedTotal: 36, EventCount: 41206, LinkCount: 34, RenderMillis: 41,
		StripFrom: now.Add(-stripMinutes * time.Minute), StripTo: now}
	r.Buckets = []Bucket{
		{Label: "1", Hours: 61, PRsPerHour: 0.21, WaitSec: 110, ReplySec: 95},
		{Label: "2", Hours: 88, PRsPerHour: 0.34, WaitSec: 130, ReplySec: 120},
		{Label: "3", Hours: 102, PRsPerHour: 0.48, WaitSec: 160, ReplySec: 150},
		{Label: "4", Hours: 74, PRsPerHour: 0.56, WaitSec: 210, ReplySec: 190},
		{Label: "5", Hours: 31, PRsPerHour: 0.41, WaitSec: 440, ReplySec: 380},
		{Label: "6+", Hours: 9, PRsPerHour: 0.27, WaitSec: 840, ReplySec: 610},
	}
	r.Knee = 3
	r.Weeks = []Week{
		{Label: "W33", Range: "Aug 10–16", Conc: 2, ConcMax: 3, Merged: 6, WaitSec: 125, ReplySec: 118, Steer: 19.4},
		{Label: "W34", Range: "Aug 17–23", Conc: 3, ConcMax: 4, Merged: 9, WaitSec: 160, ReplySec: 140, Steer: 16.1},
		{Label: "W35", Range: "Aug 24–30", Conc: 4, ConcMax: 5, Merged: 11, WaitSec: 205, ReplySec: 175, Steer: 14.8},
		{Label: "W36", Range: "Aug 31–Sep 06", Conc: 5, ConcMax: 6, Merged: 9, WaitSec: 410, ReplySec: 320, Steer: 21.3},
		{Label: "W37", Range: "Sep 07 →", Conc: 4, ConcMax: 4, Merged: 1, WaitSec: 190, ReplySec: 160, Steer: 27.0, Partial: true},
	}
	w36 := r.Weeks[3]
	r.LastFullWeek = &w36

	chip := func(short string, mine bool, by string) PRChip {
		author := "chelmertz"
		if !mine {
			author = by
		}
		return PRChip{URL: prURL(short), Short: short, Author: author, Mine: mine, OpenedAgo: "—", State: "open"}
	}
	web401 := chip("matchi/matchi-web#401", false, "jd")
	web401.Demo = true
	web401.Title, web401.Desc = "Court booking: block double-submit on the payment step", "Adds a submit latch to the checkout form and disables the pay button until the intent response returns. Also removes the retry loop that produced the duplicate charge on 2026-08-29."
	web401.Add, web401.Del, web401.OpenedAgo, web401.Detail = 148, 62, "3d ago", "changes requested · 2 unresolved threads"
	web412 := chip("matchi/matchi-web#412", true, "")
	web412.Title, web412.Desc, web412.Add, web412.Del, web412.OpenedAgo = "Ingress: route /api/v2 through the new gateway", "Moves v2 traffic to the gateway service and drops the legacy path rewrite.", 88, 131, "1d ago"
	web409 := chip("matchi/matchi-web#409", true, "")
	web409.Title, web409.Desc, web409.Add, web409.Del, web409.OpenedAgo, web409.State = "Fix stale availability cache after cancellations", "Invalidate the slot cache on booking.cancelled events instead of on the 5-minute timer.", 41, 9, "4d ago", "merged"
	r.Projects = []ProjectRow{
		{Path: "m/matchi-web", Status: "ongoing", Merged: 7, Open: 2, Closed: 1, LeadDays: 1.6, FlightDays: 24, Sessions: 31, LastActive: "14:20 today", PRs: []PRChip{web412, web409, web401}, More: 7},
		{Path: "m/nginx-ingress", Status: "ongoing", Merged: 3, Open: 1, Closed: 0, LeadDays: 2.8, FlightDays: 11, Sessions: 12, LastActive: "14:31 today", PRs: []PRChip{chip("matchi/nginx-ingress#88", true, ""), chip("matchi/nginx-ingress#86", false, "ak")}, More: 2},
		{Path: "personal/p-launcher", Status: "ongoing", Merged: 5, Open: 1, Closed: 0, LeadDays: 0.6, FlightDays: 9, Sessions: 14, LastActive: "14:29 today", PRs: []PRChip{chip("chelmertz/p-launcher#14", true, ""), chip("chelmertz/p-launcher#12", true, ""), chip("chelmertz/p-launcher#11", true, "")}, More: 3},
		{Path: "m/matchi-api", Status: "reopened", Rounds: 2, Merged: 4, Open: 1, Closed: 1, LeadDays: 3.1, FlightDays: 38, Sessions: 17, LastActive: "14:28 today", PRs: []PRChip{chip("matchi/matchi-api#231", true, ""), chip("matchi/matchi-api#227", false, "jd"), chip("matchi/matchi-api#219", true, "")}, More: 3},
		{Path: "m/infra-terraform", Status: "ongoing", Merged: 2, LeadDays: 4.2, FlightDays: 16, Sessions: 9, LastActive: "13:20 today", PRs: []PRChip{chip("matchi/infra#57", true, ""), chip("matchi/infra#55", true, "")}},
		{Path: "m/booking-service", Status: "archived", Merged: 3, Closed: 1, LeadDays: 2.2, FlightDays: 21, Sessions: 11, LastActive: "Thu 16:41", PRs: []PRChip{chip("matchi/booking-service#140", true, ""), chip("matchi/booking-service#137", false, "ml")}, More: 2},
		{Path: "personal/dotfiles", Status: "archived", Merged: 2, LeadDays: 0.3, FlightDays: 4, Sessions: 5, LastActive: "Sat 10:12", PRs: []PRChip{chip("chelmertz/dotfiles#9", true, "")}, More: 1},
		{Path: "personal/notes", Status: "archived", LeadDays: math.NaN(), FlightDays: 2, Sessions: 1, LastActive: "Aug 26"},
	}
	r.Totals = ProjectTotals{Merged: 26, Open: 5, Closed: 3, Ongoing: 4, Reopened: 1, Archived: 3}

	r.Approvals, r.UserWaits, r.ReviewOwed, r.ApprovalDelta = 96, 212, 3, 18
	r.ApprovalTrend = []int{24, 21, 31, 26}
	r.Perms = []PermRow{
		{Path: "m/matchi-web", N: 34, Rule: "Bash(gh pr view *)", Spark: []int{6, 9, 11, 8}},
		{Path: "m/nginx-ingress", N: 22, Rule: "Bash(kubectl get *)", Spark: []int{3, 5, 8, 6}},
		{Path: "personal/p-launcher", N: 19, Rule: "Bash(go test *)", Spark: []int{0, 4, 7, 8}},
		{Path: "m/matchi-api", N: 12, Rule: "Bash(./gradlew test *)", Spark: []int{4, 3, 3, 2}},
		{Path: "m/infra-terraform", N: 7, Rule: "Bash(terraform plan *)", Spark: []int{1, 2, 2, 2}},
		{Path: "m/booking-service", N: 2, Rule: "WebFetch(domain:github.com)", Spark: []int{1, 1, 0, 0}},
	}
	r.Compactions = []CompRow{
		{Path: "m/matchi-web", Auto: 0.45, Manual: 0.10},
		{Path: "m/nginx-ingress", Auto: 0.33, Manual: 0.10},
		{Path: "m/matchi-api", Auto: 0.35, Manual: 0.05},
		{Path: "personal/p-launcher", Auto: 0.28, Manual: 0.05},
		{Path: "m/infra-terraform", Auto: 0.22, Manual: 0.03},
		{Path: "other (3)", Auto: 0.08},
	}
	r.CompTotal, r.CompAuto, r.CompManual = 14, 11, 3
	r.NoPRSessions, r.SessionsTotal, r.NoPRMedianPrompts, r.NoPRBig = 14, 38, 4, 3

	live := []LiveRow{
		{State: "you", Label: "NEED INPUT", Path: "personal/p-launcher", For: "2m 14s", Why: "waiting for approval · Bash(go test ./...)"},
		{State: "you", Label: "NEED INPUT", Path: "m/matchi-web", For: "11m 48s", Why: "waiting for user · turn finished"},
		{State: "review", Label: "REVIEW", Path: "m/matchi-web", For: "3h 12m", Why: "reviewer waiting · matchi/matchi-web#401 · 2 unresolved threads"},
		{State: "claude", Label: "WORKING", Path: "m/nginx-ingress", For: "0m 37s", Why: "turn 23 · started 11:15"},
		{State: "claude", Label: "WORKING", Path: "m/matchi-api", For: "4m 02s", Why: "turn 6 · compacted 14:27"},
	}
	for i := range live {
		live[i].Strip = demoStrip(live[i].State, uint32(100+i))
	}
	r.Live, r.LiveHidden = live, 1
	r.NeedInput, r.Working, r.Idle = 3, 2, 1
	return r
}

func prURL(short string) string {
	for i := len(short) - 1; i >= 0; i-- {
		if short[i] == '#' {
			return "https://github.com/" + short[:i] + "/pull/" + short[i+1:]
		}
	}
	return "https://github.com/" + short
}

// mulberry32 is the seeded generator from gen.mjs, so demo strips match the
// mockup exactly.
func mulberry32(seed uint32) func() float64 {
	a := seed
	return func() float64 {
		a += 0x6D2B79F5
		t := a
		t = (t ^ (t >> 15)) * (1 | t)
		t = (t + (t^(t>>7))*(61|t)) ^ t
		return float64(t^(t>>14)) / 4294967296
	}
}

// demoStrip ports strip(final, seed) from gen.mjs, including the 20-minute
// away window at minutes 13–33.
func demoStrip(final string, seed uint32) []StripSeg {
	r := mulberry32(seed)
	var mins []string
	for len(mins) < stripMinutes {
		var st string
		if final == "idle" && len(mins) > 30 {
			st = "idle"
		} else if r() < 0.55 {
			st = "claude"
		} else if r() < 0.7 {
			st = "you"
		} else {
			st = "idle"
		}
		span := 4.0
		if st == "claude" {
			span = 6
		}
		m := 1 + int(math.Round(r()*span))
		for k := 0; k < m && len(mins) < stripMinutes; k++ {
			mins = append(mins, st)
		}
	}
	tail := final
	if final == "review" {
		tail = "you"
	}
	last := 30
	switch final {
	case "you", "review":
		last = 2
	case "claude":
		last = 1
	}
	for i := stripMinutes - last; i < stripMinutes; i++ {
		mins[i] = tail
	}
	for i := 13; i < 33; i++ {
		if mins[i] == "you" || (mins[i] == "idle" && final != "idle") {
			mins[i] = "away"
		}
	}
	var segs []StripSeg
	for _, st := range mins {
		if n := len(segs); n > 0 && segs[n-1].State == st {
			segs[n-1].Mins++
		} else {
			segs = append(segs, StripSeg{State: st, Mins: 1})
		}
	}
	return segs
}
