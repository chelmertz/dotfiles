package main

import (
	"github.com/chelmertz/dotfiles/claude-review/ids"

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
		Author: "alice", HeadRefOid: "h1", LastUpdated: t0, Asked: askedAs(askedNotAsked)}
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
		want     Trigger
	}{
		{"nothing owed", ellyPR(nil), "h1", nil},
		{"review requested", ellyPR(func(p *EllyPR) { p.Asked = askedAs(askedRequested) }), "h1", Requested{At: t0.UTC()}},
		{"an Asked this build does not know", ellyPR(func(p *EllyPR) { p.Asked = askedAs("maybe") }), "h1", nil},
		{"replies", ellyPR(func(p *EllyPR) { p.ThreadsActionable = 2 }), "h1", Replies{At: t0.UTC()}},
		{"new head", ellyPR(func(p *EllyPR) { p.HeadRefOid = "h2" }), "h1", NewHead{Head: "h2"}},
		{"no head known", ellyPR(func(p *EllyPR) { p.HeadRefOid = "" }), "h1", nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := roundTrigger(c.pr, c.lastHead); got != c.want {
				t.Errorf("got %#v, want %#v", got, c.want)
			}
		})
	}
}

type fakeGate struct {
	descends bool
	base     string
	closed   bool
	// closedPRs closes single PRs by number; asked counts Base calls by
	// number when set; baseErr is GitHub not answering for that number.
	closedPRs map[int]bool
	asked     map[int]int
	baseErr   map[int]error
}

func (f fakeGate) Descends(PR, string, string) (bool, error) { return f.descends, nil }
func (f fakeGate) Base(pr PR) (string, bool, error) {
	if f.asked != nil {
		f.asked[pr.Number]++
	}
	return f.base, !f.closed && !f.closedPRs[pr.Number], f.baseErr[pr.Number]
}

// notSkipped is the plans tick acts on: a round, or a gate to show.
func notSkipped(plans []Planned) []Planned {
	var out []Planned
	for _, p := range plans {
		if p.Skip == "" {
			out = append(out, p)
		}
	}
	return out
}

