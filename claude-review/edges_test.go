package main

// Edges a mutation run found unpinned: each test here failed with the
// mutant gremlins reported surviving, and passes on the real code.

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

// With no round yet, the head the user accepted is the last one looked
// at: a PR still at that head owes nothing.
func TestPlanTickAcceptedHeadIsTheLastLook(t *testing.T) {
	cfg := TickConfig{ReviewRepos: []string{"acme/*"}, User: "me", DailyCap: 3}
	l := openTestLedger(t)
	if err := l.Accept(widgetsPR, "h1", "main", t0); err != nil {
		t.Fatal(err)
	}
	got, err := planTick([]EllyPR{ellyPR(nil)}, cfg, l, fakeGate{descends: true, base: "main"}, t0)
	if err != nil || len(got) != 0 {
		t.Errorf("a PR at the accepted head, nothing asked: %+v %v", got, err)
	}
}

// The per-PR cap counts the last 24 hours, not only rounds started this
// instant, and TickConfig's zero cap is no cap.
func TestPlanTickDailyCapWindow(t *testing.T) {
	requested := ellyPR(func(p *EllyPR) { p.ReviewRequestedFromUsers = []string{"me"} })
	ok := fakeGate{descends: true, base: "main"}
	l := openTestLedger(t)
	if err := l.Accept(widgetsPR, "h0", "main", t0); err != nil {
		t.Fatal(err)
	}
	if _, err := l.StartRound(widgetsPR, "hx", "x", t0.Add(-25*time.Hour)); err != nil {
		t.Fatal(err)
	}
	if _, err := l.StartRound(widgetsPR, "hx", "x", t0.Add(-time.Hour)); err != nil {
		t.Fatal(err)
	}
	gate := func(dailyCap int) string {
		t.Helper()
		got, err := planTick([]EllyPR{requested}, TickConfig{ReviewRepos: []string{"acme/*"}, User: "me", DailyCap: dailyCap}, l, ok, t0)
		if err != nil || len(got) != 1 {
			t.Fatalf("cap %d: %+v %v", dailyCap, got, err)
		}
		return got[0].Gate
	}
	if g := gate(1); g != gatePaused {
		t.Errorf("a round an hour ago reaches a cap of 1: gate %q", g)
	}
	if g := gate(2); g != "" {
		t.Errorf("a round 25 hours ago counted toward the cap: gate %q", g)
	}
	if g := gate(0); g != "" {
		t.Errorf("a zero cap paused the PR: gate %q", g)
	}
}

func TestEllyClientTimesOut(t *testing.T) {
	if got := (Elly{}).client().Timeout; got != 10*time.Second {
		t.Errorf("timeout %v: a hung elly would hang tick", got)
	}
}

func TestGitHubRoundFactsWithoutApproval(t *testing.T) {
	g := &routeGh{routes: map[string]string{
		"repos/acme/widgets/pulls/42":          `{"state":"open","head":{"sha":"h2"},"base":{"ref":"main"}}`,
		"repos/acme/widgets/pulls/42/reviews":  `[]`,
		"repos/acme/widgets/pulls/42/comments": `[]`,
		// A thread whose first comment is gone has no author to match.
		"graphql": `{"data":{"repository":{"pullRequest":{"reviewThreads":{"nodes":[{"isResolved":false,"comments":{"nodes":[]}}]}}}}}`,
	}}
	f, err := GitHub{Gh: g.run, User: "me"}.RoundFacts(widgetsPR, Round{Head: "h2", StartedAt: t0})
	if err != nil {
		t.Fatal(err)
	}
	if f.UserReview != nil || f.UnresolvedUserThreads != 0 {
		t.Errorf("no approval, no author: %+v", f)
	}
}

func TestGraphqlVarsMismatchTrailingFlag(t *testing.T) {
	if err := graphqlVarsMismatch([]string{"api", "graphql", "-f", "query=query { x }", "-f"}); err != nil {
		t.Errorf("a trailing flag without a value: %v", err)
	}
}

