package main

import (
	"html"
	"time"
)

// Row is one rofi line. Path is "" only for rows that must not open anything
// (none are produced today; selectRow still guards it).
type Row struct {
	Text   string
	Path   string // project path; "" for docked action rows
	Icon   string // icon name (see iconPaths), presentation only
	Action string // docked row action: "archived" | "report"; "" for projects
}

// Rows renders projects (already sorted) into rofi lines: the folder name,
// then the namespace label in a muted span; the state is a row icon (see
// stateIcon), not text. rofi cannot make rows unselectable or skip them while
// navigating, so grouping lives inside each row instead of in heading rows.
// The two columns are tab-separated and aligned by the tab-stop on
// element-text in rofi/cards.rasinc, so the font need not be monospace. Rows are Pango markup
// (rofi runs with -markup-rows), so names and labels are escaped.
func Rows(ps []Project, open map[string]bool, withTail bool) []Row {
	return rowsAt(ps, open, withTail, time.Now())
}

func rowsAt(ps []Project, open map[string]bool, withTail bool, now time.Time) []Row {
	var out []Row
	for _, p := range ps {
		text := html.EscapeString(p.Name) + "\t" +
			`<span alpha="45%">` + html.EscapeString(p.Label) + `</span>`
		if st := stateText(p, open[tagFor(p.Path)], now); st != "" {
			// third column: what the icon means, in words
			text += "\t" + `<span alpha="60%">` + html.EscapeString(st) + `</span>`
		}
		if p.Description != "" {
			// second line of the same row (rows are separated by \x1e, not
			// \n), so the cursor never lands on it: decoration only
			text += "\n" + `<span size="small" alpha="60%">` + html.EscapeString(p.Description) + `</span>`
		}
		icon := stateIcon(p, open[tagFor(p.Path)])
		if p.Archived {
			icon = "archived"
		}
		out = append(out, Row{Text: text, Path: p.Path, Icon: icon})
	}
	if withTail {
		// Docked rows: switch to the archived list, open the report. Path ""
		// so they never open a project; the Icon names the action.
		out = append(out,
			Row{Text: "archived…", Icon: "archived", Action: "archived"},
			Row{Text: "report…", Icon: "report", Action: "report"})
	}
	return out
}

// stateText spells out the state the icon stands for, with how long it has
// been so. "" for a closed project with nothing pending.
func stateText(p Project, isOpen bool, now time.Time) string {
	dur := ""
	if !p.Since.IsZero() && now.After(p.Since) {
		dur = " · " + fmtDur(int(now.Sub(p.Since)/time.Second))
	}
	switch {
	case isOpen && p.Ball == "you":
		switch p.Reason {
		case "question":
			return "asked you a question" + dur
		case "permission_prompt":
			return "needs your permission" + dur
		case "elicitation_dialog", "elicitation_url_dialog", "agent_needs_input":
			return "asked you for input" + dur
		}
		return "finished, waiting for you" + dur
	case p.Review:
		return "reviewer waiting for your reply"
	case p.Snoozed:
		return "postponed"
	case !isOpen:
		return ""
	case p.Ball == "claude":
		return "working" + dur
	}
	return "terminal open, no Claude"
}

// stateIcon names the row's state icon: "you" needs you, "claude" Claude
// working, "idle" open window without a live Claude session, "" closed. A
// ball state without an open window is a stale session row, so the window
// gates and the state only refines. The same icons appear in the report.
func stateIcon(p Project, isOpen bool) string {
	switch {
	case isOpen && p.Ball == "you":
		return "you"
	case p.Review:
		// a reviewer waits on the user; needs no window to be true
		return "review"
	case p.Snoozed:
		return "snoozed"
	case !isOpen:
		return ""
	case p.Ball == "claude":
		return "claude"
	}
	return "idle"
}