func TestPlanTick(t *testing.T) {
	cfg := TickConfig{ReviewRepos: []string{"acme/*"}, User: "me", DailyCap: 3}
	requested := func(p *EllyPR) { p.Asked = askedAs(askedRequested) }
	plan := func(l *Ledger, g fakeGate, prs ...EllyPR) []Planned {
		t.Helper()
		got, err := planTick(prs, cfg, l, g, t0)
		if err != nil {
			t.Fatal(err)
		}
		return notSkipped(got)
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
	if len(got) != 1 || got[0].Gate != "" || got[0].Head != "h1" || got[0].Trigger.Kind() != trigRequested {
		t.Fatalf("accepted, requested: %+v", got)
	}

	if got := plan(l, ok, ellyPR(func(p *EllyPR) { requested(p); p.Author = "me" })); len(got) != 0 {
		t.Errorf("the user's own PR got %+v", got)
	}
	if got := plan(l, ok, ellyPR(func(p *EllyPR) { requested(p); p.RepoOwner = "globex" })); len(got) != 0 {
		t.Errorf("a repo outside review_repos got %+v", got)
	}
	// Bury replaced Ignore (2026-11-16): elly unburies on someone else's
	// update, a re-request included, and rounds resume.
	if got := plan(l, ok, ellyPR(func(p *EllyPR) { requested(p); p.Buried = true })); len(got) != 0 {
		t.Errorf("a buried PR is not left alone: %+v", got)
	}

	if got := plan(l, fakeGate{descends: false, base: "main"}, ellyPR(requested)); len(got) != 1 || got[0].Gate != gateFreshLook {
		t.Errorf("force-push off the accepted head: %+v", got)
	}
	if got := plan(l, fakeGate{descends: true, base: "release"}, ellyPR(requested)); len(got) != 1 || got[0].Gate != gateFreshLook {
		t.Errorf("base changed since acceptance: %+v", got)
	}
	// elly lags GitHub by up to a poll: a PR merged since still looks owed.
	if got := plan(l, fakeGate{descends: true, base: "main", closed: true}, ellyPR(requested)); len(got) != 0 {
		t.Errorf("a PR merged since elly's last poll got %+v", got)
	}

	trig := plan(l, ok, ellyPR(requested))[0].Trigger
	if _, err := l.StartRound(widgetsPR, "h1", trig, t0); err != nil {
		t.Fatal(err)
	}
	if got := plan(l, ok, ellyPR(requested)); len(got) != 0 {
		t.Errorf("same head and trigger ran twice: %+v", got)
	}
	if got := plan(l, ok, ellyPR(func(p *EllyPR) { p.HeadRefOid = "h2" })); len(got) != 1 || got[0].Trigger != Trigger(NewHead{Head: "h2"}) {
		t.Errorf("a push after the last round: %+v", got)
	}

	for range 2 {
		if _, err := l.StartRound(widgetsPR, "hx", NewHead{}, t0); err != nil {
			t.Fatal(err)
		}
	}
	if got := plan(l, ok, ellyPR(func(p *EllyPR) { p.HeadRefOid = "h3" })); len(got) != 1 || got[0].Gate != gatePaused {
		t.Errorf("past the daily cap: %+v", got)
	}

	if got := plan(l, ok, ellyPR(func(p *EllyPR) { p.HeadRefOid = "h4"; p.Buried = true })); len(got) != 0 {
		t.Errorf("a buried PR got %+v", got)
	}
}

func TestDecide(t *testing.T) {
	const head = "0123456789abcdef0123456789abcdef01234567"
	round := Round{PR: widgetsPR.URL(), Head: head, StartedAt: t0}
	approveLine := Outcome{Kind: OutcomeApprove, PrURL: widgetsPR.URL(), SHA: head}
	open := RoundFacts{State: "open", Head: head}
	approved := func(commit string) *Review {
		return &Review{ID: reviewID(9), State: "APPROVED", CommitID: commit}
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
			func(d Decision) bool { return isClosed(d) }, "clear"},
		{"clean round, no approval: Approve link", round, approveLine, open,
			func(d Decision) bool { return isK(d, kindApprove) }, "needs_you + token"},
		{"clean round on the user's own PR: no Approve link", round, approveLine, RoundFacts{State: "open", Head: head, Own: true},
			func(d Decision) bool { return isK(d, kindNothing) }, "handled, nothing"},
		{"clean round under a standing approval", round, approveLine, RoundFacts{State: "open", Head: head, UserReview: approved("old")},
			func(d Decision) bool { return isK(d, kindApproved) }, "handled"},
		{"approve line for another sha", round, Outcome{Kind: OutcomeApprove, PrURL: widgetsPR.URL(), SHA: strings.Repeat("f", 40)}, RoundFacts{State: "open", Head: head, RoundReviewURL: roundReview},
			func(d Decision) bool { return nextOf(d).State == stHandled }, "no Approve link"},
		{"approve line for another PR", round, Outcome{Kind: OutcomeApprove, PrURL: "https://github.com/acme/widgets/pull/43", SHA: head}, open,
			func(d Decision) bool { return !isK(d, kindApprove) }, "no Approve link"},
		{"approve line but the round posted findings", round, approveLine, RoundFacts{State: "open", Head: head, NewThreads: 2, RoundReviewURL: roundReview},
			func(d Decision) bool { return !isK(d, kindApprove) }, "no Approve link"},
		// A re-round that only confirms fixes in the threads and resolves them.
		{"approve line and only replies: Approve link", round, approveLine, RoundFacts{State: "open", Head: head, NewReplies: 3, RoundReviewURL: roundReview},
			func(d Decision) bool { return isK(d, kindApprove) }, "needs_you + token"},
		{"approve line but the user's threads are open", round, approveLine, RoundFacts{State: "open", Head: head, UnresolvedUserThreads: 1},
			func(d Decision) bool { return !isK(d, kindApprove) }, "no Approve link"},
		{"approve line but the head moved", round, approveLine, RoundFacts{State: "open", Head: "moved"},
			func(d Decision) bool { return !isK(d, kindApprove) }, "no Approve link"},
		{"findings after the user's approval dismiss it", round, Outcome{}, RoundFacts{State: "open", Head: head, NewThreads: 1, UserReview: approved("old"), RoundReviewURL: roundReview},
			func(d Decision) bool { return dismissOf(d) == reviewID(9) && isK(d, kindDismiss) }, "dismiss + needs_you"},
		{"replies after the user's approval do not dismiss it", round, Outcome{}, RoundFacts{State: "open", Head: head, NewReplies: 2, UserReview: approved("old"), RoundReviewURL: roundReview},
			func(d Decision) bool { return dismissOf(d).IsZero() && nextOf(d).State == stHandled }, "no dismissal, handled"},
		// The user read the findings and approved anyway, before tick settled the round.
		{"an approval given after the round started is not dismissed", round, Outcome{}, RoundFacts{State: "open", Head: head, NewThreads: 1, UserReview: &Review{ID: reviewID(9), State: "APPROVED", CommitID: "old", SubmittedAt: t0.Add(time.Minute)}, RoundReviewURL: roundReview},
			func(d Decision) bool { return dismissOf(d).IsZero() && nextOf(d).State == stHandled }, "no dismissal, handled"},
		// The summary is posted last: without it the round was cut off
		// (a shutdown, a crash) partway through posting, and resumes.
		{"findings without a summary: interrupted, resumed", round, Outcome{}, RoundFacts{State: "open", Head: head, NewThreads: 2},
			func(d Decision) bool { return isRetry(d) && isK(d, kindRetry) }, "retry"},
		{"replies without a summary: interrupted, resumed", round, Outcome{}, RoundFacts{State: "open", Head: head, NewReplies: 1},
			func(d Decision) bool { return isRetry(d) && isK(d, kindRetry) }, "retry"},
		// Inline comments and replies wait in a draft that the summary sends.
		{"drafts without a summary: interrupted, resumed", round, Outcome{}, RoundFacts{State: "open", Head: head, UnsentDrafts: 2},
			func(d Decision) bool { return isRetry(d) && isK(d, kindRetry) }, "retry"},
		{"drafts cut off again: the user decides on them", Round{PR: round.PR, Head: head, Retries: 1}, Outcome{}, RoundFacts{State: "open", Head: head, UnsentDrafts: 2},
			func(d Decision) bool {
				return !isRetry(d) && isK(d, kindDrafts) && strings.Contains(note(d), "2 unsent comments")
			}, "needs_you naming the draft"},
		{"nothing to post, but a draft holds comments: resumed", round, Outcome{Kind: OutcomeNothing}, RoundFacts{State: "open", Head: head, UnsentDrafts: 1},
			func(d Decision) bool { return isRetry(d) }, "retry"},
		{"approve line with comments in the draft: no Approve link", round, approveLine, RoundFacts{State: "open", Head: head, UnsentDrafts: 1},
			func(d Decision) bool { return !isK(d, kindApprove) }, "no Approve link"},
		{"cut off again: stuck", Round{PR: round.PR, Head: head, Retries: 1}, Outcome{}, RoundFacts{State: "open", Head: head, NewThreads: 2},
			func(d Decision) bool {
				return !isRetry(d) && isK(d, kindFailed) && strings.Contains(note(d), "summary")
			}, "stuck, names the missing summary"},
		{"a refused post: stuck, held back", Round{PR: round.PR, Head: head, PostRefused: true}, approveLine, open,
			func(d Decision) bool {
				return isK(d, kindRefused) && strings.Contains(note(d), "refused")
			}, "stuck, refused"},
		{"a refused post, summary posted: handled", Round{PR: round.PR, Head: head, PostRefused: true}, approveLine, RoundFacts{State: "open", Head: head, RoundReviewURL: roundReview},
			func(d Decision) bool {
				return isK(d, kindPosted) && nextOf(d).State == stHandled && strings.Contains(note(d), "limit") && !isRetry(d)
			}, "handled at the limit"},
		{"findings with the approval at the current head do not dismiss", round, Outcome{}, RoundFacts{State: "open", Head: head, NewThreads: 1, UserReview: approved(head), RoundReviewURL: roundReview},
			func(d Decision) bool { return dismissOf(d).IsZero() }, "no dismissal"},
		{"question", round, Outcome{Kind: OutcomeQuestion}, open,
			func(d Decision) bool {
				return isK(d, kindQuestion) && hasLink(row(d), "attach") && note(d) == "Claude has a question for you"
			}, "needs_you + attach, today's note"},
		{"question with its text", round, Outcome{Kind: OutcomeQuestion, Question: "Per booking or per venue?"}, open,
			func(d Decision) bool {
				return isK(d, kindQuestion) && note(d) == "Claude has a question for you: Per booking or per venue?"
			}, "needs_you with the question"},
		{"findings posted: handled", round, Outcome{}, RoundFacts{State: "open", Head: head, NewThreads: 3, RoundReviewURL: roundReview},
			func(d Decision) bool {
				return isK(d, kindPosted) && nextOf(d).Review == roundReview && hasLink(row(d), "attach") && !hasLink(row(d), "pullrequestreview-5")
			}, "handled, Open Claude, summary kept in state not on the row"},
		{"nothing to post, said so: handled, not retried", round, Outcome{Kind: OutcomeNothing}, open,
			func(d Decision) bool { return !isRetry(d) && isK(d, kindNothing) }, "handled"},
		{"nothing to show for it: retry once", round, Outcome{}, open,
			func(d Decision) bool { return isRetry(d) && isK(d, kindRetry) }, "retry"},
		{"nothing again: round failed", Round{PR: round.PR, Head: head, Retries: 1}, Outcome{}, open,
			func(d Decision) bool {
				return !isRetry(d) && isK(d, kindFailed) && strings.Contains(note(d), "failed")
			}, "stuck failed"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			d := decide(c.r, c.o, c.f, widgetsPR)
			if !c.check(d) {
				t.Errorf("got %+v (next %+v), want %s", d, nextOf(d), c.explain)
			}
		})
	}
}

