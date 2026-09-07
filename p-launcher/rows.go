package main

const divider = "───"

// Row is one rofi line. Path is "" for the divider, which is a no-op when
// selected.
type Row struct {
	Text string
	Path string
}

// Rows renders projects (already sorted) into rofi lines: a divider between
// namespace groups, "● " for projects with an open window, "  " otherwise so
// names align.
func Rows(ps []Project, open map[string]bool) []Row {
	var out []Row
	prevLabel := ""
	for i, p := range ps {
		if i > 0 && p.Label != prevLabel {
			out = append(out, Row{Text: divider})
		}
		prevLabel = p.Label
		prefix := "  "
		if open[tagFor(p.Path)] {
			prefix = "● "
		}
		out = append(out, Row{Text: prefix + p.Name, Path: p.Path})
	}
	return out
}
