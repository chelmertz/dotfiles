package main

import (
	"bytes"
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
