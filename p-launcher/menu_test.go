package main

import (
	"bytes"
	"strings"
	"testing"
)

func TestWriteList(t *testing.T) {
	ps := []Project{
		{Path: "m/dependabot", Name: "dependabot", Label: "matchi", LastActive: "2026-09-01T10:00:00Z"},
		{Path: "personal/health", Name: "health", Label: "personal"},
	}
	var buf bytes.Buffer
	writeList(&buf, ps, map[string]bool{"p:m/dependabot": true})
	want := "m/dependabot\tdependabot\tmatchi\t1\t2026-09-01T10:00:00Z\n" +
		"personal/health\thealth\tpersonal\t0\t\n"
	if buf.String() != want {
		t.Fatalf("got %q", buf.String())
	}
}

func TestRofiArgs(t *testing.T) {
	plain := strings.Join(rofiArgs(""), " ")
	if strings.Contains(plain, "-kb-cancel") || !strings.Contains(plain, "-markup-rows") {
		t.Fatalf("got %q", plain)
	}
	withKey := strings.Join(rofiArgs("F5"), " ")
	if !strings.Contains(withKey, "-kb-cancel Escape,Control+g,Control+bracketleft,F5") {
		t.Fatalf("got %q", withKey)
	}
}

func TestParseRofiIndex(t *testing.T) {
	cases := map[string]struct {
		idx int
		ok  bool
	}{"2\n": {2, true}, "0": {0, true}, "-1\n": {-1, true}, "": {0, false}, "abc": {0, false}}
	for in, c := range cases {
		idx, ok := parseRofiIndex(in)
		if idx != c.idx || ok != c.ok {
			t.Errorf("%q: got (%d,%v)", in, idx, ok)
		}
	}
}

func TestSelectRow(t *testing.T) {
	rows := []Row{
		{Text: "  dependabot", Path: "m/dependabot"},
		{Text: "<b>ns</b>" + heading},
		{Text: "  health", Path: "personal/health"},
	}
	cases := []struct {
		idx      int
		wantPath string
		wantOk   bool
	}{
		{0, "m/dependabot", true},
		{1, "", false}, // heading
		{2, "personal/health", true},
		{-1, "", false},
		{3, "", false},
	}
	for _, c := range cases {
		path, ok := selectRow(rows, c.idx)
		if path != c.wantPath || ok != c.wantOk {
			t.Errorf("idx %d: got (%q,%v) want (%q,%v)", c.idx, path, ok, c.wantPath, c.wantOk)
		}
	}
}
