package main

import (
	"bytes"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"
)

var openMention = EllyMention{Url: widgetsPR.URL() + "#discussion_r7", Author: "adam", At: t0}

// The contract: no Approve token is minted or honoured while the PR's gate
// blocks, a token minted before the mention included.
func TestApproveTokensWaitForTheMentionGate(t *testing.T) {
	l := openTestLedger(t)
	before, err := l.MintToken(widgetsPR, sha)
	if err != nil {
		t.Fatalf("no gate recorded must not block: %v", err)
	}
	for name, g := range map[string]MentionGate{
		"open":       {Open: []EllyMention{openMention}},
		"unreadable": {Unreadable: true},
	} {
		if err := l.SetMentionGate(widgetsPR, g); err != nil {
			t.Fatal(err)
		}
		if _, err := l.MintToken(widgetsPR, sha); !errors.Is(err, errMentionOpen) {
			t.Errorf("%s: minted: %v", name, err)
		}
		if ok, err := l.UseToken(widgetsPR, sha, before); ok || !errors.Is(err, errMentionOpen) {
			t.Errorf("%s: an earlier token was honoured: %v %v", name, ok, err)
		}
	}
	if err := l.SetMentionGate(widgetsPR, MentionGate{}); err != nil {
		t.Fatal(err)
	}
	if ok, err := l.UseToken(widgetsPR, sha, before); !ok || err != nil {
		t.Errorf("cleared gate: the earlier token was refused: %v %v", ok, err)
	}
	if _, err := l.db.Exec(`update mention_gate set open = 'garbage'`); err != nil {
		t.Fatal(err)
	}
	if g, err := l.MentionGate(widgetsPR); err != nil || !g.Blocks() {
		t.Errorf("an unreadable stored list does not block: %+v %v", g, err)
	}
}

func TestMentionGateOfFailsClosed(t *testing.T) {
	no := false
	for name, c := range map[string]struct {
		p    EllyPR
		want bool
	}{
		"older elly":     {ellyPR(func(p *EllyPR) { p.MentionsReadable = nil }), true},
		"threads unread": {ellyPR(func(p *EllyPR) { p.MentionsReadable = &no }), true},
		"open mention":   {ellyPR(func(p *EllyPR) { p.Mentions = []EllyMention{openMention} }), true},
		"readable, none": {ellyPR(nil), false},
	} {
		if got := mentionGateOf(c.p).Blocks(); got != c.want {
			t.Errorf("%s: blocks %v", name, got)
		}
	}
}

// One total choice per state: a state that yields shows the mention, every
// other keeps its row exactly; an unreadable gate changes Approve alone.
func TestMentionRowPrecedence(t *testing.T) {
	yields := map[string]bool{"Idle": true, "AwaitingApprove": true, "HandledPosted": true, "HandledNothing": true, "HandledApproved": true}
	for _, v := range allVariants {
		s := untyped(Typed{V: v, Head: sha, Since: t0})
		name := strings.TrimPrefix(fmt.Sprintf("%T", v), "main.")
		plain := render(widgetsPR, s, View{})
		withMention := render(widgetsPR, s, View{Mentions: MentionGate{Open: []EllyMention{openMention, openMention}}})
		if yields[name] != v.yieldsToMention() {
			t.Errorf("%s: yieldsToMention %v", name, v.yieldsToMention())
		}
		if !v.yieldsToMention() {
			if !sameRow(plain, withMention) {
				t.Errorf("%s: a mention changed a row that keeps its own: %+v", name, withMention)
			}
			continue
		}
		if withMention.State != stateNeedsYou || withMention.Links[0].URL != openMention.Url || !withMention.Links[0].Primary || !strings.Contains(withMention.Note, "adam mentioned you") || !strings.Contains(withMention.Note, "1 of 2") {
			t.Errorf("%s: mention row %+v", name, withMention)
		}
		unreadable := render(widgetsPR, s, View{Mentions: MentionGate{Unreadable: true}})
		_, approve := v.(AwaitingApprove)
		if approve == sameRow(plain, unreadable) {
			t.Errorf("%s: unreadable gate row %+v", name, unreadable)
		}
		if approve && strings.Contains(strings.ToLower(unreadable.Note), "approve waits") == false {
			t.Errorf("unreadable Approve row says nothing: %+v", unreadable)
		}
	}
}

