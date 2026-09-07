package main

import (
	"fmt"
	"math"
	"sort"
	"strings"
	"time"
)

// Raw rows as loaded from SQLite (report_load.go) or synthesised by tests.
// Everything below is pure: no I/O, no clock, so it can be tested exactly.

type rawEvent struct {
	SessionID    string
	Project      string // "" when the event has no project
	Kind, Detail string
	At           time.Time
}

type rawLink struct {
	Project, URL, Author, Title, Body         string
	Add, Del                                  int
	OpenedAt, ClosedAt, MergedAt, RefreshedAt time.Time // zero when unknown
	Merged, ActionNeeded                      bool
	Rounds                                    int // automatic tend rounds
}

type rawProjectEvent struct {
	Project, Kind, Detail string
	At                    time.Time
}

type rawPerm struct {
	SessionID, Project, Tool, Rule string
	At                             time.Time
}

type rawState struct {
	SessionID, Project, State string
	Since                     time.Time
}

type rawData struct {
	Events   []rawEvent // sorted by At; covers the window plus the prior window
	Links    []rawLink
	PEvents  []rawProjectEvent
	Perms    []rawPerm
	States   []rawState
	Projects []string        // every project path, in list order
	Open     map[string]bool // tag → has an open window; nil when i3 is unreachable
	Me       string          // GitHub login, for PRChip.Mine
	Notes    []string
}

type interval struct{ from, to time.Time }

type wait struct {
	session, project string
	start            time.Time
	dur              time.Duration // away excluded, capped at maxWait
	kind             string        // stop | permission | user
}

const (
	desktopSession = "desktop" // lock/unlock events, never a Claude session
	awayGap        = 15 * time.Minute
	maxWait        = 4 * time.Hour
	staleLive      = 24 * time.Hour
	stripMinutes   = 60
	chipsPerRow    = 3
)

// awayIntervals finds when the user was not at the keyboard: gaps of at
// least gap between prompts in any session (including the tail up to now),
// plus lock..unlock spans from the desktop pseudo-session. Merged, sorted.
func awayIntervals(evs []rawEvent, gap time.Duration, now time.Time) []interval {
	var prompts []time.Time
	var out []interval
	var lock time.Time
	for _, e := range evs {
		switch {
		case e.SessionID == desktopSession && e.Kind == "lock":
			if lock.IsZero() {
				lock = e.At
			}
		case e.SessionID == desktopSession && e.Kind == "unlock":
			if !lock.IsZero() {
				out = append(out, interval{lock, e.At})
				lock = time.Time{}
			}
		case e.SessionID != desktopSession && e.Kind == "prompt":
			prompts = append(prompts, e.At)
		}
	}
	if !lock.IsZero() && now.After(lock) {
		out = append(out, interval{lock, now})
	}
	sort.Slice(prompts, func(i, j int) bool { return prompts[i].Before(prompts[j]) })
	for i := 0; i < len(prompts); i++ {
		end := now
		if i+1 < len(prompts) {
			end = prompts[i+1]
		}
		if end.Sub(prompts[i]) >= gap {
			out = append(out, interval{prompts[i], end})
		}
	}
	return mergeIntervals(out)
}

func mergeIntervals(in []interval) []interval {
	sort.Slice(in, func(i, j int) bool { return in[i].from.Before(in[j].from) })
	var out []interval
	for _, iv := range in {
		if n := len(out); n > 0 && !iv.from.After(out[n-1].to) {
			if iv.to.After(out[n-1].to) {
				out[n-1].to = iv.to
			}
			continue
		}
		out = append(out, iv)
	}
	return out
}

// overlap is how much of [from, to) lies inside the away intervals.
func overlap(from, to time.Time, away []interval) time.Duration {
	var d time.Duration
	for _, a := range away {
		s, e := maxTime(from, a.from), minTime(to, a.to)
		if e.After(s) {
			d += e.Sub(s)
		}
	}
	return d
}

