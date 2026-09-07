package main

import (
	"reflect"
	"testing"
)

func TestRows(t *testing.T) {
	ps := []Project{
		{Path: "m/reputation", Name: "reputation", Label: "matchi"},
		{Path: "m/dependabot", Name: "dependabot", Label: "matchi"},
		{Path: "personal/health", Name: "health", Label: "personal"},
		{Path: "oss/thing", Name: "thing", Label: "oss"},
	}
	open := map[string]bool{"p:m/dependabot": true}
	rows := Rows(ps, open)
	var lines []string
	var paths []string
	for _, r := range rows {
		lines = append(lines, r.Text)
		paths = append(paths, r.Path)
	}
	wantLines := []string{"  reputation", "● dependabot", divider, "  health", divider, "  thing"}
	wantPaths := []string{"m/reputation", "m/dependabot", "", "personal/health", "", "oss/thing"}
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
