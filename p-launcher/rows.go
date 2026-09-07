package main

import "html"

// rowOpts is rofi's per-row option syntax: "\0key\x1fvalue" appended to the
// row text. nonselectable rows render but Enter does nothing on them.
const heading = "\x00nonselectable\x1ftrue"

// Row is one rofi line. Path is "" for a namespace heading, which is a no-op
// when selected.
type Row struct {
	Text string
	Path string
}

// Rows renders projects (already sorted) into rofi lines: a bold, non-
// selectable heading per namespace group, then "● " for projects with an open
// window, "  " otherwise so names align. Rows are Pango markup (rofi runs with
// -markup-rows), so names are escaped. Groups are runs of equal Label;
// ListProjects' ORDER BY sort_order guarantees rows sharing a Label are
// always contiguous.
func Rows(ps []Project, open map[string]bool) []Row {
	var out []Row
	prevLabel := ""
	for i, p := range ps {
		if i == 0 || p.Label != prevLabel {
			out = append(out, Row{Text: "<b>" + html.EscapeString(p.Label) + "</b>" + heading})
		}
		prevLabel = p.Label
		prefix := "  "
		if open[tagFor(p.Path)] {
			prefix = "● "
		}
		out = append(out, Row{Text: prefix + html.EscapeString(p.Name), Path: p.Path})
	}
	return out
}