func maxTime(a, b time.Time) time.Time {
	if a.After(b) {
		return a
	}
	return b
}

func minTime(a, b time.Time) time.Time {
	if a.Before(b) {
		return a
	}
	return b
}

// waitKind says whether an event hands the ball to the user and how.
func waitKind(e rawEvent) string {
	switch {
	case e.Kind == "stop":
		return "stop"
	case e.Kind == "notification" && e.Detail == "permission_prompt":
		return "permission"
	case e.Kind == "notification" && waitsOnUser[e.Detail]:
		return "user"
	}
	return ""
}

// waits finds every completed wait: a hand-over to the user followed by the
// user's next prompt in the same session. A second hand-over before the
// prompt extends the same wait; a session_end cancels it. Away time inside
// the wait is subtracted, then the result is capped at maxWait.
func waits(evs []rawEvent, away []interval) []wait {
	pending := map[string]*wait{}
	var out []wait
	for _, e := range evs {
		if e.SessionID == desktopSession {
			continue
		}
		switch {
		case e.Kind == "prompt":
			if w, ok := pending[e.SessionID]; ok {
				raw := e.At.Sub(w.start) - overlap(w.start, e.At, away)
				if raw < 0 {
					raw = 0
				}
				if raw > maxWait {
					raw = maxWait
				}
				w.dur = raw
				out = append(out, *w)
				delete(pending, e.SessionID)
			}
		case e.Kind == "session_end":
			delete(pending, e.SessionID)
		default:
			if k := waitKind(e); k != "" {
				if _, ok := pending[e.SessionID]; !ok {
					pending[e.SessionID] = &wait{session: e.SessionID, project: e.Project, start: e.At, kind: k}
				}
			}
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].start.Before(out[j].start) })
	return out
}

func hourOf(t time.Time) time.Time { return t.UTC().Truncate(time.Hour) }

// hourConcurrency counts distinct Claude sessions with any event per hour.
func hourConcurrency(evs []rawEvent) map[time.Time]int {
	sets := map[time.Time]map[string]bool{}
	for _, e := range evs {
		if e.SessionID == desktopSession {
			continue
		}
		h := hourOf(e.At)
		if sets[h] == nil {
			sets[h] = map[string]bool{}
		}
		sets[h][e.SessionID] = true
	}
	out := make(map[time.Time]int, len(sets))
	for h, s := range sets {
		out[h] = len(s)
	}
	return out
}

var bucketLabels = []string{"1", "2", "3", "4", "5", "6+"}

func bucketIndex(conc int) int {
	if conc < 1 {
		return -1
	}
	if conc > 6 {
		conc = 6
	}
	return conc - 1
}

// buckets groups hours, merged PRs and waits by the concurrency of their
// hour. knee is the bucket with the most merged PRs per hour, -1 when empty.
func buckets(conc map[time.Time]int, links []rawLink, ws []wait) ([]Bucket, int) {
	out := make([]Bucket, len(bucketLabels))
	waitsBy := make([][]int, len(bucketLabels))
	repliesBy := make([][]int, len(bucketLabels))
	for i := range out {
		out[i].Label = bucketLabels[i]
	}
	for _, c := range conc {
		if i := bucketIndex(c); i >= 0 {
			out[i].Hours++
		}
	}
	for _, l := range links {
		if !l.Merged || l.MergedAt.IsZero() {
			continue
		}
		if i := bucketIndex(conc[hourOf(l.MergedAt)]); i >= 0 {
			out[i].Merged++
		}
	}
	for _, w := range ws {
		i := bucketIndex(conc[hourOf(w.start)])
		if i < 0 {
			continue
		}
		sec := int(w.dur / time.Second)
		waitsBy[i] = append(waitsBy[i], sec)
		if w.kind == "stop" {
			repliesBy[i] = append(repliesBy[i], sec)
		}
	}
	knee := -1
	for i := range out {
		if out[i].Hours > 0 {
			out[i].PRsPerHour = float64(out[i].Merged) / float64(out[i].Hours)
			// a knee needs a merge; all-zero buckets highlight nothing
			if out[i].Merged > 0 && (knee < 0 || out[i].PRsPerHour > out[knee].PRsPerHour) {
				knee = i
			}
		}
		out[i].WaitSec = median(waitsBy[i])
		out[i].ReplySec = median(repliesBy[i])
	}
	return out, knee
}

