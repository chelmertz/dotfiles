package main

import (
	"bytes"
	"strings"
	"testing"
)

func TestWriteList(t *testing.T) {
	ps := []Project{
		{Path: "m/dependabot", Name: "dependabot", Label: "matchi", LastActive: "2026-09-01T10:00:00Z", Ball: "you"},
		{Path: "personal/health", Name: "health", Label: "personal"},
	}
	var buf bytes.Buffer
	writeList(&buf, ps, map[string]bool{"p:m/dependabot": true})
	want := "m/dependabot\tdependabot\tmatchi\t1\t2026-09-01T10:00:00Z\tyou\n" +
		"personal/health\thealth\tpersonal\t0\t\t\n"
	if buf.String() != want {
		t.Fatalf("got %q", buf.String())
	}
}

func TestRofiArgs(t *testing.T) {
	plain := strings.Join(rofiArgs(""), " ")
	if strings.Contains(plain, "-kb-cancel") || !strings.Contains(plain, "-markup-rows") || !strings.Contains(plain, "-show-icons") {
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
		{Text: "ns"},
		{Text: "  health", Path: "personal/health"},
	}
	cases := []struct {
		idx      int
		wantPath string
		wantOk   bool
	}{
		{0, "m/dependabot", true},
		{1, "", false}, // row without a path
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

func TestRofiInput(t *testing.T) {
	rows := []Row{{Text: "a", Path: "m/a", Icon: "you"}, {Text: "b", Path: "m/b"}}
	icons := map[string]string{"you": "/x/you.svg", "blank": "/x/blank.svg"}
	got := string(rofiInput(rows, icons))
	want := "a\x00icon\x1f/x/you.svg\nb\x00icon\x1f/x/blank.svg\n"
	if got != want {
		t.Fatalf("got %q want %q", got, want)
	}
	// no icon files: plain rows, rofi ignores a missing option
	if got := string(rofiInput(rows, nil)); got != "a\nb\n" {
		t.Fatalf("got %q", got)
	}
}
