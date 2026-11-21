package main

import (
	"net/url"
	"strings"
	"testing"
)

// elly's types.ActionVerbs names the same pairs; a pair either side moves
// alone makes elly refuse the row.
func TestActionVerbLiterals(t *testing.T) {
	want := map[string]string{"approve": "approve", "open": "attach", "start": "", "help": "states"}
	if len(actionVerbs) != len(want) {
		t.Fatalf("actions %v, want %v", actionVerbs, want)
	}
	for a, v := range want {
		if actionVerbs[a] != v {
			t.Errorf("action %q is verb %q, want %q; move elly's with it", a, actionVerbs[a], v)
		}
	}
}

// Every link a row carries names what it does, and the action's verb is
// the link's own, so elly, which reads the action and never the label,
// takes every row.
func TestEveryRowLinkNamesItsAction(t *testing.T) {
	pr := PR{Owner: "o", Repo: "r", Number: 1}
	rows := []Annotation{startOffer(pr)}
	for _, v := range allVariants {
		s := untyped(Typed{V: v, Head: "0123456789abcdef0123456789abcdef01234567"})
		rows = append(rows, render(pr, s, View{}), render(pr, s, View{Hold: holdFor(true, "")}),
			render(pr, s, View{Mentions: MentionGate{Open: []EllyMention{{Url: pr.URL() + "#discussion_r1", Author: "a"}}}}),
			render(pr, s, View{Mentions: MentionGate{Unreadable: true}}))
	}
	for _, a := range rows {
		for _, l := range a.Links {
			// A plain GitHub link to the PR (the mention's thread, the PR
			// page) does nothing claude-review does; elly takes it without
			// an action and checks only that it names the PR.
			if l.Action == "" && strings.HasPrefix(l.URL, pr.URL()) {
				continue
			}
			verb, known := actionVerbs[l.Action]
			u, err := url.Parse(l.URL)
			if err != nil || !known {
				t.Errorf("%s row: link %q has action %q", a.State, l.Label, l.Action)
				continue
			}
			segments := strings.Split(strings.TrimPrefix(u.Path, "/"), "/")
			got := ""
			if len(segments) == 3 {
				got = segments[2]
			}
			if got != verb {
				t.Errorf("%s row: link %q is verb %q under action %q", a.State, l.Label, got, l.Action)
			}
		}
	}
}