// weekStart is the Monday 00:00 of t's ISO week, in t's location.
func weekStart(t time.Time) time.Time {
	y, m, d := t.Date()
	day := time.Date(y, m, d, 0, 0, 0, 0, t.Location())
	wd := int(day.Weekday()+6) % 7 // Monday = 0
	return day.AddDate(0, 0, -wd)
}

func weekLabel(t time.Time) string {
	_, w := t.ISOWeek()
	return fmt.Sprintf("W%02d", w)
}

func weekRange(start time.Time) string {
	end := start.AddDate(0, 0, 6)
	if start.Month() == end.Month() {
		return fmt.Sprintf("%s %02d–%02d", start.Format("Jan"), start.Day(), end.Day())
	}
	return fmt.Sprintf("%s %02d–%s %02d", start.Format("Jan"), start.Day(), end.Format("Jan"), end.Day())
}

// weeks builds one row per ISO week from the week of from to the week of
// now (in now's location). Steer is NaN when nothing merged that week.
func weeks(from, now time.Time, evs []rawEvent, links []rawLink, ws []wait, conc map[time.Time]int) []Week {
	loc := now.Location()
	var out []Week
	for ws0 := weekStart(from.In(loc)); !ws0.After(now); ws0 = ws0.AddDate(0, 0, 7) {
		we := ws0.AddDate(0, 0, 7)
		w := Week{Label: weekLabel(ws0), Range: weekRange(ws0), Start: ws0, Partial: we.After(now)}
		for _, e := range evs {
			if e.Kind == "prompt" && e.SessionID != desktopSession && in(e.At, ws0, we) {
				w.Prompts++
			}
		}
		for _, l := range links {
			if l.Merged && in(l.MergedAt, ws0, we) {
				w.Merged++
			}
		}
		var waitS, replyS, concs []int
		for _, x := range ws {
			if in(x.start, ws0, we) {
				sec := int(x.dur / time.Second)
				waitS = append(waitS, sec)
				if x.kind == "stop" {
					replyS = append(replyS, sec)
				}
			}
		}
		for h, c := range conc {
			if in(h, ws0, we) {
				concs = append(concs, c)
				if c > w.ConcMax {
					w.ConcMax = c
				}
			}
		}
		w.WaitSec, w.ReplySec, w.Conc = median(waitS), median(replyS), median(concs)
		w.Steer = math.NaN()
		if w.Merged > 0 {
			w.Steer = float64(w.Prompts) / float64(w.Merged)
		}
		out = append(out, w)
	}
	return out
}

// in reports from <= t < to.
func in(t, from, to time.Time) bool { return !t.Before(from) && t.Before(to) }

// shortPR turns https://github.com/o/r/pull/1 into o/r#1; other URLs are
// returned as their host-less path.
func shortPR(url string) string {
	s := strings.TrimPrefix(strings.TrimPrefix(url, "https://"), "http://")
	s = strings.TrimPrefix(s, "github.com/")
	if i := strings.Index(s, "/pull/"); i >= 0 {
		return s[:i] + "#" + s[i+len("/pull/"):]
	}
	return s
}

func linkState(l rawLink) string {
	switch {
	case l.Merged:
		return "merged"
	case !l.ClosedAt.IsZero():
		return "closed"
	}
	return "open"
}

