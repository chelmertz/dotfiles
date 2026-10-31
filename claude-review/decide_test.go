package main

import (
	"strings"
	"testing"
	"time"
)

func TestRepoAllowed(t *testing.T) {
	allow := []string{"acme/*", "globex/platform", "globex/kb"}
	cases := map[string]bool{
		"acme/widgets": true, "ACME/Widgets": true, "globex/platform": true,
		"globex/kb": true, "globex/monorepo": false, "initech/acme": false,
	}
	for repo, want := range cases {
		o, r, _ := strings.Cut(repo, "/")
		if got := repoAllowed(o, r, allow); got != want {
			t.Errorf("%s: got %v", repo, got)
		}
	}
	if repoAllowed("acme", "widgets", nil) {
		t.Error("an empty allowlist allows a repo")
	}
}

func ellyPR(mod func(*EllyPR)) EllyPR {
	p := EllyPR{Url: "https://github.com/acme/widgets/pull/42", RepoOwner: "acme", RepoName: "widgets",
		Author: "alice", HeadRefOid: "h1", LastUpdated: t0}
	if mod != nil {
		mod(&p)
	}
	return p
}

func TestRoundTrigger(t *testing.T) {
	cases := []struct {
		name     string
		pr       EllyPR
		lastHead string
		want     string
	}{
		{"nothing owed", ellyPR(nil), "h1", ""},
		{"review requested", ellyPR(func(p *EllyPR) { p.ReviewRequestedFromUsers = []string{"me"} }), "h1", "review_requested@" + t0.Format(time.RFC3339)},
		{"someone else's request", ellyPR(func(p *EllyPR) { p.ReviewRequestedFromUsers = []string{"bob"} }), "h1", ""},
		{"re-review", ellyPR(func(p *EllyPR) { p.RereviewFrom = []string{"me"} }), "h1", "rereview_requested@" + t0.Format(time.RFC3339)},
		{"replies", ellyPR(func(p *EllyPR) { p.ThreadsActionable = 2 }), "h1", "replies@" + t0.Format(time.RFC3339)},
		{"new head", ellyPR(func(p *EllyPR) { p.HeadRefOid = "h2" }), "h1", "new_head"},
		{"no head known", ellyPR(func(p *EllyPR) { p.HeadRefOid = "" }), "h1", ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := roundTrigger(c.pr, "me", c.lastHead); got != c.want {
				t.Errorf("got %q, want %q", got, c.want)
			}
		})
	}
}

type fakeGate struct {
	descends bool
	base     string
}

func (f fakeGate) Descends(PR, string, string) (bool, error) { return f.descends, nil }
func (f fakeGate) Base(PR) (string, error)                   { return f.base, nil }

func TestPlanTick(t *testing.T) {
	cfg := TickConfig{ReviewRepos: []string{"acme/*"}, User: "me", DailyCap: 3}
	requested := func(p *EllyPR) { p.ReviewRequestedFromUsers = []string{"me"} }
	plan := func(l *Ledger, g fakeGate, prs ...EllyPR) []Planned {
		t.Helper()
		got, err := planTick(prs, cfg, l, g, t0)
		if err != nil {
			t.Fatal(err)
		}
		return got
	}
	ok := fakeGate{descends: true, base: "main"}

	l := openTestLedger(t)
	if got := plan(l, ok, ellyPR(requested)); len(got) != 0 {
		t.Errorf("a PR the user never clicked got %+v", got)
	}

	if err := l.Accept(widgetsPR, "h0", "main", t0); err != nil {
		t.Fatal(err)
	}
	got := plan(l, ok, ellyPR(requested))
	if len(got) != 1 || got[0].Gate != "" || got[0].Head != "h1" || !strings.HasPrefix(got[0].Trigger, "review_requested@") {
		t.Fatalf("accepted, requested: %+v", got)
	}

	if got := plan(l, ok, ellyPR(func(p *EllyPR) { requested(p); p.Author = "me" })); len(got) != 0 {
		t.Errorf("the user's own PR got %+v", got)
	}
	if got := plan(l, ok, ellyPR(func(p *EllyPR) { requested(p); p.RepoOwner = "globex" })); len(got) != 0 {
		t.Errorf("a repo outside review_repos got %+v", got)
	}
	if got := plan(l, ok, ellyPR(func(p *EllyPR) { requested(p); p.Buried = true })); len(got) != 1 {
		t.Errorf("bury is elly's display preference and must not stop rounds: %+v", got)
	}

	if got := plan(l, fakeGate{descends: false, base: "main"}, ellyPR(requested)); len(got) != 1 || got[0].Gate != gateFreshLook {
		t.Errorf("force-push off the accepted head: %+v", got)
	}
	if got := plan(l, fakeGate{descends: true, base: "release"}, ellyPR(requested)); len(got) != 1 || got[0].Gate != gateFreshLook {
		t.Errorf("base changed since acceptance: %+v", got)
	}

	trig := plan(l, ok, ellyPR(requested))[0].Trigger
	if _, err := l.StartRound(widgetsPR, "h1", trig, t0); err != nil {
		t.Fatal(err)
	}
	if got := plan(l, ok, ellyPR(requested)); len(got) != 0 {
		t.Errorf("same head and trigger ran twice: %+v", got)
	}
	if got := plan(l, ok, ellyPR(func(p *EllyPR) { p.HeadRefOid = "h2" })); len(got) != 1 || got[0].Trigger != "new_head" {
		t.Errorf("a push after the last round: %+v", got)
	}

	for range 2 {
		if _, err := l.StartRound(widgetsPR, "hx", "x", t0); err != nil {
			t.Fatal(err)
		}
	}
	if got := plan(l, ok, ellyPR(func(p *EllyPR) { p.HeadRefOid = "h3" })); len(got) != 1 || got[0].Gate != gatePaused {
		t.Errorf("past the daily cap: %+v", got)
	}

	if err := l.Ignore(widgetsPR); err != nil {
		t.Fatal(err)
	}
	if got := plan(l, ok, ellyPR(func(p *EllyPR) { p.HeadRefOid = "h4" })); len(got) != 0 {
		t.Errorf("an ignored PR got %+v", got)
	}
}

