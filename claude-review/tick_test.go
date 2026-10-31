package main

import (
	"bytes"
	"errors"
	"net/url"
	"slices"
	"strings"
	"testing"
	"time"
)

type fakeElly struct {
	prs     []EllyPR
	down    bool
	putErr  error
	put     map[string]Annotation
	deleted []string
}

func (e *fakeElly) PRs() ([]EllyPR, error) {
	if e.down {
		return nil, errors.New("connection refused")
	}
	return e.prs, nil
}
func (e *fakeElly) Put(u string, a Annotation, _ time.Time) error {
	if e.putErr != nil {
		return e.putErr
	}
	e.put[u] = a
	return nil
}
func (e *fakeElly) Delete(u string) error { e.deleted = append(e.deleted, u); return nil }

type fakeTickGitHub struct {
	fakeGate
	facts      RoundFacts
	state      string
	head       string
	user       *Review
	dismissed  []int64
	dismissErr error
}

func (g *fakeTickGitHub) RoundFacts(PR, Round) (RoundFacts, error) { return g.facts, nil }
func (g *fakeTickGitHub) Standing(PR) (string, string, *Review, error) {
	return g.state, g.head, g.user, nil
}
func (g *fakeTickGitHub) Dismiss(_ PR, id int64, _ string) error {
	if g.dismissErr != nil {
		return g.dismissErr
	}
	g.dismissed = append(g.dismissed, id)
	return nil
}

type tickWorld struct {
	h       tickHost
	elly    *fakeElly
	gh      *fakeTickGitHub
	started [][]string
	live    map[string]bool
	msgs    map[string]string
	out     *bytes.Buffer
}

func newTickWorld(t *testing.T) *tickWorld {
	w := &tickWorld{
		elly: &fakeElly{put: map[string]Annotation{}},
		gh:   &fakeTickGitHub{fakeGate: fakeGate{descends: true, base: "main"}, state: "open", head: "h1"},
		live: map[string]bool{},
		msgs: map[string]string{},
		out:  &bytes.Buffer{},
	}
	w.h = tickHost{
		tcfg:        TickConfig{ReviewRepos: []string{"acme/*"}, User: "me", DailyCap: 6, MaxRunning: 2, Wrapper: []string{"capped"}},
		ledger:      openTestLedger(t),
		elly:        w.elly,
		gh:          w.gh,
		running:     func(id string) bool { return w.live[id] },
		lastMessage: func(id string) (string, error) { return w.msgs[id], nil },
		findRepo:    func(PR) (string, string, error) { return "/clone", "upstream", nil },
		start:       func(_ string, argv []string) error { w.started = append(w.started, argv); return nil },
		now:         func() time.Time { return t0 },
		out:         w.out,
	}
	return w
}

func requestedPR(n int) EllyPR {
	return ellyPR(func(p *EllyPR) {
		p.Url = PR{Owner: "acme", Repo: "widgets", Number: n}.URL()
		p.ReviewRequestedFromUsers = []string{"me"}
	})
}

func (w *tickWorld) accept(t *testing.T, n int) PR {
	pr := PR{Owner: "acme", Repo: "widgets", Number: n}
	if err := w.h.ledger.Accept(pr, "h0", "main", t0); err != nil {
		t.Fatal(err)
	}
	return pr
}

func TestTickWithoutEllyDoesNothing(t *testing.T) {
	w := newTickWorld(t)
	w.accept(t, 42)
	w.elly.down = true
	if err := w.h.tick(); err == nil {
		t.Error("tick without elly reported success")
	}
	if len(w.started) != 0 || len(w.elly.put) != 0 {
		t.Errorf("tick without elly acted: started %v, put %v", w.started, w.elly.put)
	}
}

func TestTickStartsAnOwedRoundLockedDownAndCapped(t *testing.T) {
	w := newTickWorld(t)
	pr := w.accept(t, 42)
	w.elly.prs = []EllyPR{requestedPR(42)}
	if err := w.h.tick(); err != nil {
		t.Fatal(err)
	}
	if len(w.started) != 1 {
		t.Fatalf("started %v", w.started)
	}
	want := append([]string{"capped"}, UnattendedArgs(Config{}, pr, "upstream")...)
	if !slices.Equal(w.started[0], want) {
		t.Errorf("round command:\n%q\nwant\n%q", w.started[0], want)
	}
	if err := w.h.tick(); err != nil {
		t.Fatal(err)
	}
	if len(w.started) != 1 {
		t.Errorf("the same request started a second round: %d", len(w.started))
	}
}