func agoShort(t, now time.Time) string {
	if t.IsZero() {
		return "—"
	}
	d := now.Sub(t)
	switch {
	case d < time.Hour:
		return fmt.Sprintf("%dm ago", int(d/time.Minute))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh ago", int(d/time.Hour))
	}
	return fmt.Sprintf("%dd ago", int(d/(24*time.Hour)))
}

// whenShort renders a past time relative to now: "14:20 today", "Thu 16:41",
// "Aug 26". Uses now's location.
func whenShort(t, now time.Time) string {
	if t.IsZero() {
		return "—"
	}
	t = t.In(now.Location())
	ny, nm, nd := now.Date()
	ty, tm, td := t.Date()
	switch {
	case ny == ty && nm == tm && nd == td:
		return t.Format("15:04") + " today"
	case now.Sub(t) < 6*24*time.Hour:
		return t.Format("Mon 15:04")
	}
	return t.Format("Jan 02")
}

// projectRows builds section 2: ongoing and reopened projects first (last
// active desc), archived last (archived desc). Lead time per merged link is
// merged_at minus the project's first prompt after the previous merge.
func projectRows(paths []string, evs []rawEvent, links []rawLink, pe []rawProjectEvent, me string, now time.Time) ([]ProjectRow, ProjectTotals) {
	type acc struct {
		sessions map[string]bool
		prompts  []time.Time
		last     time.Time
		first    time.Time
	}
	by := map[string]*acc{}
	get := func(p string) *acc {
		if by[p] == nil {
			by[p] = &acc{sessions: map[string]bool{}}
		}
		return by[p]
	}
	for _, e := range evs {
		if e.Project == "" || e.SessionID == desktopSession {
			continue
		}
		a := get(e.Project)
		a.sessions[e.SessionID] = true
		if e.At.After(a.last) {
			a.last = e.At
		}
		if a.first.IsZero() || e.At.Before(a.first) {
			a.first = e.At
		}
		if e.Kind == "prompt" {
			a.prompts = append(a.prompts, e.At)
		}
	}
	linksBy := map[string][]rawLink{}
	for _, l := range links {
		linksBy[l.Project] = append(linksBy[l.Project], l)
	}
	peBy := map[string][]rawProjectEvent{}
	for _, x := range pe {
		peBy[x.Project] = append(peBy[x.Project], x)
	}
	var rows []ProjectRow
	var tot ProjectTotals
	lastActive := map[string]time.Time{}
	archivedAt := map[string]time.Time{}
	for _, p := range paths {
		a := get(p)
		sort.Slice(a.prompts, func(i, j int) bool { return a.prompts[i].Before(a.prompts[j]) })
		r := ProjectRow{Path: p, Status: "ongoing", Sessions: len(a.sessions), LeadDays: math.NaN(), LastActive: whenShort(a.last, now)}
		lastActive[p] = a.last
		pes := peBy[p]
		sort.Slice(pes, func(i, j int) bool { return pes[i].At.Before(pes[j].At) })
		start := a.first
		for _, x := range pes {
			switch x.Kind {
			case "reopened":
				r.Rounds++
			case "created":
				if start.IsZero() || x.At.Before(start) {
					start = x.At
				}
			}
		}
		end := now
		if n := len(pes); n > 0 {
			switch last := pes[n-1]; last.Kind {
			case "archived":
				r.Status = "archived"
				end = last.At
				archivedAt[p] = end
			case "reopened":
				r.Status = "reopened"
			case "snoozed":
				if parseTime(last.Detail).After(now) {
					r.Status = "snoozed"
				}
			}
		}
		if !start.IsZero() {
			r.FlightDays = int(end.Sub(start).Hours() / 24)
		}
		ls := linksBy[p]
		sort.Slice(ls, func(i, j int) bool { return ls[i].OpenedAt.After(ls[j].OpenedAt) })
		var merged []rawLink
		for _, l := range ls {
			r.AutoRounds += l.Rounds
			switch linkState(l) {
			case "merged":
				r.Merged++
				merged = append(merged, l)
			case "closed":
				r.Closed++
			default:
				r.Open++
			}
			if len(r.PRs) < chipsPerRow {
				r.PRs = append(r.PRs, PRChip{URL: l.URL, Short: shortPR(l.URL), Author: l.Author, Mine: l.Author != "" && l.Author == me,
					Title: l.Title, Desc: l.Body, Add: l.Add, Del: l.Del, OpenedAgo: agoShort(l.OpenedAt, now), State: linkState(l)})
			} else {
				r.More++
			}
		}
		sort.Slice(merged, func(i, j int) bool { return merged[i].MergedAt.Before(merged[j].MergedAt) })
		var leads []int
		var prev time.Time
		for _, l := range merged {
			for _, pt := range a.prompts {
				if pt.After(prev) && !pt.After(l.MergedAt) {
					leads = append(leads, int(l.MergedAt.Sub(pt)/time.Second))
					break
				}
			}
			prev = l.MergedAt
		}
		if len(leads) > 0 {
			r.LeadDays = float64(median(leads)) / 86400
		}
		switch r.Status {
		case "archived":
			tot.Archived++
		case "reopened":
			tot.Reopened++
		default:
			tot.Ongoing++
		}
		tot.Merged += r.Merged
		tot.Open += r.Open
		tot.Closed += r.Closed
		rows = append(rows, r)
	}
	sort.SliceStable(rows, func(i, j int) bool {
		ai, aj := rows[i].Status == "archived", rows[j].Status == "archived"
		if ai != aj {
			return !ai
		}
		if ai {
			return archivedAt[rows[i].Path].After(archivedAt[rows[j].Path])
		}
		return lastActive[rows[i].Path].After(lastActive[rows[j].Path])
	})
	return rows, tot
}

