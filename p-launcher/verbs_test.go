package main

import (
	"reflect"
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
	if len(rows) != 2 || rows[1].Text != "archived…" || rows[1].Icon != "archived" || rows[1].Path != "" {
		t.Fatalf("%+v", rows)
	}
	if _, ok := selectRow(rows, 1); ok {
		t.Fatal("tail row must not open anything")
	}
	if !isArchivedTail(rows, 1) || isArchivedTail(rows, 0) || isArchivedTail(rows, 5) {
		t.Fatal("tail detection")
	}
	if got := Rows(ps, nil, false); len(got) != 1 {
		t.Fatalf("%+v", got)
	}
}

func TestVerbRows(t *testing.T) {
	ongoing := verbRows(Project{Path: "m/a", Name: "a"})
	if got := verbTexts(ongoing); !reflect.DeepEqual(got, []string{"open", "archive", "add link", "context"}) {
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
