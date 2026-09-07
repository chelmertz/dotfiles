package main

import (
	"html"
	"strings"
	"unicode/utf8"
)

// Row is one rofi line. Path is "" only for rows that must not open anything
// (none are produced today; selectRow still guards it).
type Row struct {
	Text string
	Path string
}

// Rows renders projects (already sorted) into rofi lines: a glyph column
// (see glyph), the folder name, then the namespace
// label right-aligned in a muted span. rofi cannot make rows unselectable or
// skip them while navigating, so grouping lives inside each row instead of
// in heading rows. Alignment relies on rofi's monospace font. Rows are Pango
// markup (rofi runs with -markup-rows), so names and labels are escaped.
func Rows(ps []Project, open map[string]bool) []Row {
	width := 0
	for _, p := range ps {
		if n := utf8.RuneCountInString(p.Name); n > width {
			width = n
		}
	}
	var out []Row
	for _, p := range ps {
		prefix := glyph(p, open[tagFor(p.Path)])
		pad := strings.Repeat(" ", width-utf8.RuneCountInString(p.Name)+4)
		text := prefix + html.EscapeString(p.Name) + pad +
			`<span alpha="45%">` + html.EscapeString(p.Label) + `</span>`
		out = append(out, Row{Text: text, Path: p.Path})
	}
	return out
}

// glyph is the 2-char row prefix: whose turn it is for an open project.
// "■ " needs you, "● " Claude working, "○ " open window without a live
// Claude session, "  " closed. A ball state without an open window is a
// stale session row, so the window gates and the state only refines. All
// three symbols are East Asian Width "A", one cell in a mono font; ✋ is
// wide and broke the label column.
func glyph(p Project, isOpen bool) string {
	switch {
	case !isOpen:
		return "  "
	case p.Ball == "you":
		return "■ "
	case p.Ball == "claude":
		return "● "
	}
	return "○ "
}