func TestEightCharacterSessionIDs(t *testing.T) {
	dir := t.TempDir()
	const id = "d2cc10f2"
	if err := os.MkdirAll(filepath.Join(dir, "jobs", id), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "jobs", id, "state.json"), []byte(`{"state":"done","respawnFlags":["--x"]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if flags, ran, err := readSaved(dir, id); err != nil || !ran || !slices.Equal(flags, []string{"--x"}) {
		t.Errorf("readSaved: %q %v %v", flags, ran, err)
	}
	if got := JobState(dir, id); got != "done" {
		t.Errorf("JobState %q", got)
	}
}

func TestWorkersForSkipsPidZero(t *testing.T) {
	s := Sessions{Alive: func(int) bool { return true }} // kill(0, 0) succeeds: it signals the process group
	if got := s.workersFor(sid, []Worker{workerFor(sid, 0)}); len(got) != 0 {
		t.Errorf("pid 0 counted as a live worker: %+v", got)
	}
}

func TestWindowOpenIsNoWorkersOwnProcess(t *testing.T) {
	f := newFakeSessions()
	w := workerFor(sid, 500)
	w.ReplPID = 501
	f.workers = []Worker{w}
	for pid, want := range map[int]bool{500: false, 501: false, 999: true} {
		f.window = pid
		if got := f.sessions().WindowOpen(sid); got != want {
			t.Errorf("window pid %d: open %v, want %v", pid, got, want)
		}
	}
}

func TestPostResolveWithoutArgumentsIsResolveUsage(t *testing.T) {
	var out, errb bytes.Buffer
	code := runPost([]string{prURL, kindResolve}, (&fakeGh{}).run, func(PR, string, int) error { return nil }, nil, &out, &errb)
	if code != 2 || !strings.Contains(errb.String(), "resolve needs a PR and --thread") {
		t.Errorf("exit %d, stderr %q", code, errb.String())
	}
}

func TestSecretInCatchesEightCharacters(t *testing.T) {
	if name := secretIn("see 12345678", []string{"API_TOKEN=12345678"}); name != "API_TOKEN" {
		t.Errorf("an 8-character secret got through: %q", name)
	}
}

func TestFail_NotFound_CopyFailureSaysSo(t *testing.T) {
	for _, copyErr := range []error{nil, errors.New("exit status 127")} {
		h := newHost(t, "", true, false)
		h.d.Capture = func(_ string, argv []string) (string, error) {
			if argv[0] == "notify-send" {
				return "default\n", nil
			}
			return "", copyErr
		}
		fail(h.d, widgetsNotFound)
		said := len(h.spawns) == 1 && strings.Contains(strings.Join(h.spawns[0], " "), "could not copy")
		if said != (copyErr != nil) || (copyErr == nil && len(h.spawns) != 0) {
			t.Errorf("copy error %v: spawns %q", copyErr, h.spawns)
		}
	}
}

// The roster is a map: a dead entry for the same id must not hide the
// live one whichever comes first, so the lookup is repeated.
func TestWorkerPIDSkipsPidZeroEntries(t *testing.T) {
	old := processAlive
	defer func() { processAlive = old }()
	processAlive = func(int) bool { return true }
	dir := t.TempDir()
	writeRoster(t, dir, fmt.Sprintf(`{"workers":{"aa": {"sessionId": %q, "pid": 0}, "bb": {"sessionId": %q, "pid": 100}}}`, sid, sid))
	for range 64 {
		if got := workerPID(dir, sid); got != 100 {
			t.Fatalf("got %d, want the live worker 100", got)
		}
	}
}

func TestSessionPIDSkipsPidZeroFiles(t *testing.T) {
	oldAlive, oldStart := processAlive, procStart
	defer func() { processAlive, procStart = oldAlive, oldStart }()
	processAlive = func(int) bool { return true }
	procStart = func(int) string { return "" }
	dir := t.TempDir()
	writeLive(t, dir, 0, fmt.Sprintf(`{"pid":0,"sessionId":%q}`, sid)) // sorts before 100.json
	writeLive(t, dir, 100, fmt.Sprintf(`{"pid":100,"sessionId":%q}`, sid))
	if got := sessionPID(dir, sid); got != 100 {
		t.Errorf("got %d, want 100", got)
	}
}

func TestReadProcStartNineteenFields(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "7"), 0o755); err != nil {
		t.Fatal(err)
	}
	stat := "7 (x) " + strings.Repeat("1 ", 18) + "1" // fields 3 to 21: one short of the start time
	if err := os.WriteFile(filepath.Join(root, "7", "stat"), []byte(stat), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := readProcStart(root, 7, func(int) (string, error) { return "", errors.New("no ps") }); got != "" {
		t.Errorf("got %q", got)
	}
}

func TestTickFlagsAreExclusive(t *testing.T) {
	for _, args := range [][]string{{"--dry-run", "--reset"}, {"--dry-run", "--self-check"}, {"--reset", "--self-check"}} {
		h := newHost(t, `{"review_repos":["acme/*"]}`, true, true)
		h.d.Home = t.TempDir()
		h.d.StateDir = h.d.Home + "/.local/state"
		if code := runTick(args, h.d); code != 2 {
			t.Errorf("%q: exit %d, want usage", args, code)
		}
	}
}

// A breaker that cannot be read fails the tick: the journal is where a
// failed tick shows.
func TestTickFailsOnAnUnreadableBreaker(t *testing.T) {
	w := newTickWorld(t)
	w.h.version = func() (string, error) {
		// After the self-check's read of the breaker, before the last one.
		_, err := w.h.ledger.db.Exec(`drop table breaker`)
		return "2.1.314 (Claude Code)", err
	}
	if err := w.h.tick(); err == nil {
		t.Error("tick succeeded without reading the breaker")
	}
}

// A PR whose lock another process holds has a round running there.
func TestTickALockedPRTakesASlot(t *testing.T) {
	w := newTickWorld(t)
	busy := w.accept(t, 1)
	if _, err := w.h.ledger.StartRound(busy, "h1", "new_head", t0); err != nil {
		t.Fatal(err)
	}
	w.locked[busy.SessionID()] = true
	for n := 2; n <= 3; n++ {
		w.accept(t, n)
		w.elly.prs = append(w.elly.prs, requestedPR(n))
	}
	if err := w.h.tick(); err != nil {
		t.Fatal(err)
	}
	if len(w.started) != 1 {
		t.Errorf("with two slots and one locked round, started %d", len(w.started))
	}
}

func TestTickMarksASettledRoundRead(t *testing.T) {
	w := newTickWorld(t)
	pr := w.accept(t, 42)
	if _, err := w.h.ledger.StartRound(pr, "h1", "new_head", t0); err != nil {
		t.Fatal(err)
	}
	w.gh.facts = RoundFacts{State: "open", Head: "h1", NewThreads: 1, RoundReviewURL: "r"}
	if err := w.h.tick(); err != nil {
		t.Fatal(err)
	}
	if a := w.elly.put[pr.URL()]; a.State != stateHandled {
		t.Errorf("elly got %+v", a)
	}
	if unread, _ := w.h.ledger.UnreadRounds(); len(unread) != 0 {
		t.Errorf("a settled round stays unread: %+v", unread)
	}
}

// A gate on one PR does not stop the PRs after it, and a clean start
// leaves no error in the journal.
func TestTickAGatedPRDoesNotStopTheNext(t *testing.T) {
	w := newTickWorld(t)
	w.h.tcfg.DailyCap = 1
	gated := w.accept(t, 1)
	id, err := w.h.ledger.StartRound(gated, "h1", "x", t0.Add(-time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if err := w.h.ledger.MarkRead(id); err != nil {
		t.Fatal(err)
	}
	next := w.accept(t, 2)
	w.elly.prs = []EllyPR{requestedPR(1), requestedPR(2)}
	if err := w.h.tick(); err != nil {
		t.Fatal(err)
	}
	if a := w.elly.put[gated.URL()]; !strings.HasPrefix(a.Note, "paused") {
		t.Errorf("the gated PR: %+v", a)
	}
	if len(w.started) != 1 || !slices.Contains(w.started[0], next.RoundSessionID()) {
		t.Errorf("started %q, want the second PR's round", w.started)
	}
	if strings.Contains(w.out.String(), "claude-review tick:") {
		t.Errorf("a clean start logged an error: %q", w.out.String())
	}
}

type deleteFailingElly struct{ *fakeElly }

func (deleteFailingElly) Delete(string) error { return errors.New("elly: 503") }

// A failed launch clears the running row it stood for; a failure to clear
// it is reported, and a clean clear reports nothing.
func TestLaunchFailureClearsTheRunningRow(t *testing.T) {
	for _, failing := range []bool{false, true} {
		w := newTickWorld(t)
		if failing {
			w.h.elly = deleteFailingElly{w.elly}
		}
		w.h.findRepo = func(PR) (string, string, error) { return "", "", errors.New("repo not found") }
		if err := w.h.ledger.SetAnnotation(widgetsPR, Annotation{State: stateRunning, Note: "started by tick"}); err != nil {
			t.Fatal(err)
		}
		if _, err := w.h.launch(widgetsPR, nil, func(time.Time) error { return nil }); err == nil {
			t.Fatal("launch without a clone succeeded")
		}
		if _, ok, _ := w.h.ledger.Annotation(widgetsPR); ok {
			t.Errorf("failing %v: the running row stayed", failing)
		}
		if said := strings.Contains(w.out.String(), "elly: 503"); said != failing || strings.Contains(w.out.String(), "<nil>") {
			t.Errorf("failing %v: out %q", failing, w.out.String())
		}
	}
}

func TestLaunchCapCountsTheLastDay(t *testing.T) {
	w := newTickWorld(t)
	w.h.tcfg.TotalDailyCap = 1
	if _, err := w.h.ledger.StartRound(widgetsPR, "h1", "new_head", t0.Add(-time.Hour)); err != nil {
		t.Fatal(err)
	}
	if _, err := w.h.launch(widgetsPR, nil, func(time.Time) error { return nil }); err == nil || len(w.started) != 0 {
		t.Errorf("a launch an hour ago did not reach a cap of 1: err %v, started %q", err, w.started)
	}
}

func TestRefreshedExpiryEdges(t *testing.T) {
	h := tickHost{now: func() time.Time { return t0 }}
	for _, c := range []struct {
		left    time.Duration
		zero    bool
		send    bool
		explain string
	}{
		{-time.Second, true, false, "expired"},
		{0, true, false, "expiring now"},
		{30 * time.Second, false, false, "too little left for elly"},
		{time.Minute, false, true, "a minute left"},
	} {
		exp, send := h.refreshedExpiry(Annotation{Expires: t0.Add(c.left)})
		if exp.IsZero() != c.zero || send != c.send || (!c.zero && !exp.Equal(t0.Add(c.left))) {
			t.Errorf("%s: expires %v send %v", c.explain, exp, send)
		}
	}
}

func TestEnsureTrustedReportsAFailedWrite(t *testing.T) {
	if os.Getuid() == 0 {
		t.Skip("root writes into a read-only directory")
	}
	p := writeClaudeJSON(t, `{"projects":{}}`)
	dir := filepath.Dir(p)
	if err := os.Chmod(dir, 0o500); err != nil { //nolint:gosec // a test's own temp dir
		t.Fatal(err)
	}
	defer func() { _ = os.Chmod(dir, 0o700) }() //nolint:gosec // restored for TempDir's cleanup
	if err := ensureTrusted(p, "/h/code/widgets"); !errors.Is(err, fs.ErrPermission) {
		t.Errorf("got %v, want the write's own error", err)
	}
}

func TestLoadConfigEllyURL(t *testing.T) {
	dir := t.TempDir()
	if cfg, err := LoadConfig(ConfigPath(dir), "/h"); err != nil || cfg.EllyURL != "http://localhost:9876" {
		t.Errorf("default: %q %v", cfg.EllyURL, err)
	}
	if err := os.MkdirAll(filepath.Dir(ConfigPath(dir)), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(ConfigPath(dir), []byte(`{"elly_url":"http://127.0.0.1:1"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if cfg, err := LoadConfig(ConfigPath(dir), "/h"); err != nil || cfg.EllyURL != "http://127.0.0.1:1" {
		t.Errorf("set: %q %v", cfg.EllyURL, err)
	}
}

// A round already started is not failed by elly refusing its running row,
// but the refusal reaches the journal.
func TestMarkRunningOrSayReportsAFailedSend(t *testing.T) {
	w := newTickWorld(t)
	w.elly.putErr = errors.New("elly: 503")
	w.h.markRunningOrSay(widgetsPR, "tick", t0)
	if !strings.Contains(w.out.String(), "claude-review tick: "+widgetsPR.Slug()+": elly: 503") {
		t.Errorf("out %q", w.out.String())
	}
}

// An escape that does not advance loops forever while the word grows, and a
// mutation run then hangs until memory runs out. edges_test.go sorts before
// platform_test.go, so this watchdog ends the test binary first.
func TestSplitWordsEscapeReturns(t *testing.T) {
	done := make(chan []string, 1)
	go func() {
		words, _ := splitWords(`a\ b`)
		done <- words
	}()
	select {
	case words := <-done:
		if !slices.Equal(words, []string{"a b"}) {
			t.Errorf("got %q", words)
		}
	case <-time.After(2 * time.Second):
		panic("splitWords did not return on an escaped space")
	}
}

// A click starts a round seconds after the review request it answers. elly
// still lists the request until its next poll, but the round started after
// everything elly saw, so tick must not run the same head again. A round
// that started before the PR last changed does not cover it.
func TestPlanTickARoundAfterTheLastUpdateCoversIt(t *testing.T) {
	cfg := TickConfig{ReviewRepos: []string{"acme/*"}, User: "me", DailyCap: 3}
	asked := ellyPR(func(p *EllyPR) { p.ReviewRequestedFromUsers = []string{"me"} })
	for _, c := range []struct {
		started time.Time
		want    int
	}{{t0.Add(5 * time.Second), 0}, {t0, 0}, {t0.Add(-time.Minute), 1}} {
		l := openTestLedger(t)
		if err := l.Accept(widgetsPR, "h1", "main", t0); err != nil {
			t.Fatal(err)
		}
		if _, err := l.StartRound(widgetsPR, "h1", "click@x", c.started); err != nil {
			t.Fatal(err)
		}
		got, err := planTick([]EllyPR{asked}, cfg, l, fakeGate{descends: true, base: "main"}, t0.Add(time.Hour))
		if err != nil || len(got) != c.want {
			t.Errorf("click round at %v, PR updated %v: planned %+v %v, want %d", c.started, t0, got, err, c.want)
		}
	}
}
