package main

import (
	"html"
)

// Row is one rofi line. Path is "" only for rows that must not open anything
// (none are produced today; selectRow still guards it).
type Row struct {
	Text string
	Path string
	Icon string // state icon name (see iconPaths), "" for a closed project
}

// Rows renders projects (already sorted) into rofi lines: the folder name,
// then the namespace label in a muted span; the state is a row icon (see
// stateIcon), not text. rofi cannot make rows unselectable or skip them while
// navigating, so grouping lives inside each row instead of in heading rows.
// The two columns are tab-separated and aligned by the tab-stop on
// element-text in rofi/cards.rasinc, so the font need not be monospace. Rows are Pango markup
// (rofi runs with -markup-rows), so names and labels are escaped.
func Rows(ps []Project, open map[string]bool, withTail bool) []Row {
	var out []Row
	for _, p := range ps {
		text := html.EscapeString(p.Name) + "\t" +
			`<span alpha="45%">` + html.EscapeString(p.Label) + `</span>`
		icon := stateIcon(p, open[tagFor(p.Path)])
		if p.Archived {
			icon = "archived"
		}
		out = append(out, Row{Text: text, Path: p.Path, Icon: icon})
	}
	if withTail {
		// Switches the menu to the archived list; Path "" so it never opens.
		out = append(out, Row{Text: "archived…", Icon: "archived"})
	}
	return out
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
	case !isOpen:
		return ""
	case p.Ball == "claude":
		return "claude"
	}
	return "idle"
}