type frictionOut struct {
	Approvals, UserWaits, ApprovalDelta int
	CompTotal, CompAuto, CompManual     int
	NoPRSessions, SessionsTotal         int
	NoPRMedianPrompts, NoPRBig          int
	Perms                               []PermRow
	Compactions                         []CompRow
	ApprovalTrend                       []int
}

// friction builds section 3 from the window [from, now); events before from
// (the prior window) only feed ApprovalDelta.
func friction(from, now time.Time, evs []rawEvent, links []rawLink, perms []rawPerm) frictionOut {
	var f frictionOut
	priorFrom := from.Add(-now.Sub(from))
	prior := 0
	type sess struct {
		project string
		first   time.Time
		prompts int
	}
	sessions := map[string]*sess{}
	comp := map[string]*[2]int{} // project → auto, manual
	sessBy := map[string]map[string]bool{}
	nWeeks := len(weeks(from, now, nil, nil, nil, nil))
	f.ApprovalTrend = make([]int, nWeeks)
	for _, e := range evs {
		if e.SessionID == desktopSession {
			continue
		}
		if in(e.At, priorFrom, from) && e.Kind == "notification" && e.Detail == "permission_prompt" {
			prior++
		}
		if !in(e.At, from, now) {
			continue
		}
		if e.Project != "" {
			if sessBy[e.Project] == nil {
				sessBy[e.Project] = map[string]bool{}
			}
			sessBy[e.Project][e.SessionID] = true
		}
		switch e.Kind {
		case "notification":
			if e.Detail == "permission_prompt" {
				f.Approvals++
				if wi := weekIndex(e.At, from, now); wi >= 0 && wi < nWeeks {
					f.ApprovalTrend[wi]++
				}
			} else if waitsOnUser[e.Detail] {
				f.UserWaits++
			}
		case "pre_compact":
			f.CompTotal++
			if comp[e.Project] == nil {
				comp[e.Project] = &[2]int{}
			}
			if e.Detail == "manual" {
				f.CompManual++
				comp[e.Project][1]++
			} else {
				f.CompAuto++
				comp[e.Project][0]++
			}
		case "prompt":
			if e.Project == "" {
				continue
			}
			s := sessions[e.SessionID]
			if s == nil {
				s = &sess{project: e.Project, first: e.At}
				sessions[e.SessionID] = s
			}
			s.prompts++
		}
	}
	f.ApprovalDelta = f.Approvals - prior
	// waste: sessions whose project got no link within 7 days of the first prompt
	var noPRPrompts []int
	for _, s := range sessions {
		f.SessionsTotal++
		got := false
		for _, l := range links {
			if l.Project == s.project && !l.OpenedAt.IsZero() && in(l.OpenedAt, s.first, s.first.Add(7*24*time.Hour)) {
				got = true
				break
			}
		}
		if !got {
			f.NoPRSessions++
			noPRPrompts = append(noPRPrompts, s.prompts)
			if s.prompts > 30 {
				f.NoPRBig++
			}
		}
	}
	f.NoPRMedianPrompts = median(noPRPrompts)
	// compactions per session by project
	for p, c := range comp {
		n := len(sessBy[p])
		if n == 0 {
			n = 1
		}
		f.Compactions = append(f.Compactions, CompRow{Path: orDash(p), Auto: float64(c[0]) / float64(n), Manual: float64(c[1]) / float64(n)})
	}
	sort.Slice(f.Compactions, func(i, j int) bool {
		return f.Compactions[i].Auto+f.Compactions[i].Manual > f.Compactions[j].Auto+f.Compactions[j].Manual
	})
	if len(f.Compactions) > 6 {
		rest := f.Compactions[5:]
		other := CompRow{Path: fmt.Sprintf("other (%d)", len(rest))}
		for _, r := range rest {
			other.Auto += r.Auto
			other.Manual += r.Manual
		}
		other.Auto /= float64(len(rest))
		other.Manual /= float64(len(rest))
		f.Compactions = append(f.Compactions[:5], other)
	}
	// permission asks by project and rule
	type key struct{ project, rule string }
	pr := map[key]*PermRow{}
	for _, p := range perms {
		if !in(p.At, from, now) {
			continue
		}
		k := key{p.Project, p.Rule}
		if pr[k] == nil {
			pr[k] = &PermRow{Path: orDash(p.Project), Rule: p.Rule, Spark: make([]int, nWeeks)}
		}
		pr[k].N++
		if wi := weekIndex(p.At, from, now); wi >= 0 && wi < nWeeks {
			pr[k].Spark[wi]++
		}
	}
	for _, r := range pr {
		f.Perms = append(f.Perms, *r)
	}
	sort.Slice(f.Perms, func(i, j int) bool {
		if f.Perms[i].N != f.Perms[j].N {
			return f.Perms[i].N > f.Perms[j].N
		}
		return f.Perms[i].Path+f.Perms[i].Rule < f.Perms[j].Path+f.Perms[j].Rule
	})
	if len(f.Perms) > 6 {
		f.Perms = f.Perms[:6]
	}
	return f
}