func isK(d Decision, kind string) bool { return nextOf(d) != nil && nextOf(d).Kind == kind }

// row is the row a decision's state renders to.
func row(d Decision) *Annotation {
	if nextOf(d) == nil {
		return nil
	}
	a := render(widgetsPR, *nextOf(d), View{})
	return &a
}

func note(d Decision) string {
	if a := row(d); a != nil {
		return a.Note
	}
	return ""
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

// Every PR in review_repos that tick leaves alone carries the reason, so
// the journal can say why a round did not start.
func TestPlanTickNamesWhyItLeavesAPR(t *testing.T) {
	cfg := TickConfig{ReviewRepos: []string{"acme/*"}, User: "me", DailyCap: 3}
	asked := ellyPR(func(p *EllyPR) { p.Asked = askedAs(askedRequested) })
	ok := fakeGate{descends: true, base: "main"}
	skipOf := func(l *Ledger, g fakeGate, p EllyPR) skip {
		t.Helper()
		got, err := planTick([]EllyPR{p}, cfg, l, g, t0)
		if err != nil || len(got) != 1 {
			t.Fatalf("planned %+v %v", got, err)
		}
		return got[0].Skip
	}
	l := openTestLedger(t)
	if s := skipOf(l, ok, asked); s != skipNotAccepted {
		t.Errorf("never clicked: %q", s)
	}
	if err := l.Accept(widgetsPR, "h1", "main", t0); err != nil {
		t.Fatal(err)
	}
	if s := skipOf(l, ok, ellyPR(nil)); s != skipNothingOwed {
		t.Errorf("at the accepted head, nothing asked: %q", s)
	}
	if s := skipOf(l, fakeGate{descends: true, base: "main", closed: true}, asked); s != skipClosed {
		t.Errorf("merged since elly's poll: %q", s)
	}
	if s := skipOf(l, ok, asked); s != "" {
		t.Errorf("owed: %q", s)
	}
	if _, err := l.StartRound(widgetsPR, "h1", Click{At: t0}, t0.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	if s := skipOf(l, ok, asked); s != skipAlreadyRan {
		t.Errorf("a round after the last update: %q", s)
	}
	asked.Buried = true
	if s := skipOf(l, ok, asked); s != skipBuried {
		t.Errorf("buried: %q", s)
	}
}

// elly's button on a needs_you row is its Primary link, with that link's
// label: on a question it must answer it.
func TestDecideAQuestionLeadsWithAnswer(t *testing.T) {
	q := decide(Round{Head: sha}, Outcome{Kind: OutcomeQuestion, Question: "rows?"}, RoundFacts{State: prOpen, Head: sha}, widgetsPR)
	if a := row(q); a == nil || len(a.Links) == 0 || a.Links[0] != primary(answerLink(widgetsPR)) {
		t.Errorf("question links %+v", a)
	}
	if l := answerLink(widgetsPR); l.Label != "Answer" || l.URL != "claude-review://acme/widgets/42/attach" {
		t.Errorf("answer link %+v", l)
	}
}

// A post-limit stop has no worker: its button starts a new review, the click
// the note asks for (2026-11-16: "no round running"); Open Claude sits
// beside it, not as the button.
func TestDecideAPostRefusedRowStartsAgain(t *testing.T) {
	d := decide(Round{Head: sha, PostRefused: true}, Outcome{}, RoundFacts{State: prOpen, Head: sha}, widgetsPR)
	if a := row(d); a == nil || len(a.Links) < 2 || a.Links[0] != primary(startAgainLink(widgetsPR)) || a.Links[1] != attachLink(widgetsPR) {
		t.Errorf("links %+v", a)
	}
}

// nextOf is the state d leaves the PR in: Next's and Dismiss's own, a
// retry's running state, nil for a closed PR.
func nextOf(d Decision) *PRState {
	switch d := d.(type) {
	case Next:
		return &d.State
	case Dismiss:
		return &d.State
	case Retry:
		return &PRState{State: stRunning, Kind: kindRetry}
	case Closed:
	}
	return nil
}

func isClosed(d Decision) bool { _, ok := d.(Closed); return ok }
func isRetry(d Decision) bool  { _, ok := d.(Retry); return ok }

func dismissOf(d Decision) ids.ReviewID {
	if d, ok := d.(Dismiss); ok {
		return d.Review
	}
	return ids.ReviewID{}
}

func askedAs(v string) *string { return &v }

// elly's types.AskedRequested and types.AskedNotAsked name the same
// literals; a value either side moves alone reads as unreadable here.
func TestAskedLiterals(t *testing.T) {
	if askedRequested != "requested" || askedNotAsked != "not_asked" {
		t.Errorf("Asked literals moved: %q %q; move elly's with them", askedRequested, askedNotAsked)
	}
	if askedReadable([]EllyPR{{}}) || askedReadable([]EllyPR{{Asked: askedAs("Requested")}}) || !askedReadable([]EllyPR{{Asked: askedAs(askedNotAsked)}}) {
		t.Error("a missing or unknown Asked read as readable, or a known one as not")
	}
}
