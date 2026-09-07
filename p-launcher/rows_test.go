package main

import (
	"reflect"
	"testing"
)

func TestRows(t *testing.T) {
	ps := []Project{
		{Path: "m/reputation", Name: "reputation", Label: "matchi", Ball: "you"},
		{Path: "m/dependabot", Name: "dependabot", Label: "matchi", Ball: "claude"},
		{Path: "m/stale", Name: "stale", Label: "matchi", Ball: "you"}, // no window: stale state, shown closed
		{Path: "personal/health", Name: "health", Label: "personal"},
		{Path: "oss/a&b", Name: "a&b", Label: "oss"},
	}
	open := map[string]bool{"p:m/dependabot": true, "p:m/reputation": true, "p:personal/health": true}
	rows := Rows(ps, open)
	var lines []string
	var paths []string
	for _, r := range rows {
		lines = append(lines, r.Text)
		paths = append(paths, r.Path)
	}
	// Columns are tab-separated; the rofi theme's tab-stops align them.
	muted := func(l string) string { return `<span alpha="45%">` + l + `</span>` }
	wantLines := []string{
		"■\treputation\t" + muted("matchi"),
		"●\tdependabot\t" + muted("matchi"),
		"\tstale\t" + muted("matchi"),
		"○\thealth\t" + muted("personal"),
		"\ta&amp;b\t" + muted("oss"),
	}
	wantPaths := []string{"m/reputation", "m/dependabot", "m/stale", "personal/health", "oss/a&b"}
	if !reflect.DeepEqual(lines, wantLines) {
		t.Fatalf("lines %q", lines)
	}
	if !reflect.DeepEqual(paths, wantPaths) {
		t.Fatalf("paths %q", paths)
	}
}

func TestRowsEmpty(t *testing.T) {
	if got := Rows(nil, nil); len(got) != 0 {
		t.Fatalf("got %+v", got)
	}
}
