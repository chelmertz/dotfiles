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
	rows := Rows(ps, open, false)
	var lines, paths, icons []string
	for _, r := range rows {
		lines = append(lines, r.Text)
		paths = append(paths, r.Path)
		icons = append(icons, r.Icon)
	}
	// Columns are tab-separated; the rofi theme's tab-stops align them.
	muted := func(l string) string { return `<span alpha="45%">` + l + `</span>` }
	wantLines := []string{
		"reputation\t" + muted("matchi"),
		"dependabot\t" + muted("matchi"),
		"stale\t" + muted("matchi"),
		"health\t" + muted("personal"),
		"a&amp;b\t" + muted("oss"),
	}
	wantIcons := []string{"you", "claude", "", "idle", ""}
	wantPaths := []string{"m/reputation", "m/dependabot", "m/stale", "personal/health", "oss/a&b"}
	if !reflect.DeepEqual(lines, wantLines) {
		t.Fatalf("lines %q", lines)
	}
	if !reflect.DeepEqual(paths, wantPaths) {
		t.Fatalf("paths %q", paths)
	}
	if !reflect.DeepEqual(icons, wantIcons) {
		t.Fatalf("icons %q", icons)
	}
}

func TestRowsEmpty(t *testing.T) {
	if got := Rows(nil, nil, false); len(got) != 0 {
		t.Fatalf("got %+v", got)
	}
}
