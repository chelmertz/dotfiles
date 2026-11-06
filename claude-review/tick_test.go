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
	login  string // the GitHub login elly polls as
	prs    []EllyPR
	down   bool
	putErr error
	// reject refuses one state, as an elly that predates it does.
	reject  string
	put     map[string]Annotation
	expires map[string]time.Time
	deleted []string
	// sourced holds annotations put under another source, by source and URL.
	sourced map[string]Annotation
}

func (e *fakeElly) PRs() ([]EllyPR, error) {
	if e.down {
		return nil, errors.New("connection refused")
	}
	return e.prs, nil
}
func (e *fakeElly) Username() (string, error) { return e.login, nil }
func (e *fakeElly) Put(u string, a Annotation, exp time.Time) error {
	if e.putErr != nil {
		return e.putErr
	}
	if e.reject != "" && a.State == e.reject {
		return errors.New("elly: 400 Bad Request state must be handled or needs_you")
	}
	e.put[u] = a
	if e.expires == nil {
		e.expires = map[string]time.Time{}
	}
	e.expires[u] = exp
	return nil
}
func (e *fakeElly) PutSource(source, u string, a Annotation, _ time.Time) error {
	if e.sourced == nil {
		e.sourced = map[string]Annotation{}
	}
	e.sourced[source+" "+u] = a
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
	h        tickHost
	elly     *fakeElly
	gh       *fakeTickGitHub
	started  [][]string
	live     map[string]bool
	blocked  map[string]bool
	msgs     map[string]string
	out      *bytes.Buffer
	fs       *fakeSessions
	fresh    map[string]bool // round sessions with no transcript yet
	locked   map[string]bool
	trusted  []string
	pid      int
	notified []string
}

func newTickWorld(t *testing.T) *tickWorld {
	w := &tickWorld{
		elly:    &fakeElly{login: "me", put: map[string]Annotation{}},
		gh:      &fakeTickGitHub{fakeGate: fakeGate{descends: true, base: "main"}, state: "open", head: "h1"},
		live:    map[string]bool{},
		blocked: map[string]bool{},
		msgs:    map[string]string{},
		out:     &bytes.Buffer{},
		fs:      newFakeSessions(),
		fresh:   map[string]bool{},
		locked:  map[string]bool{},
		pid:     1000,
	}
	w.h = tickHost{
		tcfg:    TickConfig{ReviewRepos: []string{"acme/*"}, User: "me", DailyCap: 6, MaxRunning: 2, TotalDailyCap: 24, Wrapper: []string{"capped"}},
		ledger:  openTestLedger(t),
		elly:    w.elly,
		gh:      w.gh,
		running: func(id string) bool { return w.live[id] },
		blocked: func(id string) bool { return w.blocked[id] },
		lastMessage: func(id string) (string, error) {
			w.fs.events = append(w.fs.events, "read "+id[:8])
			return w.msgs[id], nil
		},
		findRepo: func(PR) (string, string, error) { return "/clone", "upstream", nil },
		// A launch adds the worker Claude Code would: listed under the
		// fixed id that follows --resume.
		start: func(_ string, argv []string) error {
			w.started = append(w.started, argv)
			if !slices.Contains(argv, "--bg") {
				return nil // the bootstrap
			}
			id := argv[slices.Index(argv, "--resume")+1]
			w.pid++
			w.fs.workers = append(w.fs.workers, workerFor(id, w.pid))
			if _, ok := w.fs.saved[id]; !ok && len(argv) > 6 {
				w.fs.saved[id] = savedFrom(argv)
			}
			return nil
		},
		exists: func(id string) bool { return !w.fresh[id] },
		now:    func() time.Time { return t0 },
		out:    w.out,
		lock: func(id string) (func(), error) {
			if w.locked[id] {
				return nil, errLocked
			}
			return func() {}, nil
		},
		sess:      w.fs.sessions(),
		trust:     func(dir string) error { w.trusted = append(w.trusted, dir); return nil },
		version:   func() (string, error) { return "2.1.314 (Claude Code)", nil },
		selfCheck: func() error { return nil },
		notify:    func(msg string) { w.notified = append(w.notified, msg) },
	}
	w.fs.saved = map[string][]string{}
	return w
}