func orDash(p string) string {
	if p == "" {
		return "(no project)"
	}
	return p
}

// weekIndex is the 0-based ISO week offset of t from the week of from.
func weekIndex(t, from, now time.Time) int {
	loc := now.Location()
	return int(weekStart(t.In(loc)).Sub(weekStart(from.In(loc))).Hours() / (24 * 7))
}

type liveOut struct {
	rows                     []LiveRow
	hidden                   int
	needInput, working, idle int
}

// live builds section 4: needs-you first (Claude states, then PRs waiting on
// a review reply), then working, then idle windows. Idle rows need i3's view
// of open windows; with open == nil they are omitted. Rows beyond max are
// counted in hidden but still in the bucket totals.
func live(paths []string, evs []rawEvent, states []rawState, links []rawLink, open map[string]bool, away []interval, now time.Time, max int) liveOut {
	var l liveOut
	// newest live state per project; "you" wins over "claude"
	best := map[string]rawState{}
	for _, s := range states {
		if now.Sub(s.Since) > staleLive {
			continue
		}
		cur, ok := best[s.Project]
		if !ok || (s.State == "you" && cur.State != "you") || (s.State == cur.State && s.Since.After(cur.Since)) {
			best[s.Project] = s
		}
	}
	bySession := map[string][]rawEvent{}
	newestSession := map[string]string{}
	newestAt := map[string]time.Time{}
	for _, e := range evs {
		if e.SessionID == desktopSession {
			continue
		}
		bySession[e.SessionID] = append(bySession[e.SessionID], e)
		if e.Project != "" && e.At.After(newestAt[e.Project]) {
			newestAt[e.Project] = e.At
			newestSession[e.Project] = e.SessionID
		}
	}
	var you, review, work, idle []LiveRow
	covered := map[string]bool{}
	for _, p := range paths {
		s, ok := best[p]
		if !ok {
			continue
		}
		covered[p] = true
		sev := bySession[s.SessionID]
		row := LiveRow{State: s.State, Path: p, For: fmtDur(int(now.Sub(s.Since) / time.Second)), Strip: stripFor(sev, away, now, false)}
		if s.State == "you" {
			row.Label = "NEED INPUT"
			row.Why = "waiting for user · turn finished"
			if n := len(sev); n > 0 && sev[n-1].Kind == "notification" && sev[n-1].Detail == "permission_prompt" {
				row.Why = "waiting for approval"
			}
			you = append(you, row)
		} else {
			row.Label = "WORKING"
			turns, started := 0, time.Time{}
			for _, e := range sev {
				if e.Kind == "prompt" {
					turns++
					started = e.At
				}
			}
			row.Why = fmt.Sprintf("turn %d · started %s", turns, started.In(now.Location()).Format("15:04"))
			work = append(work, row)
		}
	}
	for _, lk := range links {
		if !lk.ActionNeeded {
			continue
		}
		covered[lk.Project] = true
		review = append(review, LiveRow{State: "review", Label: "REVIEW", Path: lk.Project, For: fmtDur(int(now.Sub(lk.OpenedAt) / time.Second)),
			Why: "reviewer waiting · " + shortPR(lk.URL), Strip: stripFor(bySession[newestSession[lk.Project]], away, now, false)})
	}
	if open != nil {
		for _, p := range paths {
			if covered[p] || !open[tagFor(p)] {
				continue
			}
			row := LiveRow{State: "idle", Label: "IDLE", Path: p, Strip: stripFor(bySession[newestSession[p]], away, now, true)}
			if sev := bySession[newestSession[p]]; len(sev) > 0 {
				last := sev[len(sev)-1]
				row.For = fmtDur(int(now.Sub(last.At) / time.Second))
				if last.Kind == "session_end" {
					row.Why = "session ended " + last.At.In(now.Location()).Format("15:04")
				}
			}
			idle = append(idle, row)
		}
	}
	l.needInput, l.working, l.idle = len(you)+len(review), len(work), len(idle)
	all := append(append(append(you, review...), work...), idle...)
	if len(all) > max {
		l.hidden = len(all) - max
		all = all[:max]
	}
	l.rows = all
	return l
}