func TestTickDryRunWritesNothing(t *testing.T) {
	w := newTickWorld(t)
	w.accept(t, 42)
	w.elly.prs = []EllyPR{requestedPR(42)}
	w.h.dry = true
	if err := w.h.tick(); err != nil {
		t.Fatal(err)
	}
	if len(w.started) != 0 {
		t.Errorf("a dry run started %v", w.started)
	}
	if rounds, _ := w.h.ledger.UnreadRounds(); len(rounds) != 0 {
		t.Errorf("a dry run recorded rounds %v", rounds)
	}
	if !strings.Contains(w.out.String(), "would start a round on acme/widgets#42") {
		t.Errorf("dry run said %q", w.out.String())
	}
}

func TestTickAtMostTwoRunning(t *testing.T) {
	w := newTickWorld(t)
	for n := 1; n <= 3; n++ {
		w.accept(t, n)
		w.elly.prs = append(w.elly.prs, requestedPR(n))
	}
	if err := w.h.tick(); err != nil {
		t.Fatal(err)
	}
	if len(w.started) != 2 {
		t.Fatalf("started %d rounds, want 2", len(w.started))
	}
	for n := 1; n <= 3; n++ {
		w.live[PR{Owner: "acme", Repo: "widgets", Number: n}.SessionID()] = true
	}
	if err := w.h.tick(); err != nil {
		t.Fatal(err)
	}
	if len(w.started) != 2 {
		t.Errorf("a third round started while two run: %d", len(w.started))
	}
}

func TestTickCleanRoundGetsAWorkingApproveLink(t *testing.T) {
	w := newTickWorld(t)
	pr := w.accept(t, 42)
	w.elly.prs = []EllyPR{requestedPR(42)}
	if err := w.h.tick(); err != nil {
		t.Fatal(err)
	}
	// ReadOutcome needs a full sha; make it the round's head.
	full := strings.Repeat("a", 40)
	w.msgs[pr.SessionID()] = "Clean. `! claude-review approve " + pr.URL() + " --at " + full + "`"
	if _, err := w.h.ledger.db.Exec(`update rounds set head = ?`, full); err != nil {
		t.Fatal(err)
	}
	w.gh.facts = RoundFacts{State: "open", Head: full, CommitsSinceApproval: -1}
	w.gh.head = full
	if err := w.h.tick(); err != nil {
		t.Fatal(err)
	}
	a, ok := w.elly.put[pr.URL()]
	if !ok || a.State != stateNeedsYou || len(a.Links) == 0 || a.Links[0].Label != "Approve" {
		t.Fatalf("elly got %+v", a)
	}
	u, err := url.Parse(a.Links[0].URL)
	if err != nil {
		t.Fatal(err)
	}
	if ok, _ := w.h.ledger.UseToken(pr, u.Query().Get("at"), u.Query().Get("token")); !ok {
		t.Errorf("the Approve link's token is not one the ledger minted: %s", a.Links[0].URL)
	}
}

func TestTickFindingsAfterApprovalDismiss(t *testing.T) {
	w := newTickWorld(t)
	pr := w.accept(t, 42)
	if _, err := w.h.ledger.StartRound(pr, "h1", "new_head", t0); err != nil {
		t.Fatal(err)
	}
	w.gh.facts = RoundFacts{State: "open", Head: "h1", NewComments: 2, UserReview: &Review{ID: 7, State: "APPROVED", CommitID: "h0"}}
	if err := w.h.tick(); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(w.gh.dismissed, []int64{7}) {
		t.Errorf("dismissed %v", w.gh.dismissed)
	}
	if a := w.elly.put[pr.URL()]; a.State != stateNeedsYou {
		t.Errorf("elly got %+v", a)
	}
}

