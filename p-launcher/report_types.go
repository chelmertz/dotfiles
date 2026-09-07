package main

import "time"

// Report is everything the template renders: one struct per section of the
// four-question report. Built by computeReport from raw rows, or by
// demoReport from constants ported from report-design/gen.mjs.
type Report struct {
	GeneratedAt time.Time
	Range       string // "7d" | "30d" | "90d"
	From, To    time.Time
	Demo        bool
	Theme       Theme

	// S1: how many things should I run at once?
	Buckets      []Bucket
	Knee         int // index into Buckets, -1 when no data
	Weeks        []Week
	ActiveHours  int
	MergedTotal  int
	LastFullWeek *Week

	// S2: what did I finish, what is still open?
	Projects []ProjectRow
	Totals   ProjectTotals

	// S3: what friction should I remove?
	Approvals, UserWaits, ReviewOwed int
	ApprovalDelta                    int // vs the prior window of the same length
	ApprovalTrend                    []int
	Perms                            []PermRow
	Compactions                      []CompRow
	CompTotal, CompAuto, CompManual  int
	NoPRSessions, SessionsTotal      int
	NoPRMedianPrompts, NoPRBig       int

	// S4: what is waiting on me right now?
	Live                     []LiveRow
	LiveHidden               int
	NeedInput, Working, Idle int
	StripFrom, StripTo       time.Time

	// footer
	EventCount, LinkCount int
	RenderMillis          int64
	Notes                 []string // e.g. "i3 unreachable: idle rows omitted"
}

// Bucket is one concurrency level: how many sessions were active in an hour.
type Bucket struct {
	Label      string // "1".."5", "6+"
	Hours      int    // hours seen at this concurrency
	Merged     int
	PRsPerHour float64
	WaitSec    int // median blocked-wait, away excluded
	ReplySec   int // median stop → my next prompt
}

type Week struct {
	Label, Range      string // "W36", "Aug 31–Sep 06"
	Start             time.Time
	Conc, ConcMax     int
	Merged, Prompts   int
	WaitSec, ReplySec int
	Steer             float64 // prompts / merged, NaN when nothing merged
	Partial           bool
}

type ProjectRow struct {
	Path, Status         string // ongoing | reopened | archived
	Rounds               int    // reopened count
	Merged, Open, Closed int
	LeadDays             float64 // NaN when nothing merged
	FlightDays           int
	Sessions             int
	LastActive           string
	PRs                  []PRChip
	More                 int
}

type PRChip struct {
	URL, Short, Author string // Short: "owner/repo#123"
	Mine               bool
	Title, Desc        string
	Add, Del           int
	OpenedAgo, State   string // State: open | merged | closed
	Detail             string
	Demo               bool // force the hover card open (screenshots)
}

type ProjectTotals struct{ Merged, Open, Closed, Ongoing, Reopened, Archived int }

type PermRow struct {
	Path, Rule string
	N          int
	Spark      []int // per week
}

type CompRow struct {
	Path         string
	Auto, Manual float64 // compactions per session
}

type LiveRow struct {
	State, Label, Path, For, Why string // State: you | review | claude | idle
	Strip                        []StripSeg
}

// StripSeg is a run of minutes in one state on the 60-minute strip.
type StripSeg struct {
	State string // claude | you | idle | away
	Mins  int
}

// Theme is the token table from gen.mjs THEMES.
type Theme struct {
	Name, BG, Panel, Panel2, Border, Grid, Ink, Ink2, Ink3, Axis string
	Claude, You, Friction, Idle, YouTint, ClaudeTint, BarTrack   string
}

var themes = map[string]Theme{
	"dark":  {Name: "dark", BG: "#0f1113", Panel: "#16191c", Panel2: "#1b1f23", Border: "#262b31", Grid: "#23282d", Ink: "#e6e8eb", Ink2: "#9aa1a9", Ink3: "#6b727a", Axis: "#3a4046", Claude: "#3987e5", You: "#d95926", Friction: "#199e70", Idle: "#5c636b", YouTint: "rgba(217,89,38,0.10)", ClaudeTint: "rgba(57,135,229,0.12)", BarTrack: "rgba(255,255,255,0.06)"},
	"light": {Name: "light", BG: "#f3f3f0", Panel: "#fcfcfb", Panel2: "#f4f4f1", Border: "#dedcd5", Grid: "#e8e7e2", Ink: "#0b0b0b", Ink2: "#52514e", Ink3: "#898781", Axis: "#c3c2b7", Claude: "#2a78d6", You: "#eb6834", Friction: "#1baf7a", Idle: "#a3a19a", YouTint: "rgba(235,104,52,0.10)", ClaudeTint: "rgba(42,120,214,0.10)", BarTrack: "rgba(11,11,11,0.06)"},
}

// iconColor is the accent each state icon wears in the report.
func (t Theme) iconColor(name string) string {
	switch name {
	case "claude":
		return t.Claude
	case "you", "review", "reopened":
		return t.You
	case "idle":
		return t.Idle
	}
	return t.Ink3
}