// stripFor renders the last stripMinutes of one session as state segments.
// The state of a minute is set by the last event at or before its start:
// prompt → claude, a hand-over → you, session_end → idle. Away intervals
// overwrite user-side minutes unless the row itself is idle.
func stripFor(sev []rawEvent, away []interval, now time.Time, idleRow bool) []StripSeg {
	from := now.Add(-stripMinutes * time.Minute)
	var segs []StripSeg
	push := func(st string) {
		if n := len(segs); n > 0 && segs[n-1].State == st {
			segs[n-1].Mins++
			return
		}
		segs = append(segs, StripSeg{State: st, Mins: 1})
	}
	for i := 0; i < stripMinutes; i++ {
		m := from.Add(time.Duration(i) * time.Minute)
		st := "idle"
		for _, e := range sev {
			if e.At.After(m) {
				break
			}
			switch {
			case e.Kind == "prompt":
				st = "claude"
			case waitKind(e) != "":
				st = "you"
			case e.Kind == "session_end":
				st = "idle"
			}
		}
		if !idleRow && st != "claude" && overlap(m, m.Add(time.Minute), away) > 0 {
			st = "away"
		}
		push(st)
	}
	return segs
}

// fmtDur renders seconds as "5s", "1m 05s" or "1h 01m".
func fmtDur(sec int) string {
	switch {
	case sec >= 3600:
		return fmt.Sprintf("%dh %02dm", sec/3600, (sec%3600)/60)
	case sec >= 60:
		return fmt.Sprintf("%dm %02ds", sec/60, sec%60)
	}
	return fmt.Sprintf("%ds", sec)
}

