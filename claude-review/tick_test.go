package main

import (
	"bytes"
	"errors"
	"net/url"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/chelmertz/dotfiles/claude-review/ids"
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
	delErr  error
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
		return ellyRefused{Method: "PUT", URL: u, Status: 400, Text: "400 Bad Request"}
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
func (e *fakeElly) Delete(u string) error {
	if e.delErr != nil {
		return e.delErr
	}
	e.deleted = append(e.deleted, u)
	return nil
}

type fakeTickGitHub struct {
	fakeGate
	facts      RoundFacts
	state      string
	head       string
	user       *Review
	dismissed  []ids.ReviewID
	dismissErr error
	posted     bool
	// repliedSince is the start of the last window RepliedInRoundThread
	// was asked about.
	repliedSince time.Time
	standings    int // Standing calls
	// status answers DismissalStatus; statusErr fails it.
	status    DismissalStatus
	statusErr error
}

func (g *fakeTickGitHub) DismissalStatus(PR, ids.ReviewID) (DismissalStatus, error) {
	return g.status, g.statusErr
}

func (g *fakeTickGitHub) RepliedInRoundThread(_ PR, since, _ time.Time) (bool, error) {
	g.repliedSince = since
	return g.posted, nil
}

func (g *fakeTickGitHub) RoundFacts(PR, Round) (RoundFacts, error) { return g.facts, nil }
func (g *fakeTickGitHub) Standing(PR) (string, string, *Review, error) {
	g.standings++
	return g.state, g.head, g.user, nil
}
func (g *fakeTickGitHub) Dismiss(_ PR, id ids.ReviewID, _ string) error {
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
	live     map[ids.SessionID]bool
	blocked  map[ids.SessionID]string
	msgs     map[ids.SessionID]string
	out      *bytes.Buffer
	fs       *fakeSessions
	fresh    map[ids.SessionID]bool // round sessions with no transcript yet
	locked   map[string]bool
	trusted  []string
	pid      int
	notified []string
}

func newTickWorld(t *testing.T) *tickWorld {
	w := &tickWorld{
		elly:    &fakeElly{login: "me", put: map[string]Annotation{}},
		gh:      &fakeTickGitHub{fakeGate: fakeGate{descends: true, base: "main"}, state: "open", head: "h1"},
		live:    map[ids.SessionID]bool{},
		blocked: map[ids.SessionID]string{},
		msgs:    map[ids.SessionID]string{},
		out:     &bytes.Buffer{},
		fs:      newFakeSessions(),
		fresh:   map[ids.SessionID]bool{},
		locked:  map[string]bool{},
		pid:     1000,
	}
	w.h = tickHost{
		tcfg:    TickConfig{ReviewRepos: []string{"acme/*"}, User: "me", DailyCap: 6, MaxRunning: 2, TotalDailyCap: 24, Wrapper: []string{"capped"}},
		ledger:  openTestLedger(t),
		elly:    w.elly,
		gh:      w.gh,
		running: func(id ids.SessionID) bool { return w.live[id] },
		blocked: func(id ids.SessionID) (string, bool) {
			why, ok := w.blocked[id]
			return why, ok
		},
		lastMessage: func(id ids.SessionID) (string, error) {
			w.fs.events = append(w.fs.events, "read "+id.Key().String())
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
			id := mustSession(argv[slices.Index(argv, "--resume")+1])
			w.pid++
			w.fs.workers = append(w.fs.workers, workerFor(id, w.pid))
			if _, ok := w.fs.saved[id]; !ok && len(argv) > 6 {
				w.fs.saved[id] = savedFrom(argv)
			}
			return nil
		},
		exists: func(id ids.SessionID) bool { return !w.fresh[id] },
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
	w.fs.saved = map[ids.SessionID][]string{}
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
		p.Asked = askedAs(askedRequested)
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
	if _, err := w.h.ledger.StartRound(failed, "h1", NewHead{}, t0); err != nil {
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
	tok, err := ids.ParseToken(u.Query().Get("token"))
	if err != nil {
		t.Fatal(err)
	}
	if ok, _ := w.h.ledger.UseToken(pr, u.Query().Get("at"), tok); !ok {
		t.Errorf("the Approve link's token is not one the ledger minted: %s", a.Links[0].URL)
	}
	if want := []string{pr.URL() + " needs you: LGTM at aaaaaaaaa; approve it if you agree"}; !slices.Equal(w.notified, want) {
		t.Errorf("notified %q, want %q", w.notified, want)
	}
}

func TestTickFindingsAfterApprovalDismiss(t *testing.T) {
	w := newTickWorld(t)
	pr := w.accept(t, 42)
	if _, err := w.h.ledger.StartRound(pr, "h1", NewHead{}, t0); err != nil {
		t.Fatal(err)
	}
	w.gh.facts = RoundFacts{State: "open", Head: "h1", NewThreads: 2, UserReview: &Review{ID: reviewID(7), State: "APPROVED", CommitID: "h0"}}
	if err := w.h.tick(); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(w.gh.dismissed, []ids.ReviewID{reviewID(7)}) {
		t.Errorf("dismissed %v", w.gh.dismissed)
	}
	if a := w.elly.put[pr.URL()]; a.State != stateNeedsYou {
		t.Errorf("elly got %+v", a)
	}
}

func TestTickRefreshDropsOnlyOnEvidence(t *testing.T) {
	w := newTickWorld(t)
	pr := w.accept(t, 42)
	if err := w.h.ledger.SetPRState(pr, PRState{State: stAwaiting, Kind: kindDismiss}); err != nil {
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
	if _, err := w.h.ledger.StartRound(failed, "h1", NewHead{}, t0); err != nil {
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
	if len(w.started) != 1 || !slices.Contains(w.started[0], failed.RoundSessionID().String()) {
		t.Errorf("with one slot, the retry must take it: started %q", w.started)
	}
}

func TestTickOneEllyFailureDoesNotStopTheRest(t *testing.T) {
	w := newTickWorld(t)
	for n := 1; n <= 2; n++ {
		pr := w.accept(t, n)
		if err := w.h.ledger.SetPRState(pr, PRState{State: stAwaiting, Kind: kindDismiss}); err != nil {
			t.Fatal(err)
		}
	}
	w.elly.putErr = ellyRefused{Method: "PUT", Status: 404, Text: "404 Not Found"}
	if err := w.h.tick(); err != nil {
		t.Fatalf("one PR elly does not list failed the whole tick: %v", err)
	}
	if strings.Count(w.out.String(), "404") != 2 {
		t.Errorf("want both failures reported: %q", w.out.String())
	}
	// Nothing elly did not take is recorded or notified: the rows go out,
	// and notify, once elly lists the PRs.
	if all, _ := w.h.ledger.Annotations(); len(all) != 0 || len(w.notified) != 0 {
		t.Errorf("a row elly refused was recorded %v or notified %q", all, w.notified)
	}
	w.elly.putErr = nil
	if err := w.h.tick(); err != nil {
		t.Fatal(err)
	}
	if all, _ := w.h.ledger.Annotations(); len(all) != 2 || len(w.notified) != 2 {
		t.Errorf("once listed: recorded %d, notified %q", len(all), w.notified)
	}
}

func TestLaunchRefusesInADryRun(t *testing.T) {
	w := newTickWorld(t)
	w.h.dry = true
	if _, err := w.h.launch(widgetsPR, nil, func(time.Time) error { return nil }, nil); err == nil || len(w.started) != 0 {
		t.Errorf("launch in a dry run: err %v, started %q", err, w.started)
	}
}

// A blocked round waits for the user, on a permission it lacks or on a
// question it asks; the note carries the round's own words for which. A
// retry would resume on top of the live, blocked worker.
func TestTickABlockedRoundNeedsTheUser(t *testing.T) {
	for why, want := range map[string]string{
		"Does the ignore step print \"proceeding with build\"?": "Claude has a question for you: Does the ignore step print \"proceeding with build\"?",
		"": "Claude has a question for you",
	} {
		w := newTickWorld(t)
		pr := w.accept(t, 1)
		if _, err := w.h.ledger.StartRound(pr, "h1", NewHead{}, t0); err != nil {
			t.Fatal(err)
		}
		w.gh.facts = RoundFacts{State: "open", Head: "h1"}
		w.blocked[pr.RoundSessionID()] = why
		if err := w.h.tick(); err != nil {
			t.Fatal(err)
		}
		if len(w.started) != 0 {
			t.Errorf("a blocked round was retried: %q", w.started)
		}
		all, _ := w.h.ledger.Annotations()
		a := all[pr.URL()]
		// The same row as a round that ended on the question line: elly's
		// button is the Primary link, so it reads Answer and opens the session.
		if a.State != stateNeedsYou || a.Note != want || len(a.Links) == 0 || a.Links[0] != primary(answerLink(pr)) || slices.Contains(a.Links, forkLink(pr)) {
			t.Errorf("blocked on %q: annotation %+v", why, a)
		}
	}
}

// A push after the round started overtakes its question: the waiting worker
// is stopped and the round read, so the new head gets a round of its own,
// which asks again if the question still holds. Left alone, the blocked
// worker held the PR with no round on the new commits (2026-11-16).
func TestTickABlockedRoundOvertakenByAPushIsStopped(t *testing.T) {
	w := newTickWorld(t)
	pr := w.accept(t, 1)
	if _, err := w.h.ledger.StartRound(pr, "h1", NewHead{}, t0); err != nil {
		t.Fatal(err)
	}
	w.gh.facts = RoundFacts{State: "open", Head: "h2"}
	w.gh.state, w.gh.head = prOpen, "h2"
	w.fs.workers = append(w.fs.workers, workerFor(pr.RoundSessionID(), 601))
	w.fs.states[pr.RoundSessionID()] = "blocked"
	w.blocked[pr.RoundSessionID()] = "does the query return rows?"
	if err := w.h.tick(); err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(w.fs.events, "stop "+pr.RoundSessionID().Key().String()) {
		t.Errorf("the overtaken worker was not stopped: %q", w.fs.events)
	}
	if unread, _ := w.h.ledger.UnreadRounds(); slices.ContainsFunc(unread, func(r Round) bool { return r.Head == "h1" }) {
		t.Errorf("the overtaken round is still unread: %+v", unread)
	}
	all, _ := w.h.ledger.Annotations()
	if a, ok := all[pr.URL()]; ok && a.State == stateNeedsYou {
		t.Errorf("the overtaken question still waits on the user: %+v", a)
	}
}

// A cap trip clears itself once the last 24 hours hold fewer starts than
// the cap; a second cap trip within a week of that latches, since a loop
// would trip it every day, and only a reset reopens it.
func TestTickBreakerHealsOnceThenLatches(t *testing.T) {
	w := newTickWorld(t)
	w.h.tcfg.TotalDailyCap = 2
	w.h.tcfg.MaxRunning = 10
	for n := 1; n <= 5; n++ {
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
	keepLive := func(upTo int) {
		for n := 1; n <= upTo; n++ {
			w.live[PR{Owner: "acme", Repo: "widgets", Number: n}.RoundSessionID()] = true
		}
	}
	keepLive(2)
	// Still inside the 24 hours: tripped, and the owed PR says why.
	w.h.now = func() time.Time { return t0.Add(time.Hour) }
	if err := w.h.tick(); err == nil {
		t.Error("the breaker closed inside the window")
	}
	if a, _ := w.h.ledger.Annotations(); a[PR{Owner: "acme", Repo: "widgets", Number: 3}.URL()].Note != "paused: too many rounds started in 24 hours, the limit across all PRs; claude-review starts again once fewer than the limit started in the last 24 hours" {
		t.Errorf("an owed PR does not say why it waits: %+v", a)
	}

	// The window moved on: the cap trip clears itself and rounds start.
	w.h.now = func() time.Time { return t0.Add(25 * time.Hour) }
	_ = w.h.tick()
	if len(w.started) != 4 {
		t.Fatalf("after the window: started %d, want 4\n%s", len(w.started), w.out)
	}
	keepLive(4)
	// It tripped again within a week of healing: latched.
	w.h.now = func() time.Time { return t0.Add(50 * time.Hour) }
	if err := w.h.tick(); err == nil {
		t.Error("a second cap trip within a week healed")
	}
	if len(w.started) != 4 {
		t.Errorf("a round started while latched: %d", len(w.started))
	}
	if !slices.ContainsFunc(w.notified, func(m string) bool { return strings.Contains(m, "twice in a week") }) {
		t.Errorf("the latch was not notified: %q", w.notified)
	}
	if a, _ := w.h.ledger.Annotations(); !strings.Contains(a[PR{Owner: "acme", Repo: "widgets", Number: 5}.URL()].Note, "until it is reset") {
		t.Errorf("a latched breaker does not ask for the reset: %+v", a)
	}

	if err := w.h.ledger.Reset(w.h.now()); err != nil {
		t.Fatal(err)
	}
	if err := w.h.tick(); err != nil {
		t.Fatal(err)
	}
	if len(w.started) != 5 {
		t.Errorf("after a reset: started %d", len(w.started))
	}
}

// Faults other than the cap never clear themselves.
func TestTickBreakerFaultsStayLatched(t *testing.T) {
	for _, reason := range []string{breakerDuplicate + "two workers", breakerSelfCheck + "it forked"} {
		w := newTickWorld(t)
		w.accept(t, 1)
		w.elly.prs = []EllyPR{requestedPR(1)}
		if err := w.h.ledger.Trip(reason); err != nil {
			t.Fatal(err)
		}
		w.h.now = func() time.Time { return t0.Add(48 * time.Hour) }
		if err := w.h.tick(); err == nil || len(w.started) != 0 {
			t.Errorf("%s: err %v, started %d", reason, err, len(w.started))
		}
	}
}

func TestTickBreakerCountsRetries(t *testing.T) {
	w := newTickWorld(t)
	failed := w.accept(t, 1)
	if _, err := w.h.ledger.StartRound(failed, "h1", NewHead{}, t0); err != nil {
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
	if _, err := w.h.launch(widgetsPR, nil, func(time.Time) error { return nil }, nil); err == nil || len(w.started) != 0 {
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
	if _, err := w.h.launch(widgetsPR, nil, func(time.Time) error { recorded = true; return nil }, nil); err == nil || recorded || len(w.started) != 0 {
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
	if st, _ := w.h.stateOf(pr); st.State != stStuck || st.Kind != kindRefused {
		t.Errorf("the PR is not held back until the next click: %+v", st)
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
	if a := w.elly.put[pr.URL()]; a.State != stateNeedsYou || a.Note != launchNotes[launchUnlisted] || strings.Contains(a.Note, "no worker was listed") {
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
		w.fs.workers = append(w.fs.workers, Worker{Short: forkID.Key(), SessionID: forkID, PID: 900, Fork: true, LaunchID: pr.RoundSessionID()})
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
	if _, err := w.h.ledger.StartRound(other, "h1", NewHead{}, t0); err != nil {
		t.Fatal(err)
	}
	w.gh.facts = RoundFacts{State: "open", Head: "h1", NewThreads: 1}
	w.elly.prs = []EllyPR{requestedPR(42)}
	w.locked[pr.SessionID().String()] = true
	w.locked[other.SessionID().String()] = true
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
	if _, err := w.h.ledger.StartRound(pr, "h0", Click{At: t0}, t0); err != nil {
		t.Fatal(err)
	}
	w.fs.workers = []Worker{workerFor(pr.RoundSessionID(), 500)}
	w.fs.states[pr.RoundSessionID()] = "done"
	w.gh.facts = RoundFacts{State: "open", Head: "h1", NewThreads: 1}
	w.elly.prs = []EllyPR{requestedPR(42)}
	calls := 0
	w.h.running = func(ids.SessionID) bool { calls++; return calls == 1 } // still running when the settle pass looks
	if err := w.h.tick(); err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(w.fs.events, ","); got != "read "+pr.RoundSessionID().Key().String()+",stop "+pr.RoundSessionID().Key().String() {
		t.Errorf("events %s", got)
	}
	rounds, _ := w.h.ledger.UnreadRounds()
	if len(w.started) != 1 || len(rounds) != 1 || rounds[0].Trigger.Kind() == trigClick {
		t.Errorf("started %d, unread %+v", len(w.started), rounds)
	}
}

const windowNoteWant = "Claude window open; close it to let reviews run"

// A legacy window holds the PR's rounds back; tick says so in elly and
// clears it on the first tick that sees the window gone.
func TestTickNamesALegacyWindowAndClearsIt(t *testing.T) {
	w := newTickWorld(t)
	pr := w.accept(t, 42)
	w.elly.prs = []EllyPR{requestedPR(42)}
	w.fs.window = 4242
	w.h.running = func(ids.SessionID) bool { return w.fs.window > 0 }
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
	if a, _ := w.h.ledger.Annotations(); a[pr.URL()].State != stateNeedsYou || a[pr.URL()].Note != launchNotes[launchNotStarted] {
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
	if _, err := w.h.ledger.StartRound(pr, "h1", Click{At: t0, From: "elly"}, t0); err != nil {
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
	if _, err := w.h.ledger.StartRound(merged, "h0", NewHead{}, t0); err != nil {
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
	want := []string{"stop " + merged.RoundSessionID().Key().String(), "stop " + legacy.SessionID().Key().String()}
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
	if len(w.started) != 1 || !slices.Contains(w.started[0], owed.RoundSessionID().String()) {
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
		"locked":  func(w *tickWorld, pr PR) { w.locked[pr.SessionID().String()] = true },
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
		if name == "dry run" && !strings.Contains(w.out.String(), "would stop worker "+pr.RoundSessionID().Key().String()+" on https://github.com/acme/widgets/pull/42") {
			t.Errorf("dry run said %q", w.out.String())
		}
	}
}

// Every state that needs the user says so once on the desktop, whichever
// path set it, and a tick that finds it unchanged stays quiet.
func TestTickNotifiesEachNewNeedsYouOnce(t *testing.T) {
	w := newTickWorld(t)
	asked := w.accept(t, 1)
	if _, err := w.h.ledger.StartRound(asked, "h1", NewHead{}, t0); err != nil {
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
		asked.URL() + " needs you: Claude has a question for you: Which one?",
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

// Start is offered on elly's row for a review asked of the user by name in
// review_repos that tick would not run on its own: not accepted, not
// ignored, not the user's own, with nothing claude-review shows already.
func TestTickOffersStartWhereAClickIsWhatIsMissing(t *testing.T) {
	w := newTickWorld(t)
	mine := requestedPR(4)
	mine.Author = "me"
	rereview := requestedPR(8)
	unasked := requestedPR(2)
	unasked.Asked = askedAs(askedNotAsked)
	elsewhere := requestedPR(3)
	elsewhere.Url = "https://github.com/other/x/pull/3"
	team := requestedPR(9)
	w.elly.prs = []EllyPR{requestedPR(1), unasked, elsewhere, mine, requestedPR(5), requestedPR(6), requestedPR(7), rereview, team}
	w.accept(t, 5)
	w.elly.prs[5].Buried = true
	if err := w.h.ledger.SetPRState(PR{Owner: "acme", Repo: "widgets", Number: 7}, PRState{State: stAwaiting, Kind: kindQuestion, Detail: "a question"}); err != nil {
		t.Fatal(err)
	}
	if err := w.h.tick(); err != nil {
		t.Fatal(err)
	}
	var offered []string
	for u, a := range w.elly.put {
		if a.State == stateInfo {
			offered = append(offered, u)
			want := []Link{{Label: "Start", URL: "claude-review://acme/widgets/" + u[strings.LastIndex(u, "/")+1:] + "?from=elly", Primary: true, Action: actionStart}}
			if !slices.Equal(a.Links, want) {
				t.Errorf("%s: links %+v, want %+v", u, a.Links, want)
			}
			if got := w.elly.expires[u].Sub(w.h.now()); got != offerTTL {
				t.Errorf("%s: offer lives %v, want %v", u, got, offerTTL)
			}
		}
	}
	slices.Sort(offered)
	if want := []string{"https://github.com/acme/widgets/pull/1", "https://github.com/acme/widgets/pull/8", "https://github.com/acme/widgets/pull/9"}; !slices.Equal(offered, want) {
		t.Errorf("offered %v, want %v", offered, want)
	}
	// only the accepted #5 runs; an offer starts nothing
	if len(w.started) != 1 {
		t.Errorf("started %d rounds, want 1", len(w.started))
	}
}

func TestTickDryRunOffersNothing(t *testing.T) {
	w := newTickWorld(t)
	w.elly.prs = []EllyPR{requestedPR(1)}
	w.h.dry = true
	if err := w.h.tick(); err != nil {
		t.Fatal(err)
	}
	if len(w.elly.put) != 0 || !strings.Contains(w.out.String(), "would offer Start on https://github.com/acme/widgets/pull/1") {
		t.Errorf("put %v, out %s", w.elly.put, w.out)
	}
}

// A PR a click holds gets no offer: the click writes its own "starting".
func TestTickOffersNothingOnAPRAClickHolds(t *testing.T) {
	w := newTickWorld(t)
	w.elly.prs = []EllyPR{requestedPR(1)}
	w.locked[PR{Owner: "acme", Repo: "widgets", Number: 1}.SessionID().String()] = true
	if err := w.h.tick(); err != nil {
		t.Fatal(err)
	}
	if len(w.elly.put) != 0 {
		t.Errorf("offered under a click: %v", w.elly.put)
	}
}

// A real tick writes why it leaves each PR to the journal, not only a dry
// run: "no slot" and "never clicked" otherwise read as tick finding nothing.
func TestTickLogsWhyItLeavesEachPR(t *testing.T) {
	w := newTickWorld(t)
	w.h.tcfg.MaxRunning = 1
	for n := 1; n <= 2; n++ {
		w.accept(t, n)
		w.elly.prs = append(w.elly.prs, requestedPR(n))
	}
	w.elly.prs = append(w.elly.prs, requestedPR(3))
	if err := w.h.tick(); err != nil {
		t.Fatal(err)
	}
	out := w.out.String()
	if want := "leave https://github.com/acme/widgets/pull/2: " + string(skipNoSlot); !strings.Contains(out, want) {
		t.Errorf("missing %q in:\n%s", want, out)
	}
	// The routine answers stay out of a real run's journal: one line per
	// PR every tick was most of its volume.
	for _, quiet := range []string{string(skipNotAccepted), "send https://", "link https://"} {
		if strings.Contains(out, quiet) {
			t.Errorf("a real run logged %q:\n%s", quiet, out)
		}
	}
	w.h.dry = true
	w.out.Reset()
	if err := w.h.tick(); err != nil {
		t.Fatal(err)
	}
	if want := "would leave https://github.com/acme/widgets/pull/3: " + string(skipNotAccepted); !strings.Contains(w.out.String(), want) {
		t.Errorf("a dry run lost %q:\n%s", want, w.out)
	}
}

// tick holds rounds while a fork is open under its recorded id, and lets
// them go once it closes.
func TestTickSeesAForkWindow(t *testing.T) {
	w := newTickWorld(t)
	pr := w.accept(t, 42)
	w.elly.prs = []EllyPR{requestedPR(42)}
	fid := ids.NewSession()
	if err := w.h.ledger.SetForkSession(pr, fid); err != nil {
		t.Fatal(err)
	}
	w.fs.window, w.fs.windowID = 4242, fid
	if err := w.h.tick(); err != nil {
		t.Fatal(err)
	}
	if len(w.started) != 0 {
		t.Errorf("started beside the fork: %q", w.started)
	}
	if s, _, _ := w.h.ledger.PRState(pr); s.State != stHeld || s.Kind != kindWindow {
		t.Errorf("state %+v, want held{window}", s)
	}
	w.fs.window = 0
	if err := w.h.tick(); err != nil {
		t.Fatal(err)
	}
	if s, _, _ := w.h.ledger.PRState(pr); s.State == stHeld && s.Kind == kindWindow {
		t.Errorf("held{window} outlived the fork: %+v", s)
	}
}

// The user's answer to a question is a reply to that round: Claude goes on
// with a round, not a cleared row nobody follows up.
func TestTickAnswerGetsAFollowUpRound(t *testing.T) {
	w := newTickWorld(t)
	pr := w.accept(t, 42)
	id, err := w.h.ledger.StartRound(pr, "h1", NewHead{}, time.Now().Add(-time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if err := w.h.ledger.MarkRead(id); err != nil {
		t.Fatal(err)
	}
	q := decide(Round{Head: "h1"}, Outcome{Kind: OutcomeQuestion}, RoundFacts{State: prOpen, Head: "h1"}, pr)
	if err := w.h.ledger.SetPRState(pr, *nextOf(q)); err != nil {
		t.Fatal(err)
	}
	w.elly.prs = []EllyPR{ellyPR(func(p *EllyPR) { p.Url = pr.URL() })}
	if err := w.h.tick(); err != nil {
		t.Fatal(err)
	}
	if s, _, _ := w.h.ledger.PRState(pr); len(w.started) != 0 || s.Kind != kindQuestion {
		t.Fatalf("before the answer: started %d, state %+v", len(w.started), s)
	}
	w.gh.posted = true
	// Under the daily cap the question stays, and the answer is seen again.
	w.h.tcfg.DailyCap = 1
	if err := w.h.tick(); err != nil {
		t.Fatal(err)
	}
	if s, _, _ := w.h.ledger.PRState(pr); len(w.started) != 0 || s.State != stAwaiting || s.Kind != kindQuestion {
		t.Fatalf("under the cap: started %d, state %+v", len(w.started), s)
	}
	w.h.tcfg.DailyCap = 6
	for range 2 {
		if err := w.h.tick(); err != nil {
			t.Fatal(err)
		}
	}
	if len(w.started) != 1 {
		t.Errorf("the answer got %d rounds, want 1", len(w.started))
	}
}

// A round is settled under the session it started in: a policyVersion bump
// names a new session for the next round, not for one still running.
func TestTickSettlesARoundUnderItsOwnSession(t *testing.T) {
	w := newTickWorld(t)
	pr := w.accept(t, 42)
	old := ids.SessionFor(pr.URL() + " round-policy old")
	id, err := w.h.ledger.StartRound(pr, "h1", NewHead{}, time.Now().Add(-time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.h.ledger.db.Exec(`update rounds set session = ? where id = ?`, old.String(), id); err != nil {
		t.Fatal(err)
	}
	if err := w.h.ledger.SetPRState(pr, PRState{State: stRunning, Kind: kindTick, Head: "h1"}); err != nil {
		t.Fatal(err)
	}
	w.h.running = func(s ids.SessionID) bool { return s == old }
	if err := w.h.tick(); err != nil {
		t.Fatal(err)
	}
	unread, _ := w.h.ledger.UnreadRounds()
	if s, _, _ := w.h.ledger.PRState(pr); len(unread) != 1 || s.State != stRunning {
		t.Errorf("a running round under the old policy's session looked ended: unread %d, state %+v", len(unread), s)
	}
}

// An elly that sends no Asked (older, or rolled back) cannot say who is
// asked: tick plans and offers nothing, notifies once however many passes
// it lasts, and plans again as soon as the field comes back.
func TestTickPlansNothingWithoutElllysAsked(t *testing.T) {
	w := newTickWorld(t)
	w.accept(t, 1)
	pr := requestedPR(1)
	pr.Asked = nil
	offer := requestedPR(2)
	offer.Asked = nil
	w.elly.prs = []EllyPR{pr, offer}
	for range 2 {
		if err := w.h.tick(); err != nil {
			t.Fatal(err)
		}
	}
	if len(w.started) != 0 {
		t.Errorf("planned without Asked: %q", w.started)
	}
	for u, a := range w.elly.put {
		if a.State == stateInfo {
			t.Errorf("offered Start on %s without Asked", u)
		}
	}
	if n := len(slices.DeleteFunc(slices.Clone(w.notified), func(m string) bool { return !strings.Contains(m, "who is asked") })); n != 1 {
		t.Errorf("notified %d times, want once: %q", n, w.notified)
	}
	w.elly.prs = []EllyPR{requestedPR(1)}
	if err := w.h.tick(); err != nil {
		t.Fatal(err)
	}
	if len(w.started) != 1 {
		t.Errorf("with Asked back: started %d, want 1", len(w.started))
	}
}

// A row elly refuses as invalid is a bug here: the row it last took no
// longer says what is true, so it comes down, and the user hears once that
// a row is hidden, however many passes the refusal lasts.
func TestARowEllyRefusesTakesTheOldOneDownAndSaysSoOnce(t *testing.T) {
	w := newTickWorld(t)
	pr := w.accept(t, 42)
	if err := w.h.move2(pr, PRState{State: stHandled, Kind: kindPosted, Head: "h1"}); err != nil {
		t.Fatal(err)
	}
	if _, had, _ := w.h.ledger.Annotation(pr); !had {
		t.Fatal("setup: the handled row is not up")
	}
	if err := w.h.ledger.SetPRState(pr, PRState{State: stAwaiting, Kind: kindDrafts, Detail: "2", Head: "h1"}); err != nil {
		t.Fatal(err)
	}
	w.elly.reject = stateNeedsYou
	for range 2 {
		if err := w.h.tick(); err != nil {
			t.Fatal(err)
		}
	}
	if !slices.Contains(w.elly.deleted, pr.URL()) {
		t.Errorf("the stale handled row stayed up: deleted %q", w.elly.deleted)
	}
	if _, had, _ := w.h.ledger.Annotation(pr); had {
		t.Error("the refused row is recorded as up")
	}
	hidden := slices.DeleteFunc(slices.Clone(w.notified), func(m string) bool { return !strings.Contains(m, "is hidden") })
	if len(hidden) != 1 {
		t.Errorf("hidden notices %q, want one", hidden)
	}
	for _, m := range w.notified {
		if strings.Contains(m, "unsent comments") {
			t.Errorf("notified a row elly never showed: %q", m)
		}
	}
}

// Opening and closing Open Claude's window on an unanswered question tells
// the user nothing new: the window keeps the identity of the question it
// stands over. The same question asked again after an answer notifies.
func TestAWindowOverAQuestionDoesNotNotifyAgain(t *testing.T) {
	w := newTickWorld(t)
	pr := w.accept(t, 42)
	question := PRState{State: stAwaiting, Kind: kindQuestion, Detail: "which table?", Head: "h1"}
	if err := w.h.move2(pr, question); err != nil {
		t.Fatal(err)
	}
	if len(w.notified) != 1 {
		t.Fatalf("the question notified %d times", len(w.notified))
	}
	if _, err := w.h.move(pr, Event{Kind: evWindowOpened}); err != nil {
		t.Fatal(err)
	}
	if _, err := w.h.move(pr, Event{Kind: evWindowClosed, Head: "h1"}); err != nil {
		t.Fatal(err)
	}
	if len(w.notified) != 1 {
		t.Errorf("a window over the question notified again: %q", w.notified)
	}
	if err := w.h.move2(pr, PRState{State: stHandled, Kind: kindPosted, Head: "h1"}); err != nil {
		t.Fatal(err)
	}
	if err := w.h.move2(pr, question); err != nil {
		t.Fatal(err)
	}
	if len(w.notified) != 2 {
		t.Errorf("the question asked again after it left: notified %q", w.notified)
	}
}

// Rows up before the identity was stored are seeded from the copy, so the
// deploy that brings it notifies nothing already shown.
func TestTheNotifyIdentityIsSeededFromRowsAlreadyUp(t *testing.T) {
	w := newTickWorld(t)
	pr := w.accept(t, 42)
	s := PRState{State: stAwaiting, Kind: kindDrafts, Detail: "2", Head: "h1"}
	if err := w.h.ledger.SetPRState(pr, s); err != nil {
		t.Fatal(err)
	}
	if err := w.h.ledger.SetAnnotation(pr, render(pr, s, View{})); err != nil {
		t.Fatal(err)
	}
	if err := w.h.show(pr, s, Hold{}); err != nil {
		t.Fatal(err)
	}
	if len(w.notified) != 0 {
		t.Errorf("a row already up notified on rollout: %q", w.notified)
	}
}
