package main

import (
	"encoding/json"
	"flag"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/chelmertz/dotfiles/claude-review/ids"
)

var updateRows = flag.Bool("update-rows", false, "rewrite testdata/rows.json from render")

// rowRules is, per annotation state, the actions a row may make primary,
// the actions it may offer at all, and whether its rows hand the PR over.
type rowRules map[string]struct {
	Primary  []string `json:"primary"`
	Offered  []string `json:"offered"`
	Handover bool     `json:"handover"`
}

// rowsFixture is what render writes, over every state, every hold and a
// pending dismissal, with the Start offer: the rules elly checks each PUT
// against. elly commits the same file (testdata/claude-review-rows.json)
// and its own test reads it, so the two cannot hold different rules.
func rowsFixture() rowRules {
	pr := PR{Owner: "o", Repo: "r", Number: 1}
	rows := []Annotation{startOffer(pr)}
	// An Approve row offers its link only with a token (wantToken).
	tok, err := ids.ParseToken(strings.Repeat("0", 48))
	if err != nil {
		panic(err)
	}
	for _, v := range allVariants {
		s := untyped(Typed{V: v, Head: "0123456789abcdef0123456789abcdef01234567"})
		for _, view := range []View{{}, {Hold: holdFor(true, "")}, {Hold: holdFor(false, breakerDuplicate)}, {Dismissing: true}, {Token: tok}} {
			rows = append(rows, render(pr, s, view))
		}
	}
	out := rowRules{}
	for _, a := range rows {
		if a.State == "" {
			continue
		}
		r := out[a.State]
		r.Handover = r.Handover || a.Handover
		for _, l := range a.Links {
			if !slices.Contains(r.Offered, l.Action) {
				r.Offered = append(r.Offered, l.Action)
			}
			if l.Primary && !slices.Contains(r.Primary, l.Action) {
				r.Primary = append(r.Primary, l.Action)
			}
		}
		if r.Primary == nil {
			r.Primary = []string{}
		}
		slices.Sort(r.Offered)
		slices.Sort(r.Primary)
		out[a.State] = r
	}
	return out
}

func TestRowsFixtureMatchesRender(t *testing.T) {
	path := filepath.Join("testdata", "rows.json")
	got, err := json.MarshalIndent(rowsFixture(), "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	got = append(got, '\n')
	if *updateRows {
		if err := os.WriteFile(path, got, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("%v; run go test -run TestRowsFixture -update-rows, then copy it to elly's internal/server/testdata/claude-review-rows.json", err)
	}
	if string(got) != string(want) {
		t.Errorf("render and testdata/rows.json disagree; rerun with -update-rows and copy the file to elly:\n%s", got)
	}
}
