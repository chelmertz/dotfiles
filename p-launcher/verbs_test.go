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
	if got := verbTexts(ongoing); !reflect.DeepEqual(got, []string{"open", "archive", "postpone", "rename", "describe", "add link", "context"}) {
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
	if !strings.HasPrefix(in, "open\x00icon\x1f/i/open.svg\x1e") || !strings.Contains(in, "archive\x00icon\x1f/i/blank.svg\x1e") {
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

func TestClipboardOffer(t *testing.T) {
	owner := func(url string) (string, bool) {
		if url == "https://github.com/o/r/pull/7" || url == "https://github.com/o/r/issues/1" {
			return "m/x", true
		}
		return "", false
	}
	// linked PR or issue → open the owner
	r, ok := clipboardOffer("https://github.com/o/r/pull/7#discussion_r1\n", owner)
	if !ok || r.Action != "clip-open" || r.Arg != "m/x" || r.Icon != "clipboard" || r.Path != "" || !strings.HasPrefix(r.Text, "open m/x\t") {
		t.Fatalf("%+v %v", r, ok)
	}
	// unknown issue → create
	r, ok = clipboardOffer("https://github.com/o/r/issues/12", owner)
	if !ok || r.Action != "clip-create" || r.Arg != "https://github.com/o/r/issues/12" || !strings.HasPrefix(r.Text, "create from o/r#12\t") {
		t.Fatalf("%+v %v", r, ok)
	}
	// unknown PR → nothing; junk → nothing
	if _, ok := clipboardOffer("https://github.com/o/r/pull/99", owner); ok {
		t.Fatal("unknown PR must not offer")
	}
	if _, ok := clipboardOffer("hello", owner); ok {
		t.Fatal("junk must not offer")
	}
	// the offer row is an action row, never a project
	rows := append([]Row{r}, Rows([]Project{{Path: "m/a", Name: "a", Label: "matchi"}}, nil, true)...)
	if tailOf(rows, 0) != "clip-create" || rows[0].Arg == "" {
		t.Fatalf("%+v", rows[0])
	}
	if _, ok := selectRow(rows, 0); ok {
		t.Fatal("offer row must not open as a project")
	}
}

func TestMainMenuExtraHint(t *testing.T) {
	args := mainMenuExtra("")
	plain, mesg := strings.Join(args, "\x00"), args[len(args)-1]
	if strings.Contains(mesg, "Alt+l") || !strings.Contains(plain, "-kb-custom-3\x00Alt+l") {
		t.Fatalf("without a URL the chord stays registered but is not advertised: %q", plain)
	}
	withURL := strings.Join(mainMenuExtra("https://github.com/o/r/pull/7\n"), "\x00")
	if !strings.Contains(withURL, "\nAlt+l links github.com/o/r/pull/7 to the highlighted project") && !strings.Contains(withURL, `">Alt+l links github.com/o/r/pull/7 to`) {
		t.Fatalf("%q", withURL)
	}
	if strings.Contains(withURL, "https://") {
		t.Fatal("protocol must be dropped")
	}
	long := "https://github.com/" + strings.Repeat("a", 80) + "/r/issues/1"
	if got := strings.Join(mainMenuExtra(long), "\x00"); !strings.Contains(got, "…") {
		t.Fatalf("long URL not truncated: %q", got)
	}
	if got := truncate("abcdef", 4); got != "abc…" {
		t.Fatalf("%q", got)
	}
}

func TestRowHeightArgs(t *testing.T) {
	if got := rowHeightArgs([]Project{{Path: "m/a"}, {Path: "m/b"}}); got != nil {
		t.Fatalf("no descriptions must keep single-line rows: %v", got)
	}
	if got := rowHeightArgs([]Project{{Path: "m/a"}, {Path: "m/b", Description: "x"}}); !reflect.DeepEqual(got, []string{"-eh", "2"}) {
		t.Fatalf("%v", got)
	}
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