func TestDecide(t *testing.T) {
	const head = "0123456789abcdef0123456789abcdef01234567"
	round := Round{ID: 1, PR: widgetsPR.URL(), Head: head, StartedAt: t0}
	approveLine := Outcome{Kind: OutcomeApprove, PrURL: widgetsPR.URL(), SHA: head}
	open := RoundFacts{State: "open", Head: head}
	approved := func(commit string) *Review {
		return &Review{ID: 9, State: "APPROVED", CommitID: commit}
	}
	roundReview := "https://github.com/acme/widgets/pull/42#pullrequestreview-5"

	cases := []struct {
		name    string
		r       Round
		o       Outcome
		f       RoundFacts
		check   func(Decision) bool
		explain string
	}{
		{"closed PR clears", round, approveLine, RoundFacts{State: "closed", Head: head},
			func(d Decision) bool { return d.Clear }, "clear"},
		{"clean round, no approval: Approve link", round, approveLine, open,
			func(d Decision) bool { return d.Annotation.State == stateNeedsYou && d.MintFor == head }, "needs_you + token"},
		{"clean round under a standing approval", round, approveLine, RoundFacts{State: "open", Head: head, UserReview: approved("old")},
			func(d Decision) bool { return d.Annotation.State == stateHandled && d.MintFor == "" }, "handled"},
		{"approve line for another sha", round, Outcome{Kind: OutcomeApprove, PrURL: widgetsPR.URL(), SHA: strings.Repeat("f", 40)}, RoundFacts{State: "open", Head: head, RoundReviewURL: roundReview},
			func(d Decision) bool { return d.MintFor == "" && d.Annotation.State == stateHandled }, "no Approve link"},
		{"approve line for another PR", round, Outcome{Kind: OutcomeApprove, PrURL: "https://github.com/acme/widgets/pull/43", SHA: head}, open,
			func(d Decision) bool { return d.MintFor == "" }, "no Approve link"},
		{"approve line but the round posted findings", round, approveLine, RoundFacts{State: "open", Head: head, NewComments: 2, RoundReviewURL: roundReview},
			func(d Decision) bool { return d.MintFor == "" }, "no Approve link"},
		{"approve line but the user's threads are open", round, approveLine, RoundFacts{State: "open", Head: head, UnresolvedUserThreads: 1},
			func(d Decision) bool { return d.MintFor == "" }, "no Approve link"},
		{"approve line but the head moved", round, approveLine, RoundFacts{State: "open", Head: "moved"},
			func(d Decision) bool { return d.MintFor == "" }, "no Approve link"},
		{"findings after the user's approval dismiss it", round, Outcome{}, RoundFacts{State: "open", Head: head, NewComments: 1, UserReview: approved("old"), RoundReviewURL: roundReview},
			func(d Decision) bool { return d.DismissReviewID == 9 && d.Annotation.State == stateNeedsYou }, "dismiss + needs_you"},
		{"findings with the approval at the current head do not dismiss", round, Outcome{}, RoundFacts{State: "open", Head: head, NewComments: 1, UserReview: approved(head), RoundReviewURL: roundReview},
			func(d Decision) bool { return d.DismissReviewID == 0 }, "no dismissal"},
		{"question", round, Outcome{Kind: OutcomeQuestion}, open,
			func(d Decision) bool { return d.Annotation.State == stateNeedsYou && hasLink(d.Annotation, "attach") }, "needs_you + attach"},
		{"findings posted: handled", round, Outcome{}, RoundFacts{State: "open", Head: head, NewComments: 3, RoundReviewURL: roundReview},
			func(d Decision) bool {
				return d.Annotation.State == stateHandled && hasLink(d.Annotation, "pullrequestreview-5")
			}, "handled + summary link"},
		{"nothing to show for it: retry once", round, Outcome{}, open,
			func(d Decision) bool { return d.Retry && d.Annotation == nil }, "retry"},
		{"nothing again: round failed", Round{ID: 1, PR: round.PR, Head: head, Retries: 1}, Outcome{}, open,
			func(d Decision) bool {
				return !d.Retry && d.Annotation.State == stateNeedsYou && strings.Contains(d.Annotation.Note, "failed")
			}, "needs_you failed"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			d := decide(c.r, c.o, c.f, widgetsPR)
			if !c.check(d) {
				t.Errorf("got %+v (annotation %+v), want %s", d, d.Annotation, c.explain)
			}
		})
	}
}

func hasLink(a *Annotation, part string) bool {
	if a == nil {
		return false
	}
	for _, l := range a.Links {
		if strings.Contains(l.URL, part) {
			return true
		}
	}
	return false
}