func TestTickRefreshDropsOnlyOnEvidence(t *testing.T) {
	w := newTickWorld(t)
	pr := w.accept(t, 42)
	if err := w.h.ledger.SetAnnotation(pr, Annotation{State: stateNeedsYou, Note: "look"}); err != nil {
		t.Fatal(err)
	}
	// elly does not list the PR this time: not evidence.
	if err := w.h.tick(); err != nil {
		t.Fatal(err)
	}
	if _, ok := w.elly.put[pr.URL()]; !ok || len(w.elly.deleted) != 0 {
		t.Fatalf("a PR missing from elly lost its annotation: put %v deleted %v", w.elly.put, w.elly.deleted)
	}
	w.gh.user = &Review{State: "APPROVED", CommitID: "h0"}
	if err := w.h.tick(); err != nil {
		t.Fatal(err)
	}
	if len(w.elly.deleted) != 0 {
		t.Fatalf("an approval of an older commit cleared the annotation: %v", w.elly.deleted)
	}
	w.gh.user = &Review{State: "APPROVED", CommitID: "h1"}
	if err := w.h.tick(); err != nil {
		t.Fatal(err)
	}
	if len(w.elly.deleted) != 1 {
		t.Errorf("approval at the head did not clear the annotation: %v", w.elly.deleted)
	}
	if all, _ := w.h.ledger.Annotations(); len(all) != 0 {
		t.Errorf("ledger still holds %v", all)
	}
}

func TestTickFreshLookGateStartsNothing(t *testing.T) {
	w := newTickWorld(t)
	pr := w.accept(t, 42)
	w.gh.descends = false
	w.elly.prs = []EllyPR{requestedPR(42)}
	if err := w.h.tick(); err != nil {
		t.Fatal(err)
	}
	if len(w.started) != 0 {
		t.Errorf("started %v on a force-pushed PR", w.started)
	}
	if a := w.elly.put[pr.URL()]; a.State != stateNeedsYou || !strings.Contains(a.Note, "look again") {
		t.Errorf("elly got %+v", a)
	}
}

func TestTickARetryCountsAsRunning(t *testing.T) {
	w := newTickWorld(t)
	failed := w.accept(t, 1)
	if _, err := w.h.ledger.StartRound(failed, "h1", "new_head", t0); err != nil {
		t.Fatal(err)
	}
	// The round ended with nothing to show: tick retries it.
	w.gh.facts = RoundFacts{State: "open", Head: "h1"}
	w.h.tcfg.MaxRunning = 1
	w.accept(t, 2)
	w.elly.prs = []EllyPR{requestedPR(2)}
	if err := w.h.tick(); err != nil {
		t.Fatal(err)
	}
	if len(w.started) != 1 || !slices.Contains(w.started[0], failed.SessionID()) {
		t.Errorf("with one slot, the retry must take it: started %q", w.started)
	}
}

func TestTickAFailedDismissalLeavesTheRoundForTheNextTick(t *testing.T) {
	w := newTickWorld(t)
	pr := w.accept(t, 42)
	if _, err := w.h.ledger.StartRound(pr, "h1", "new_head", t0); err != nil {
		t.Fatal(err)
	}
	w.gh.facts = RoundFacts{State: "open", Head: "h1", NewComments: 1, UserReview: &Review{ID: 7, State: "APPROVED", CommitID: "h0"}}
	w.gh.dismissErr = errors.New("403")
	if err := w.h.tick(); err != nil {
		t.Fatal(err)
	}
	if unread, _ := w.h.ledger.UnreadRounds(); len(unread) != 1 {
		t.Errorf("a round whose dismissal failed was marked read: %v", unread)
	}
	if !strings.Contains(w.out.String(), "403") {
		t.Errorf("the failure was not reported: %q", w.out.String())
	}
}

func TestTickOneEllyFailureDoesNotStopTheRest(t *testing.T) {
	w := newTickWorld(t)
	for n := 1; n <= 2; n++ {
		pr := w.accept(t, n)
		if err := w.h.ledger.SetAnnotation(pr, Annotation{State: stateNeedsYou, Note: "look"}); err != nil {
			t.Fatal(err)
		}
	}
	w.elly.putErr = errors.New("404 elly does not list that PR")
	if err := w.h.tick(); err != nil {
		t.Fatalf("one PR elly does not list failed the whole tick: %v", err)
	}
	if strings.Count(w.out.String(), "404") != 2 {
		t.Errorf("want both failures reported: %q", w.out.String())
	}
	if all, _ := w.h.ledger.Annotations(); len(all) != 2 {
		t.Errorf("a failed send dropped an annotation from the ledger: %v", all)
	}
}
