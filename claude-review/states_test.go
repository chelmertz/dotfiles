package main

import (
	"flag"
	"html"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The page shows every state, every move the rules make, each state's row
// text as render writes it, and the PR's own state marked: it is drawn from
// the code, so it cannot drift from it.
func TestStatesPageIsTheRules(t *testing.T) {
	pr := PR{Owner: "o", Repo: "r", Number: 1}
	cur := PRState{State: stAwaiting, Kind: kindQuestion}
	page, err := statesPage(&pr, &cur)
	if err != nil {
		t.Fatal(err)
	}
	for _, k := range allStates {
		if !strings.Contains(page, "<code>"+html.EscapeString(k.name())+"</code>") {
			t.Errorf("no row for %s", k.name())
		}
		a := render(pr, PRState{State: k.State, Kind: k.Kind, Detail: "…", Head: "0123456789abcdef"}, View{})
		if a.Note != "" && !strings.Contains(page, html.EscapeString(a.Note)) {
			t.Errorf("%s: render's note %q is not on the page", k.name(), a.Note)
		}
		for _, e := range edgesFrom(rules, k) {
			want := html.EscapeString(e.r.label()) + " → " + html.EscapeString(e.r.targetName())
			if e.r.broad() {
				want = html.EscapeString(e.r.label()) + ", from "
			}
			if !strings.Contains(page, want) {
				t.Errorf("%s: move %q is not on the page", k.name(), want)
			}
		}
	}
	if n := strings.Count(page, `class="cur"`); n != 2 {
		t.Errorf("%d marks of the current state, want its box and its row", n)
	}
	if !strings.Contains(page, html.EscapeString(pr.URL())) {
		t.Error("the page does not name the PR")
	}
}

// A rule after an unguarded one for the same event never fires from that
// state, so the page does not show it as a move.
func TestStatesPageLeavesOutShadowedRules(t *testing.T) {
	rs := []rule{
		{from: in(stIdle), event: evOwed, when: rerequested, to: to(stHeld, kindFresh)},
		{from: in(stIdle), event: evOwed, to: to(stRunning, kindTick)},
		{from: in(stIdle, stHandled), event: evOwed, to: to(stStuck, kindRefused)},
	}
	if got := edgesFrom(rs, stateKey{stIdle, ""}); len(got) != 2 || got[1].r.to.to.State != stRunning {
		t.Errorf("idle: %d moves, want the guarded one and the first unguarded one", len(got))
	}
	if got := edgesFrom(rs, stateKey{stHandled, kindPosted}); len(got) != 1 || got[0].r.to.to != (stateKey{stStuck, kindRefused}) {
		t.Errorf("handled: %+v", got)
	}
}

// Every row tick writes carries the `?` link, and the link parses as the
// states verb.
func TestEveryRowLinksTheStatesPage(t *testing.T) {
	pr := PR{Owner: "o", Repo: "r", Number: 1}
	for _, k := range allStates {
		a := render(pr, PRState{State: k.State, Kind: k.Kind}, View{})
		if a.State == "" {
			continue
		}
		if len(a.Links) == 0 || a.Links[len(a.Links)-1] != statesLink(pr) {
			t.Errorf("%s: no ? link: %+v", k.name(), a.Links)
		}
	}
	got, verb, _, ok := ParseVerb("claude-review://o/r/1/states")
	if !ok || verb != "states" || got != pr {
		t.Errorf("states link parses as %v %q %v", got, verb, ok)
	}
}

// `claude-review states` writes the page into a state dir that may not
// exist yet and prints where it is; the `?` link marks the PR's state from
// the ledger and opens the page.
func TestStatesWritesAndOpensThePage(t *testing.T) {
	h := newHost(t, "", true, false)
	h.d.StateDir = filepath.Join(t.TempDir(), "fresh")
	if code := run([]string{"states"}, h.d); code != 0 {
		t.Fatalf("exit %d: %s", code, h.stderr)
	}
	path := strings.TrimSpace(h.stdout.String())
	if b, err := os.ReadFile(path); err != nil || !strings.Contains(string(b), "claude-review states") {
		t.Fatalf("page %q: %v", path, err)
	}
	if len(h.spawns) != 0 {
		t.Errorf("the command opened the page: %v", h.spawns)
	}
	pr := PR{Owner: "o", Repo: "r", Number: 1}
	l, err := OpenLedger(LedgerPath(h.d.StateDir))
	if err != nil {
		t.Fatal(err)
	}
	if err := l.SetPRState(pr, PRState{State: stStuck, Kind: kindFailed}); err != nil {
		t.Fatal(err)
	}
	_ = l.Close()
	h.stdout.Reset()
	if code := runVerb(h.d, Config{}, pr, "states", nil); code != 0 {
		t.Fatalf("exit %d: %s", code, h.stderr)
	}
	if b, _ := os.ReadFile(path); !strings.Contains(string(b), "<b>stuck{failed_twice}</b>") {
		t.Error("the page does not mark the PR's state")
	}
	if len(h.spawns) != 1 || h.spawns[0][0] != "xdg-open" || h.spawns[0][1] != path {
		t.Errorf("spawns %v", h.spawns)
	}
}

// The diagram's hand-set columns hold every state the code has, once.
func TestStatesColumnsHoldEveryState(t *testing.T) {
	seen := map[string]int{}
	for _, col := range columns {
		for _, n := range col {
			seen[n]++
		}
	}
	for _, k := range allStates {
		if seen[string(k.State)] != 1 {
			t.Errorf("%s is in %d columns, want 1", k.State, seen[string(k.State)])
		}
	}
}

var updateStates = flag.Bool("update-states", false, "rewrite testdata/states.html from statesPage")

// The page as drawn, boxes and arrows to the pixel, is testdata/states.html:
// a layout change shows up as a diff to look at. After a rule or layout
// change: go test -run StatesPageMatchesItsFixture . -args -update-states
func TestStatesPageMatchesItsFixture(t *testing.T) {
	pr := PR{Owner: "o", Repo: "r", Number: 1}
	cur := PRState{State: stAwaiting, Kind: kindQuestion}
	page, err := statesPage(&pr, &cur)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join("testdata", "states.html")
	if *updateStates {
		if err := os.WriteFile(path, []byte(page), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(path) //nolint:gosec // a fixture in the package
	if err != nil {
		t.Fatal(err)
	}
	if page != string(want) {
		t.Error("the states page differs from testdata/states.html; if the change is meant, rerun with -args -update-states and read the diff")
	}
}
