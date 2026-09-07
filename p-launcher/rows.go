package main

import (
	"html"
)

// Row is one rofi line. Path is "" only for rows that must not open anything
// (none are produced today; selectRow still guards it).
type Row struct {
	Text string
	Path string
}

// Rows renders projects (already sorted) into rofi lines: a glyph column
// (see glyph), the folder name, then the namespace label in a muted span.
// rofi cannot make rows unselectable or skip them while navigating, so
// grouping lives inside each row instead of in heading rows. The three
// columns are tab-separated and aligned by the tab-stops on element-text in
// rofi/cards.rasinc, so the font need not be monospace. Rows are Pango markup
// (rofi runs with -markup-rows), so names and labels are escaped.
func Rows(ps []Project, open map[string]bool) []Row {
	var out []Row
	for _, p := range ps {
		text := glyph(p, open[tagFor(p.Path)]) + "\t" + html.EscapeString(p.Name) + "\t" +
			`<span alpha="45%">` + html.EscapeString(p.Label) + `</span>`
		out = append(out, Row{Text: text, Path: p.Path})
	}
	return out
}

// glyph is the row prefix: whose turn it is for an open project. "■" needs
// you, "●" Claude working, "○" open window without a live Claude session,
// empty for closed. A ball state without an open window is a stale session
// row, so the window gates and the state only refines.
func glyph(p Project, isOpen bool) string {
	switch {
	case !isOpen:
		return ""
	case p.Ball == "you":
		return "■"
	case p.Ball == "claude":
		return "●"
	}
	return "○"
}