// median of ints; the mean of the two middle values for an even count.
func median(xs []int) int {
	if len(xs) == 0 {
		return 0
	}
	s := append([]int(nil), xs...)
	sort.Ints(s)
	n := len(s)
	if n%2 == 1 {
		return s[n/2]
	}
	return (s[n/2-1] + s[n/2]) / 2
}

func filterEvents(evs []rawEvent, from, to time.Time) []rawEvent {
	var out []rawEvent
	for _, e := range evs {
		if in(e.At, from, to) {
			out = append(out, e)
		}
	}
	return out
}

// computeReport is the glue from raw rows to the rendered struct.
func computeReport(raw rawData, rng string, from, now time.Time, theme Theme) Report {
	win := filterEvents(raw.Events, from, now)
	away := awayIntervals(win, awayGap, now)
	ws := waits(win, away)
	conc := hourConcurrency(win)
	r := Report{GeneratedAt: now, Range: rng, From: from, To: now, Theme: theme, Notes: raw.Notes,
		EventCount: len(win), LinkCount: len(raw.Links), ActiveHours: len(conc),
		StripFrom: now.Add(-stripMinutes * time.Minute), StripTo: now}
	r.Buckets, r.Knee = buckets(conc, raw.Links, ws)
	r.Weeks = weeks(from, now, win, raw.Links, ws, conc)
	for i := len(r.Weeks) - 1; i >= 0; i-- {
		if !r.Weeks[i].Partial {
			w := r.Weeks[i]
			r.LastFullWeek = &w
			break
		}
	}
	for _, l := range raw.Links {
		if l.Merged && in(l.MergedAt, from, now) {
			r.MergedTotal++
		}
		if l.ActionNeeded {
			r.ReviewOwed++
		}
	}
	for _, e := range win {
		if e.Kind == "tend" {
			r.TendSessions++
		}
	}
	r.Projects, r.Totals = projectRows(raw.Projects, raw.Events, raw.Links, raw.PEvents, raw.Me, now)
	f := friction(from, now, raw.Events, raw.Links, raw.Perms)
	r.Approvals, r.UserWaits, r.ApprovalDelta, r.ApprovalTrend = f.Approvals, f.UserWaits, f.ApprovalDelta, f.ApprovalTrend
	r.Perms, r.Compactions = f.Perms, f.Compactions
	r.CompTotal, r.CompAuto, r.CompManual = f.CompTotal, f.CompAuto, f.CompManual
	r.NoPRSessions, r.SessionsTotal, r.NoPRMedianPrompts, r.NoPRBig = f.NoPRSessions, f.SessionsTotal, f.NoPRMedianPrompts, f.NoPRBig
	l := live(raw.Projects, raw.Events, raw.States, raw.Links, raw.Open, away, now, 5)
	r.Live, r.LiveHidden, r.NeedInput, r.Working, r.Idle = l.rows, l.hidden, l.needInput, l.working, l.idle
	if raw.Open == nil {
		r.Notes = append(r.Notes, "i3 unreachable: idle rows omitted")
	}
	return r
}
