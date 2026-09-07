package main

import (
	"reflect"
	"strings"
	"testing"
)

func TestParseRofiOut(t *testing.T) {
	cases := []struct {
		in    string
		idx   int
		typed string
		ok    bool
	}{
		{"3|\n", 3, "", true},
		{"-1|m/new", -1, "m/new", true},
		{"0|a|b", 0, "a|b", true}, // only the first separator splits
		{"x", 0, "", false},
		{"", 0, "", false},
	}
	for _, c := range cases {
		idx, typed, ok := parseRofiOut(c.in)
		if idx != c.idx || typed != c.typed || ok != c.ok {
			t.Errorf("%q: got (%d,%q,%v) want (%d,%q,%v)", c.in, idx, typed, ok, c.idx, c.typed, c.ok)
		}
	}
}

func TestRowsArchivedTail(t *testing.T) {
	ps := []Project{{Path: "m/a", Name: "a", Label: "matchi"}}
	rows := Rows(ps, nil, true)
	if len(rows) != 3 || rows[1].Text != "archived…" || rows[1].Action != "archived" || rows[1].Path != "" || rows[2].Text != "report…" || rows[2].Action != "report" || rows[2].Path != "" {
		t.Fatalf("%+v", rows)
	}
	if rows[0].Action != "" {
		t.Fatalf("project row carries an action: %+v", rows[0])
	}
	// an archived project shares the icon with the switch row but is not one
	arch := Rows([]Project{{Path: "m/z", Name: "z", Archived: true}}, nil, false)
	if arch[0].Icon != "archived" || tailOf(arch, 0) != "" {
		t.Fatalf("%+v", arch[0])
	}
	for _, i := range []int{1, 2} {
		if _, ok := selectRow(rows, i); ok {
			t.Fatal("tail row must not open anything")
		}
	}
	if tailOf(rows, 1) != "archived" || tailOf(rows, 2) != "report" || tailOf(rows, 0) != "" || tailOf(rows, 5) != "" || tailOf(rows, -1) != "" {
		t.Fatal("tail detection")
	}
	if !isArchivedTail(rows, 1) || isArchivedTail(rows, 2) {
		t.Fatal("archived tail detection")
	}
	if got := Rows(ps, nil, false); len(got) != 1 {
		t.Fatalf("%+v", got)
	}
}

func TestVerbRows(t *testing.T) {
	ongoing := verbRows(Project{Path: "m/a", Name: "a"})
	if got := verbTexts(postponeRows()); !reflect.DeepEqual(got, []string{"1 day", "3 days", "10 days"}) || postponeRows()[2].arg != "10" {
		t.Fatalf("%v", got)
	}
	if got := verbTexts(ongoing); !reflect.DeepEqual(got, []string{"open", "archive", "postpone", "rename", "add link", "context"}) {
		t.Fatalf("%v", got)
	}
	archived := verbRows(Project{Path: "m/a", Name: "a", Archived: true})
	if got := verbTexts(archived); !reflect.DeepEqual(got, []string{"open (reopen)", "context"}) {
		t.Fatalf("%v", got)
	}
	if archived[0].verb != verbOpen || archived[1].verb != verbContext {
		t.Fatalf("%+v", archived)
	}
	if got := verbTexts(createRows("m/new")); !reflect.DeepEqual(got, []string{"create m/new"}) {
		t.Fatalf("%v", got)
	}
	if got := verbTexts(reasonRows()); !reflect.DeepEqual(got, []string{"done", "scrapped", "deprioritized", "solved elsewhere"}) {
		t.Fatalf("%v", got)
	}
	if reasonRows()[3].arg != "elsewhere" {
		t.Fatalf("%+v", reasonRows()[3])
	}
	// every verb row has an icon that exists in the set
	for _, vs := range [][]verbRow{ongoing, archived, createRows("m/x"), reasonRows(), postponeRows()} {
		for _, v := range vs {
			if _, ok := iconPaths[v.icon]; !ok {
				t.Errorf("verb %q has no icon %q", v.text, v.icon)
			}
			if _, ok := rofiIconColors[v.icon]; !ok {
				t.Errorf("verb %q icon %q has no rofi color", v.text, v.icon)
			}
		}
	}
	in := string(verbInput(ongoing, map[string]string{"open": "/i/open.svg", "blank": "/i/blank.svg"}))
	if !strings.HasPrefix(in, "open\x00icon\x1f/i/open.svg\n") || !strings.Contains(in, "archive\x00icon\x1f/i/blank.svg\n") {
		t.Fatalf("%q", in)
	}
}

func verbTexts(vs []verbRow) []string {
	var out []string
	for _, v := range vs {
		out = append(out, v.text)
	}
	return out
}

func TestCreatePathFromTyped(t *testing.T) {
	cases := map[string]string{
		"m/new":        "m/new",
		" m/new ":      "m/new",
		"new":          "", // no namespace
		"m/":           "",
		"m/../x":       "",
		"m/new/deeper": "",
		"":             "",
		"personal/x y": "", // spaces
	}
	for in, want := range cases {
		if got := createPathFromTyped(in); got != want {
			t.Errorf("%q: got %q want %q", in, got, want)
		}
	}
}