func sameRow(a, b Annotation) bool {
	if a.State != b.State || a.Note != b.Note || len(a.Links) != len(b.Links) {
		return false
	}
	for i := range a.Links {
		if a.Links[i] != b.Links[i] {
			return false
		}
	}
	return true
}

func TestRoundTriggerOnAnUnseenMention(t *testing.T) {
	later := openMention
	later.At = t0.Add(time.Hour)
	mentioned := ellyPR(func(p *EllyPR) { p.Mentions = []EllyMention{openMention, later}; p.HeadRefOid = "h1" })
	if got := roundTrigger(mentioned, "h1", t0.Add(-time.Minute)); got != (Mentioned{Url: later.Url, At: later.At.UTC()}) {
		t.Errorf("newest unseen mention: %#v", got)
	}
	if got := roundTrigger(mentioned, "h1", t0.Add(2*time.Hour)); got != nil {
		t.Errorf("a round after both saw them: %#v", got)
	}
	both := mentioned
	both.Asked = askedAs(askedRequested)
	if _, ok := roundTrigger(both, "h1", time.Time{}).(Requested); !ok {
		t.Error("a request outranks a mention")
	}
}

// End to end: a clean round on a PR with an open mention gets the mention
// row and no live token; once the mention is answered, Approve works.
func TestTickApproveWaitsForAnOpenMention(t *testing.T) {
	w := newTickWorld(t)
	pr := w.accept(t, 42)
	p := requestedPR(42)
	p.Mentions = []EllyMention{{Url: pr.URL() + "#discussion_r7", Author: "adam", At: t0}}
	w.elly.prs = []EllyPR{p}
	if err := w.h.tick(); err != nil {
		t.Fatal(err)
	}
	full := strings.Repeat("a", 40)
	w.msgs[pr.RoundSessionID()] = "Clean. `! claude-review approve " + pr.URL() + " --at " + full + "`"
	if _, err := w.h.ledger.db.Exec(`update rounds set head = ?`, full); err != nil {
		t.Fatal(err)
	}
	w.gh.facts = RoundFacts{State: "open", Head: full}
	w.gh.head = full
	if err := w.h.tick(); err != nil {
		t.Fatal(err)
	}
	a := w.elly.put[pr.URL()]
	if a.State != stateNeedsYou || a.Links[0].Label != "Thread" {
		t.Fatalf("with a mention open elly got %+v", a)
	}
	if _, has, _ := w.h.ledger.TokenFor(pr, full); has {
		t.Error("a token was minted under an open mention")
	}
	w.elly.prs[0].Mentions = nil
	if err := w.h.tick(); err != nil {
		t.Fatal(err)
	}
	if a := w.elly.put[pr.URL()]; len(a.Links) == 0 || a.Links[0].Label != "Approve" {
		t.Fatalf("after the mention cleared elly got %+v", a)
	}
}

func TestThreadsListsMentionsOfThePostingAccount(t *testing.T) {
	g := &fakeGh{outs: []string{
		`[{"id":1,"body":"@me can you check","user":{"login":"ada"}},{"id":2,"body":"@me noted","user":{"login":"me"}}]`,
		`[]`,
		`[{"id":4,"body":"> @me quoted only","path":"a.go","line":7,"user":{"login":"cy"}}]`,
		"me\n",
	}}
	var out, errb bytes.Buffer
	if code := runThreads([]string{prURL}, g.run, &out, &errb); code != 0 {
		t.Fatalf("code %d: %s", code, errb.String())
	}
	got := out.String()
	if !strings.Contains(got, "--- you post as me; these mention you:\n    comment 1 by ada\n") || strings.Contains(got, "\n    comment 2 by me\n") || strings.Contains(got, "\n    inline 4") {
		t.Errorf("got\n%s", got)
	}
}

// The same cases as elly's TestMentionedIn: the two rules are one contract.
func TestMentionedIn(t *testing.T) {
	cases := map[string]bool{
		"@me can you check":           true,
		"hey @Me, thoughts?":          true,
		"cc @me.":                     true,
		"@meow is someone else":       false,
		"mail me@me.com":              false,
		"> @me quoted\nno ask":        false,
		"see `@me` in code":           false,
		"```\n@me\n```\nfenced":       false,
		"quote\n> old @me\n\n@me new": true,
	}
	for body, want := range cases {
		if got := mentionedIn(body, "me"); got != want {
			t.Errorf("mentionedIn(%q) = %v", body, got)
		}
	}
}