// savedFrom is what Claude Code saves from a first background launch: the
// flags without the binary, --bg, --resume <id> and the prompt.
func savedFrom(argv []string) []string {
	i := slices.Index(argv, "--resume")
	out := slices.Clone(argv[slices.Index(argv, "--bg")+1 : i])
	return append(out, argv[i+3:]...)
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

// elly's turn reasons are computed for the login its token belongs to; a
// tick for another login would start rounds owed to someone else.
func TestTickRefusesAnEllyPollingAsAnotherLogin(t *testing.T) {
	for login, runs := range map[string]bool{"me": true, "Me": true, "other": false, "": false} {
		w := newTickWorld(t)
		w.accept(t, 42)
		w.elly.prs = []EllyPR{requestedPR(42)}
		w.elly.login = login
		err := w.h.tick()
		if runs != (err == nil) || runs != (len(w.started) > 0) {
			t.Errorf("elly as %q: err %v, started %d", login, err, len(w.started))
		}
		if !runs && (err == nil || !strings.Contains(err.Error(), "me")) {
			t.Errorf("elly as %q: the error does not name the login tick runs as: %v", login, err)
		}
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
	want := append([]string{"capped"}, RoundArgs(Config{}, pr, "upstream", true)...)
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
	if !strings.Contains(w.out.String(), "would start a round on https://github.com/acme/widgets/pull/42") {
		t.Errorf("dry run said %q", w.out.String())
	}
}

// A window left open on a PR holds its rounds back; a dry run says so, or
// it reads as tick having found nothing owed.
func TestTickDryRunNamesAnOpenSession(t *testing.T) {
	w := newTickWorld(t)
	pr := w.accept(t, 42)
	w.elly.prs = []EllyPR{requestedPR(42)}
	w.live[pr.RoundSessionID()] = true
	w.h.dry = true
	if err := w.h.tick(); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(w.out.String(), "would leave https://github.com/acme/widgets/pull/42: its session is open") {
		t.Errorf("dry run said %q", w.out.String())
	}
}

// A dry run once started three real rounds through the retry path, which
// launched outside the dry-run guard.
func TestTickDryRunRetriesNothing(t *testing.T) {
	w := newTickWorld(t)
	failed := w.accept(t, 1)
	if _, err := w.h.ledger.StartRound(failed, "h1", "new_head", t0); err != nil {
		t.Fatal(err)
	}
	w.gh.facts = RoundFacts{State: "open", Head: "h1"}
	w.h.dry = true
	if err := w.h.tick(); err != nil {
		t.Fatal(err)
	}
	if len(w.started) != 0 {
		t.Errorf("a dry run started %q", w.started)
	}
	if rounds, _ := w.h.ledger.UnreadRounds(); len(rounds) != 1 || rounds[0].Retries != 0 {
		t.Errorf("a dry run changed the round: %+v", rounds)
	}
	if !strings.Contains(w.out.String(), "would retry https://github.com/acme/widgets/pull/1") {
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
		w.live[PR{Owner: "acme", Repo: "widgets", Number: n}.RoundSessionID()] = true
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
	w.msgs[pr.RoundSessionID()] = "Clean. `! claude-review approve " + pr.URL() + " --at " + full + "`"
	if _, err := w.h.ledger.db.Exec(`update rounds set head = ?`, full); err != nil {
		t.Fatal(err)
	}
	w.gh.facts = RoundFacts{State: "open", Head: full}
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
	if want := []string{pr.URL() + " needs you: clean round at aaaaaaaaa"}; !slices.Equal(w.notified, want) {
		t.Errorf("notified %q, want %q", w.notified, want)
	}
}

func TestTickFindingsAfterApprovalDismiss(t *testing.T) {
	w := newTickWorld(t)
	pr := w.accept(t, 42)
	if _, err := w.h.ledger.StartRound(pr, "h1", "new_head", t0); err != nil {
		t.Fatal(err)
	}
	w.gh.facts = RoundFacts{State: "open", Head: "h1", NewThreads: 2, UserReview: &Review{ID: 7, State: "APPROVED", CommitID: "h0"}}
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
	if len(w.started) != 1 || !slices.Contains(w.started[0], failed.RoundSessionID()) {
		t.Errorf("with one slot, the retry must take it: started %q", w.started)
	}
}

func TestTickAFailedDismissalLeavesTheRoundForTheNextTick(t *testing.T) {
	w := newTickWorld(t)
	pr := w.accept(t, 42)
	if _, err := w.h.ledger.StartRound(pr, "h1", "new_head", t0); err != nil {
		t.Fatal(err)
	}
	w.gh.facts = RoundFacts{State: "open", Head: "h1", NewThreads: 1, UserReview: &Review{ID: 7, State: "APPROVED", CommitID: "h0"}}
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

func TestLaunchRefusesInADryRun(t *testing.T) {
	w := newTickWorld(t)
	w.h.dry = true
	if _, err := w.h.launch(widgetsPR, nil, func(time.Time) error { return nil }); err == nil || len(w.started) != 0 {
		t.Errorf("launch in a dry run: err %v, started %q", err, w.started)
	}
}

// A round stalled on a permission it lacks waits for the user; a retry would
// resume on top of the live, blocked worker.
func TestTickABlockedRoundNeedsTheUser(t *testing.T) {
	w := newTickWorld(t)
	pr := w.accept(t, 1)
	if _, err := w.h.ledger.StartRound(pr, "h1", "new_head", t0); err != nil {
		t.Fatal(err)
	}
	w.gh.facts = RoundFacts{State: "open", Head: "h1"}
	w.blocked[pr.RoundSessionID()] = true
	if err := w.h.tick(); err != nil {
		t.Fatal(err)
	}
	if len(w.started) != 0 {
		t.Errorf("a blocked round was retried: %q", w.started)
	}
	all, _ := w.h.ledger.Annotations()
	a := all[pr.URL()]
	if a.State != stateNeedsYou || !strings.Contains(a.Note, "permission") || !hasLink(&a, "attach") {
		t.Errorf("annotation %+v", a)
	}
}

// A loop that keeps starting rounds stops at the cap across all PRs, and
// stays stopped after the 24 hours have passed: only a reset reopens it.
func TestTickBreakerStopsARunawayLoopUntilReset(t *testing.T) {
	w := newTickWorld(t)
	w.h.tcfg.TotalDailyCap = 2
	w.h.tcfg.MaxRunning = 10
	for n := 1; n <= 3; n++ {
		w.accept(t, n)
		w.elly.prs = append(w.elly.prs, requestedPR(n))
	}
	if err := w.h.tick(); err == nil || !strings.Contains(err.Error(), "2 rounds started in 24 hours") {
		t.Errorf("a tripped tick reported %v", err)
	}
	if len(w.started) != 2 {
		t.Fatalf("started %d rounds past a cap of 2", len(w.started))
	}
	if rounds, _ := w.h.ledger.UnreadRounds(); len(rounds) != 2 {
		t.Errorf("the refused start left a round in the ledger: %+v", rounds)
	}

	w.h.now = func() time.Time { return t0.Add(25 * time.Hour) }
	for n := 1; n <= 2; n++ {
		w.live[PR{Owner: "acme", Repo: "widgets", Number: n}.RoundSessionID()] = true
	}
	if err := w.h.tick(); err == nil {
		t.Error("the breaker closed on its own")
	}
	if len(w.started) != 2 {
		t.Errorf("a round started while tripped: %d", len(w.started))
	}
	if a, _ := w.h.ledger.Annotations(); !strings.HasPrefix(a[PR{Owner: "acme", Repo: "widgets", Number: 3}.URL()].Note, "paused: 2 rounds started") {
		t.Errorf("an owed PR does not say why it waits: %+v", a)
	}

	if err := w.h.ledger.Reset(w.h.now()); err != nil {
		t.Fatal(err)
	}
	if err := w.h.tick(); err != nil {
		t.Fatal(err)
	}
	if len(w.started) != 3 {
		t.Errorf("after a reset: started %d", len(w.started))
	}
}

func TestTickBreakerCountsRetries(t *testing.T) {
	w := newTickWorld(t)
	failed := w.accept(t, 1)
	if _, err := w.h.ledger.StartRound(failed, "h1", "new_head", t0); err != nil {
		t.Fatal(err)
	}
	w.gh.facts = RoundFacts{State: "open", Head: "h1"}
	w.h.tcfg.TotalDailyCap = 1
	if err := w.h.tick(); err == nil {
		t.Error("a retry past the cap did not trip the breaker")
	}
	if len(w.started) != 0 {
		t.Errorf("a retry started past the cap: %q", w.started)
	}
}

func TestLaunchWithoutACapStartsNothing(t *testing.T) {
	w := newTickWorld(t)
	w.h.tcfg.TotalDailyCap = 0
	if _, err := w.h.launch(widgetsPR, nil, func(time.Time) error { return nil }); err == nil || len(w.started) != 0 {
		t.Errorf("launch with no cap: err %v, started %q", err, w.started)
	}
}

// The retry path reaches launch without passing the owed-PR gate, so the
// latch has to hold in launch itself.
func TestLaunchRefusesWhileTripped(t *testing.T) {
	w := newTickWorld(t)
	if err := w.h.ledger.Trip("too many"); err != nil {
		t.Fatal(err)
	}
	recorded := false
	if _, err := w.h.launch(widgetsPR, nil, func(time.Time) error { recorded = true; return nil }); err == nil || recorded || len(w.started) != 0 {
		t.Errorf("launch while tripped: err %v, recorded %v, started %q", err, recorded, w.started)
	}
}

// A round pushed over its post limit is suspect whatever it says: even a
// clean approve line gets no Approve link and no retry, and the PR gets no
// more rounds until the user clicks it again.
func TestTickARoundOverItsPostLimitStopsThatPR(t *testing.T) {
	w := newTickWorld(t)
	pr := w.accept(t, 42)
	w.elly.prs = []EllyPR{requestedPR(42)}
	if err := w.h.tick(); err != nil {
		t.Fatal(err)
	}
	full := strings.Repeat("a", 40)
	if _, err := w.h.ledger.db.Exec(`update rounds set head = ?`, full); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if _, err := w.h.ledger.SpendPost(pr, full, postSlotNew, 1, t0); err != nil {
			t.Fatal(err)
		}
	}
	w.msgs[pr.RoundSessionID()] = "Clean. `! claude-review approve " + pr.URL() + " --at " + full + "`"
	w.gh.facts = RoundFacts{State: "open", Head: full}
	w.gh.head = full
	w.elly.prs = []EllyPR{ellyPR(func(p *EllyPR) { p.Url = pr.URL(); p.HeadRefOid = "h9" })}
	if err := w.h.tick(); err != nil {
		t.Fatal(err)
	}
	a := w.elly.put[pr.URL()]
	if a.State != stateStuck || !strings.Contains(a.Note, "refused over the limit") || slices.ContainsFunc(a.Links, func(l Link) bool { return l.Label == "Approve" }) {
		t.Errorf("elly got %+v", a)
	}
	if want := []string{pr.URL() + " needs you: " + a.Note}; !slices.Equal(w.notified, want) {
		t.Errorf("notified %q, want %q", w.notified, want)
	}
	if len(w.started) != 1 {
		t.Errorf("rounds after the limit: %q", w.started)
	}
	if ignored, _ := w.h.ledger.Ignored(pr); !ignored {
		t.Error("the PR is not held back until the next click")
	}
}

// A launch that fails leaves nothing in the ledger: a round recorded first
// would be settled from the previous round's last message.
func TestTickRecordsARoundOnlyAfterItsLaunch(t *testing.T) {
	w := newTickWorld(t)
	w.accept(t, 42)
	w.elly.prs = []EllyPR{requestedPR(42)}
	w.h.start = func(string, []string) error { return errors.New("Workspace not trusted") }
	if err := w.h.tick(); err != nil {
		t.Fatal(err)
	}
	if rounds, _ := w.h.ledger.UnreadRounds(); len(rounds) != 0 {
		t.Errorf("a failed launch left a round: %+v", rounds)
	}
	if !strings.Contains(w.out.String(), "Workspace not trusted") {
		t.Errorf("the failure was not reported: %q", w.out)
	}
}

// Launched but never listed: nothing recorded, and the PR needs the user.
func TestTickAWorkerThatNeverAppearsNeedsTheUser(t *testing.T) {
	w := newTickWorld(t)
	pr := w.accept(t, 42)
	w.elly.prs = []EllyPR{requestedPR(42)}
	w.h.start = func(_ string, argv []string) error { w.started = append(w.started, argv); return nil }
	if err := w.h.tick(); err != nil {
		t.Fatal(err)
	}
	if rounds, _ := w.h.ledger.UnreadRounds(); len(rounds) != 0 {
		t.Errorf("an unlisted launch left a round: %+v", rounds)
	}
	if a := w.elly.put[pr.URL()]; a.State != stateNeedsYou || !strings.Contains(a.Note, "no worker was listed") {
		t.Errorf("elly got %+v", a)
	}
}

// A launch that forks the session stops the fork, trips the breaker and
// records nothing.
func TestTickAForkedWorkerTripsTheBreaker(t *testing.T) {
	w := newTickWorld(t)
	pr := w.accept(t, 42)
	w.elly.prs = []EllyPR{requestedPR(42)}
	start := w.h.start
	w.h.start = func(dir string, argv []string) error {
		w.fs.workers = append(w.fs.workers, Worker{Short: "188e19ec", SessionID: "188e19ec-1111", PID: 900, Fork: true, LaunchID: pr.RoundSessionID()})
		return start(dir, argv)
	}
	if err := w.h.tick(); err == nil {
		t.Error("a fork did not fail the tick")
	}
	if reason, _ := w.h.ledger.Tripped(); !strings.Contains(reason, "forked") {
		t.Errorf("breaker: %q", reason)
	}
	if !slices.Contains(w.fs.events, "stop 188e19ec") {
		t.Errorf("the fork was not stopped: %q", w.fs.events)
	}
	if rounds, _ := w.h.ledger.UnreadRounds(); len(rounds) != 0 {
		t.Errorf("a forked launch left a round: %+v", rounds)
	}
	if a, _ := w.h.ledger.Annotations(); a[pr.URL()].State != stateNeedsYou {
		t.Errorf("annotations %+v", a)
	}
}

// A click holding the PR's lock keeps tick off it: no start, and its
// unread round stays unread.
func TestTickLeavesALockedPRAlone(t *testing.T) {
	w := newTickWorld(t)
	pr := w.accept(t, 42)
	other := w.accept(t, 43)
	if _, err := w.h.ledger.StartRound(other, "h1", "new_head", t0); err != nil {
		t.Fatal(err)
	}
	w.gh.facts = RoundFacts{State: "open", Head: "h1", NewThreads: 1}
	w.elly.prs = []EllyPR{requestedPR(42)}
	w.locked[pr.SessionID()] = true
	w.locked[other.SessionID()] = true
	if err := w.h.tick(); err != nil {
		t.Fatal(err)
	}
	if len(w.started) != 0 {
		t.Errorf("started %d rounds on a locked PR", len(w.started))
	}
	if rounds, _ := w.h.ledger.UnreadRounds(); len(rounds) != 1 {
		t.Errorf("settled a locked PR's round: %+v", rounds)
	}
}

// The idle worker of a finished round is stopped for the next one, but
// only after that round was read: stopping first and reading later would
// read the new round's words for the old one.
func TestTickSettlesTheLastRoundBeforeStoppingItsWorker(t *testing.T) {
	w := newTickWorld(t)
	pr := w.accept(t, 42)
	if _, err := w.h.ledger.StartRound(pr, "h0", "click@x", t0); err != nil {
		t.Fatal(err)
	}
	w.fs.workers = []Worker{workerFor(pr.RoundSessionID(), 500)}
	w.fs.states[pr.RoundSessionID()] = "done"
	w.gh.facts = RoundFacts{State: "open", Head: "h1", NewThreads: 1}
	w.elly.prs = []EllyPR{requestedPR(42)}
	calls := 0
	w.h.running = func(string) bool { calls++; return calls == 1 } // still running when the settle pass looks
	if err := w.h.tick(); err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(w.fs.events, ","); got != "read "+pr.RoundSessionID()[:8]+",stop "+pr.RoundSessionID()[:8] {
		t.Errorf("events %s", got)
	}
	rounds, _ := w.h.ledger.UnreadRounds()
	if len(w.started) != 1 || len(rounds) != 1 || rounds[0].Trigger == "click@x" {
		t.Errorf("started %d, unread %+v", len(w.started), rounds)
	}
}

const windowNoteWant = "review window open; close it to let rounds run"

// A legacy window holds the PR's rounds back; tick says so in elly and
// clears it on the first tick that sees the window gone.
func TestTickNamesALegacyWindowAndClearsIt(t *testing.T) {
	w := newTickWorld(t)
	pr := w.accept(t, 42)
	w.elly.prs = []EllyPR{requestedPR(42)}
	w.fs.window = 4242
	w.h.running = func(string) bool { return w.fs.window > 0 }
	if err := w.h.tick(); err != nil {
		t.Fatal(err)
	}
	if len(w.started) != 0 {
		t.Errorf("started beside a window: %q", w.started)
	}
	if a := w.elly.put[pr.URL()]; a.State != stateNeedsYou || a.Note != windowNoteWant {
		t.Errorf("elly got %+v", a)
	}
	w.fs.window = 0
	w.elly.prs = nil
	if err := w.h.tick(); err != nil {
		t.Fatal(err)
	}
	if all, _ := w.h.ledger.Annotations(); len(all) != 0 || !slices.Contains(w.elly.deleted, pr.URL()) {
		t.Errorf("the window note outlived the window: ledger %+v, deleted %q", all, w.elly.deleted)
	}
}

func TestTickTrustsTheCloneBeforeLaunching(t *testing.T) {
	w := newTickWorld(t)
	w.accept(t, 42)
	w.elly.prs = []EllyPR{requestedPR(42)}
	w.h.trust = func(string) error { return errors.New("trusting /clone did not hold") }
	if err := w.h.tick(); err != nil {
		t.Fatal(err)
	}
	if len(w.started) != 0 || !strings.Contains(w.out.String(), "did not hold") {
		t.Errorf("launched %d rounds into an untrusted clone, out %q", len(w.started), w.out)
	}
	w = newTickWorld(t)
	w.accept(t, 42)
	w.elly.prs = []EllyPR{requestedPR(42)}
	if err := w.h.tick(); err != nil || !slices.Equal(w.trusted, []string{"/clone"}) {
		t.Errorf("trusted %q, err %v", w.trusted, err)
	}
}

// A new Claude Code version runs the self-check once, before any round.
func TestTickSelfChecksANewClaudeVersionOnce(t *testing.T) {
	w := newTickWorld(t)
	w.accept(t, 42)
	w.elly.prs = []EllyPR{requestedPR(42)}
	checks := 0
	w.h.version = func() (string, error) { return "2.1.315 (Claude Code)", nil }
	w.h.selfCheck = func() error { checks++; return nil }
	if err := w.h.tick(); err != nil {
		t.Fatal(err)
	}
	if checks != 1 || len(w.started) != 1 {
		t.Errorf("checks %d, started %d", checks, len(w.started))
	}
	if v, _ := w.h.ledger.Meta(metaClaudeVersion); v != "2.1.315 (Claude Code)" {
		t.Errorf("recorded version %q", v)
	}
	if err := w.h.tick(); err != nil || checks != 1 {
		t.Errorf("the same version was checked again: %d %v", checks, err)
	}
}

func TestTickAFailedSelfCheckTripsTheBreakerAndStartsNothing(t *testing.T) {
	w := newTickWorld(t)
	w.accept(t, 42)
	w.elly.prs = []EllyPR{requestedPR(42)}
	var told []string
	w.h.notify = func(msg string) { told = append(told, msg) }
	w.h.version = func() (string, error) { return "2.1.315", nil }
	w.h.selfCheck = func() error { return errors.New("a resume after stop: it forked") }
	if err := w.h.tick(); err == nil {
		t.Error("a failed self-check passed the tick")
	}
	if len(w.started) != 0 {
		t.Errorf("started %d rounds after a failed self-check", len(w.started))
	}
	reason, _ := w.h.ledger.Tripped()
	if !strings.Contains(reason, "2.1.315") || !strings.Contains(reason, "it forked") || len(told) != 1 || !strings.Contains(told[0], "it forked") {
		t.Errorf("breaker %q, told %q", reason, told)
	}
	if v, _ := w.h.ledger.Meta(metaClaudeVersion); v != "" {
		t.Errorf("a failed version was recorded: %q", v)
	}
	w.h.version = func() (string, error) { return "", errors.New("claude: not found") }
	if err := w.h.ledger.Reset(t0); err != nil {
		t.Fatal(err)
	}
	if err := w.h.tick(); err == nil || len(w.started) != 0 {
		t.Errorf("no version, err %v, started %d", err, len(w.started))
	}
}

// The first background round carries the policy and Claude Code saves it;
// every later round carries no flags, which continues the session instead
// of forking it, and only under that same saved policy.
func TestTickResumesFlaglessUnderTheSavedPolicy(t *testing.T) {
	w := newTickWorld(t)
	pr := w.accept(t, 42)
	w.fresh[pr.RoundSessionID()] = true
	w.elly.prs = []EllyPR{requestedPR(42)}
	if err := w.h.tick(); err != nil {
		t.Fatal(err)
	}
	if len(w.started) != 2 || !slices.Equal(w.started[0], RoundBootstrapArgs(pr)) || !slices.Equal(w.started[1], append([]string{"capped"}, RoundArgs(Config{}, pr, "upstream", true)...)) {
		t.Fatalf("first round: %d commands", len(w.started))
	}
	if !slices.Equal(w.fs.saved[pr.RoundSessionID()], PolicyFlags(pr, "upstream")) {
		t.Fatalf("the fake saved %q", w.fs.saved[pr.RoundSessionID()])
	}
	delete(w.fresh, pr.RoundSessionID())
	w.fs.states[pr.RoundSessionID()] = jobDone
	w.gh.facts = RoundFacts{State: "open", Head: "h1", NewThreads: 1, RoundReviewURL: "r"}
	w.elly.prs = []EllyPR{ellyPR(func(p *EllyPR) { p.Url = pr.URL(); p.HeadRefOid = "h2" })}
	if err := w.h.tick(); err != nil {
		t.Fatal(err)
	}
	if len(w.started) != 3 || !slices.Equal(w.started[2], append([]string{"capped"}, ResumeArgs(Config{}, pr, "upstream", true)...)) {
		t.Errorf("later round: %q", w.started[2:])
	}

	// Saved under another policy: nothing starts, and the user hears why.
	w = newTickWorld(t)
	pr = w.accept(t, 42)
	w.fs.saved[pr.RoundSessionID()] = []string{"--setting-sources", "project,local"}
	w.elly.prs = []EllyPR{requestedPR(42)}
	if err := w.h.tick(); err != nil {
		t.Fatal(err)
	}
	if len(w.started) != 0 {
		t.Errorf("started %d commands on a session saved under other options", len(w.started))
	}
	if a, _ := w.h.ledger.Annotations(); a[pr.URL()].State != stateNeedsYou || !strings.Contains(a[pr.URL()].Note, "other options") {
		t.Errorf("annotations %+v", a)
	}
	if rounds, _ := w.h.ledger.UnreadRounds(); len(rounds) != 0 {
		t.Errorf("rounds %+v", rounds)
	}
}

// closedWithWorker is PR n closed on GitHub with a click's round unread and
// its worker still working on it: the merge came mid-round.
func (w *tickWorld) closedWithWorker(t *testing.T, n, pid int) PR {
	pr := w.accept(t, n)
	if _, err := w.h.ledger.StartRound(pr, "h1", clickTrigger(t0), t0); err != nil {
		t.Fatal(err)
	}
	w.fs.workers = append(w.fs.workers, workerFor(pr.RoundSessionID(), pid))
	w.fs.states[pr.RoundSessionID()] = "working"
	w.live[pr.RoundSessionID()] = true
	w.gh.closedPRs[n] = true
	return pr
}

// Tick converges on a closed PR whoever left its worker: a working round,
// a worker under an older id, a round already read. It stops them, settles
// the unread round as closed, leaves a blocked worker on an open PR alone,
// and asks GitHub only about PRs with a worker or an unread round.
func TestTickStopsWorkersOnClosedPRs(t *testing.T) {
	w := newTickWorld(t)
	w.gh.closedPRs, w.gh.asked = map[int]bool{}, map[int]int{}
	w.gh.facts = RoundFacts{State: "open", Head: "h1"}
	merged := w.closedWithWorker(t, 42, 501)
	if _, err := w.h.ledger.StartRound(merged, "h0", "new_head", t0); err != nil {
		t.Fatal(err)
	}
	open := w.accept(t, 43)
	w.fs.workers = append(w.fs.workers, workerFor(open.RoundSessionID(), 502))
	w.fs.states[open.RoundSessionID()] = "blocked"
	legacy := w.accept(t, 44)
	w.fs.workers = append(w.fs.workers, workerFor(legacy.SessionID(), 503))
	w.gh.closedPRs[44] = true
	w.accept(t, 45) // no worker, no round

	for range 2 {
		if err := w.h.tick(); err != nil {
			t.Fatal(err)
		}
	}
	want := []string{"stop " + merged.RoundSessionID()[:8], "stop " + legacy.SessionID()[:8]}
	if got := slices.DeleteFunc(slices.Clone(w.fs.events), func(e string) bool { return !strings.HasPrefix(e, "stop ") }); !slices.Equal(got, want) {
		t.Errorf("stopped %q, want %q", got, want)
	}
	if unread, _ := w.h.ledger.UnreadRounds(); len(unread) != 0 {
		t.Errorf("the closed PR's round is still unread: %+v", unread)
	}
	if !slices.Contains(w.elly.deleted, merged.URL()) {
		t.Errorf("the closed PR was not cleared in elly: %q", w.elly.deleted)
	}
	if w.gh.asked[42] != 1 || w.gh.asked[44] != 1 || w.gh.asked[45] != 0 {
		t.Errorf("GitHub asked %v: once per closed PR with a worker, never for one without", w.gh.asked)
	}
}

// A worker on a closed PR holds no slot: the owed round elsewhere starts in
// the same tick.
func TestTickClosedPRFreesItsSlot(t *testing.T) {
	w := newTickWorld(t)
	w.gh.closedPRs = map[int]bool{}
	w.gh.facts = RoundFacts{State: "open", Head: "h1"}
	w.h.tcfg.MaxRunning = 1
	w.closedWithWorker(t, 42, 501)
	owed := w.accept(t, 43)
	w.elly.prs = []EllyPR{requestedPR(43)}
	if err := w.h.tick(); err != nil {
		t.Fatal(err)
	}
	if len(w.started) != 1 || !slices.Contains(w.started[0], owed.RoundSessionID()) {
		t.Errorf("started %q, want the owed round on #43", w.started)
	}
}

// Unknown is hands off: an unreadable roster, a PR GitHub does not answer
// for, a PR a click holds, and a dry run each stop nothing and leave the
// round for a later tick.
func TestTickSweepLeavesWhatItCannotRead(t *testing.T) {
	for name, set := range map[string]func(w *tickWorld, pr PR){
		"roster":  func(w *tickWorld, _ PR) { w.fs.unreadable = true },
		"github":  func(w *tickWorld, _ PR) { w.gh.baseErr = map[int]error{42: errors.New("502")} },
		"locked":  func(w *tickWorld, pr PR) { w.locked[pr.SessionID()] = true },
		"dry run": func(w *tickWorld, _ PR) { w.h.dry = true },
	} {
		w := newTickWorld(t)
		w.gh.closedPRs = map[int]bool{}
		w.gh.facts = RoundFacts{State: "open", Head: "h1"}
		pr := w.closedWithWorker(t, 42, 501)
		set(w, pr)
		_ = w.h.tick()
		if slices.ContainsFunc(w.fs.events, func(e string) bool { return strings.HasPrefix(e, "stop ") }) {
			t.Errorf("%s: stopped a worker: %q", name, w.fs.events)
		}
		if unread, _ := w.h.ledger.UnreadRounds(); len(unread) != 1 {
			t.Errorf("%s: the round was settled: %+v", name, unread)
		}
		if name == "dry run" && !strings.Contains(w.out.String(), "would stop worker "+pr.RoundSessionID()[:8]+" on https://github.com/acme/widgets/pull/42") {
			t.Errorf("dry run said %q", w.out.String())
		}
	}
}

// Every state that needs the user says so once on the desktop, whichever
// path set it, and a tick that finds it unchanged stays quiet.
func TestTickNotifiesEachNewNeedsYouOnce(t *testing.T) {
	w := newTickWorld(t)
	asked := w.accept(t, 1)
	if _, err := w.h.ledger.StartRound(asked, "h1", "new_head", t0); err != nil {
		t.Fatal(err)
	}
	w.msgs[asked.RoundSessionID()] = "Which one?\nclaude-review: question for the user"
	w.gh.facts = RoundFacts{State: "open", Head: "h1"}
	gated := w.accept(t, 42)
	w.gh.descends = false
	w.elly.prs = []EllyPR{requestedPR(42)}
	for range 2 {
		if err := w.h.tick(); err != nil {
			t.Fatal(err)
		}
	}
	want := []string{
		asked.URL() + " needs you: the round has a question for you: Which one?",
		gated.URL() + " needs you: " + w.elly.put[gated.URL()].Note,
	}
	slices.Sort(want)
	got := slices.Sorted(slices.Values(w.notified))
	if !slices.Equal(got, want) {
		t.Errorf("notified %q, want %q", got, want)
	}
}

// An unattended tick's journal says what it did, not only what failed.
func TestTickLogsWhatItDoes(t *testing.T) {
	w := newTickWorld(t)
	w.accept(t, 42)
	w.elly.prs = []EllyPR{requestedPR(42)}
	if err := w.h.tick(); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(w.out.String(), "start a round on https://github.com/acme/widgets/pull/42") {
		t.Errorf("journal %q names no started round", w.out)
	}
}
