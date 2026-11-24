package main

import (
	"database/sql/driver"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/chelmertz/dotfiles/claude-review/ids"
	"modernc.org/sqlite"
	"pgregory.net/rapid"
)

// A generator of histories over tickWorld: GitHub events, round outcomes,
// clicks and crashes between writes, with the invariants the transactional
// core must keep checked after every step. The Defect tests below pin each
// known hole in today's code; each is skipped until the plan step that
// closes it lands (2026-11-23 transactional-core design, "Plan, in
// commits").

// --- Crash injection ---------------------------------------------------

// A crash is a process dying between two writes. Every ledger table gets
// a trigger that asks gen_crash before each row write (genTemplate); the
// armed point fails its write, and from then on every write and every
// external effect fails, as nothing runs in a dead process. A restart reopens the ledger
// (OpenLedger, as a new process does) and builds a fresh host on it. Production code is untouched.

func init() {
	sqlite.MustRegisterScalarFunction("gen_crash", 3, func(_ *sqlite.FunctionContext, args []driver.Value) (driver.Value, error) {
		id, _ := args[0].(int64)
		table, _ := args[1].(string)
		op, _ := args[2].(string)
		if c, ok := genCrashes.Load(id); ok && c.(*crashCtl).hit(table, op) {
			return int64(1), nil
		}
		return int64(0), nil
	})
}

var (
	genCrashes  sync.Map // int64 → *crashCtl
	genCrashSeq atomic.Int64
	errCrashed  = errors.New("gen: the process crashed")
)

// crashedTables are the ledger tables a crash can interrupt a write to.
var crashedTables = []string{"pr_state", "rounds", "annotations", "tokens", "meta", "dismissals", "breaker", "acceptance", "mention_gate", "decisions"}

// Crash points outside the ledger: the effect happens, then the process
// dies before its next write.
const (
	crashAfterLaunch = "launch" // a --bg launch succeeded
	crashAfterStop   = "stop"   // a worker was stopped
	crashAfterPut    = "put"    // elly took a row
	crashAfterDelete = "delete" // elly dropped a row
)

// genCrashPoints is every crash point the generator arms.
var genCrashPoints = append(slices.Clone(crashedTables), crashAfterLaunch, crashAfterStop, crashAfterPut, crashAfterDelete)

type crashCtl struct {
	mu   sync.Mutex
	at   string // the armed point: a table, or a crashAfter* effect
	nth  int    // the nth write or effect there, or the first one after it, crashes
	seen int
	dead bool
	// spare keeps the armed crash out of the window of the known defect
	// left, which the Defect tests pin; it fires at the next write outside
	// it instead. launched: a worker is launched and the owed move has not
	// stored its state: the launch crash. Before StartRound the next pass
	// launches the same review again; after it, the old state stays over
	// the new round, an Approve at the old head included. Commit writes
	// pr_state last in its transaction, so the window ends there. A step's
	// own writes (effects, mint, decision rows, state) are one transaction
	// and a crash at any of them is armed like any other.
	spare    bool
	launched bool
}

func newCrashCtl() (*crashCtl, int64) {
	c, id := &crashCtl{}, genCrashSeq.Add(1)
	genCrashes.Store(id, c)
	return c, id
}

func (c *crashCtl) arm(at string, nth int) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.at, c.nth, c.seen = at, nth, 0
}

// hit reports whether a write or an effect at point (a table and its op, or
// a crashAfter* effect that already happened) must fail: the process is
// dead, or this is the armed one, which kills it.
func (c *crashCtl) hit(point, _ string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.dead {
		return true
	}
	if point == crashAfterLaunch {
		c.launched = true
	}
	if c.at != "" && c.at == point {
		c.seen++
	}
	if c.at != "" && c.seen >= c.nth && (!c.spare || !c.inDefectWindow()) {
		c.dead = true
		return true
	}
	if point == "pr_state" {
		c.launched = false
	}
	return false
}

func (c *crashCtl) inDefectWindow() bool { return c.launched }

func (c *crashCtl) isDead() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.dead
}

// revive disarms and brings the process back, as a restart does.
func (c *crashCtl) revive() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.at, c.nth, c.seen, c.dead = "", 0, 0, false
	c.launched = false
}

// genTemplate is a fresh ledger with the crash triggers in it, made once
// and copied for each world: making one takes about 60 ms, mostly syncs,
// and a world's ticks take 2 ms. The triggers read the world's id from
// gen_world.
var genTemplate = sync.OnceValues(func() ([]byte, error) {
	dir, err := os.MkdirTemp("", "claude-review-gen-")
	if err != nil {
		return nil, err
	}
	defer func() { _ = os.RemoveAll(dir) }()
	path := filepath.Join(dir, "ledger.db")
	l, err := OpenLedger(path)
	if err != nil {
		return nil, err
	}
	stmts := []string{`create table gen_world (id integer not null)`, `insert into gen_world (id) values (0)`}
	for _, table := range crashedTables {
		for _, op := range []string{"insert", "update", "delete"} {
			stmts = append(stmts, fmt.Sprintf(`create trigger gen_crash_%[1]s_%[2]s before %[2]s on %[1]s
				when gen_crash((select id from gen_world), '%[1]s', '%[2]s') begin select raise(abort, 'gen: crashed'); end`, table, op))
		}
	}
	for _, q := range stmts {
		if _, err := l.db.Exec(q); err != nil {
			return nil, errors.Join(err, l.Close())
		}
	}
	if err := l.Close(); err != nil {
		return nil, err
	}
	return os.ReadFile(path) //nolint:gosec // the template this func just wrote
})

// openGenLedger opens a copy of the template at path, its triggers asking
// crash controller id.
func openGenLedger(path string, id int64) (*Ledger, error) {
	tmpl, err := genTemplate()
	if err != nil {
		return nil, err
	}
	if err := os.WriteFile(path, tmpl, 0o600); err != nil {
		return nil, err
	}
	l, err := OpenLedger(path)
	if err != nil {
		return nil, err
	}
	if _, err := l.db.Exec(`update gen_world set id = ?`, id); err != nil {
		return nil, errors.Join(err, l.Close())
	}
	return l, nil
}

// dyingElly is fakeElly in a process that may die: a dead one sends
// nothing, and an armed effect dies right after elly took it.
type dyingElly struct {
	*fakeElly
	c *crashCtl
}

func (e dyingElly) Put(u string, a Annotation, exp time.Time) error {
	if e.c.isDead() {
		return errCrashed
	}
	err := e.fakeElly.Put(u, a, exp)
	if err == nil {
		e.c.hit(crashAfterPut, "")
	}
	return err
}

func (e dyingElly) PutSource(source, u string, a Annotation, exp time.Time) error {
	if e.c.isDead() {
		return errCrashed
	}
	return e.fakeElly.PutSource(source, u, a, exp)
}

func (e dyingElly) Delete(u string) error {
	if e.c.isDead() {
		return errCrashed
	}
	err := e.fakeElly.Delete(u)
	if err == nil {
		// elly no longer holds it: the fake keeps put as a record of sends
		delete(e.put, u)
		e.c.hit(crashAfterDelete, "")
	}
	return err
}

// --- GitHub, per PR -------------------------------------------------------

// genGitHub answers for each PR on its own: open or closed, its head, the
// user's review, and the summary review the last round posted.
type genGitHub struct {
	// rewritten: every head was force-pushed over the one accepted.
	rewritten bool
	open      map[int]bool
	head      map[int]string
	user      map[int]*Review
	summary   map[int]string
	dismissed []ids.ReviewID
}

func (g *genGitHub) Descends(PR, string, string) (bool, error) { return !g.rewritten, nil }
func (g *genGitHub) Base(pr PR) (string, bool, error)          { return "main", g.open[pr.Number], nil }
func (g *genGitHub) RoundFacts(pr PR, _ Round) (RoundFacts, error) {
	return RoundFacts{State: g.state(pr.Number), Head: g.head[pr.Number], UserReview: g.user[pr.Number], RoundReviewURL: g.summary[pr.Number]}, nil
}

func (g *genGitHub) Standing(pr PR) (string, string, *Review, error) {
	return g.state(pr.Number), g.head[pr.Number], g.user[pr.Number], nil
}

func (g *genGitHub) RepliedInRoundThread(PR, time.Time, time.Time) (bool, error) { return false, nil }
func (g *genGitHub) Dismiss(_ PR, id ids.ReviewID, _ string) error {
	g.dismissed = append(g.dismissed, id)
	return nil
}

func (g *genGitHub) DismissalStatus(PR, ids.ReviewID) (DismissalStatus, error) {
	return DismissalStatus{Dismissed: true}, nil
}

func (g *genGitHub) state(n int) string {
	if g.open[n] {
		return prOpen
	}
	return prClosed
}

// --- The world ------------------------------------------------------------

// genWorld is tickWorld over a ledger file it can reopen, a clock it
// advances, a GitHub per PR, and one job state per round session that the
// roster, running, blocked and lastMessage all read.
type genWorld struct {
	*tickWorld
	path    string
	crash   *crashCtl
	crashID int64
	now     time.Time
	gh      *genGitHub
	prs     []PR
	asked   map[int]bool
	update  map[int]time.Time
	shas    int
	// launches counts --bg launches by PR and the PR's head then: a review
	// launched twice for the same reason shows here.
	launches map[string]int
	restarts int
	// full reads a launched round's transcript as defect D leaves it.
	full bool
	// crashed: the last pass crashed.
	crashed bool
}

func newGenWorld(dir string, n int) (*genWorld, error) {
	path := filepath.Join(dir, "ledger.db")
	c, id := newCrashCtl()
	l, err := openGenLedger(path, id)
	if err != nil {
		return nil, err
	}
	g := &genWorld{
		tickWorld: newTickWorldOn(l),
		path:      path,
		crash:     c,
		crashID:   id,
		now:       t0,
		gh:        &genGitHub{open: map[int]bool{}, head: map[int]string{}, user: map[int]*Review{}, summary: map[int]string{}},
		asked:     map[int]bool{},
		update:    map[int]time.Time{},
		launches:  map[string]int{},
	}
	g.h.now = func() time.Time { return g.now }
	g.h.gh = g.gh
	g.h.elly = dyingElly{g.elly, c}
	start := g.h.start
	g.h.start = func(dir string, argv []string) error {
		if c.isDead() {
			return errCrashed
		}
		if err := start(dir, argv); err != nil || !slices.Contains(argv, "--bg") {
			return err
		}
		sid := mustSession(argv[slices.Index(argv, "--resume")+1])
		for _, pr := range g.prs {
			if pr.RoundSessionID() == sid {
				g.gh.summary[pr.Number] = "" // a new round has posted nothing yet
				if !g.full {
					// The transcript keeps the last round's message until
					// this one writes (defect D); only full reads it.
					delete(g.msgs, sid)
				}
			}
		}
		g.setJob(sid, "working", "")
		c.hit(crashAfterLaunch, "")
		return nil
	}
	stop := g.h.sess.Stop
	g.h.sess.Stop = func(short ids.WorkerKey) error {
		if c.isDead() {
			return errCrashed
		}
		if err := stop(short); err != nil {
			return err
		}
		for _, pr := range g.prs {
			if sid := pr.RoundSessionID(); sid.Key() == short {
				g.setJob(sid, "stopped", "")
			}
		}
		c.hit(crashAfterStop, "")
		return nil
	}
	for i := 1; i <= n; i++ {
		pr := PR{Owner: "acme", Repo: "widgets", Number: i}
		g.prs = append(g.prs, pr)
		g.gh.open[i], g.gh.head[i], g.update[i] = true, g.sha(), t0
		if err := l.Accept(pr, g.gh.head[i], "main", t0); err != nil {
			return nil, errors.Join(err, l.Close())
		}
	}
	return g, nil
}

func (g *genWorld) close() error {
	genCrashes.Delete(g.crashID)
	return g.h.ledger.Close()
}

// sha is a fresh 40-hex commit, so approve lines parse.
func (g *genWorld) sha() string {
	g.shas++
	return fmt.Sprintf("%040x", 0xc0ffee000+g.shas)
}

// setJob is Claude Code's state for sid's background job: working is
// running, blocked waits on why, anything else is over.
func (g *genWorld) setJob(sid ids.SessionID, state, why string) {
	g.fs.states[sid] = state
	g.live[sid] = state == "working"
	if state == "blocked" {
		g.blocked[sid] = why
	} else {
		delete(g.blocked, sid)
	}
}

func (g *genWorld) job(n int) string { return g.fs.states[g.prs[n-1].RoundSessionID()] }

// ellyRows is what elly lists: the open PRs, requested or not.
func (g *genWorld) ellyRows() []EllyPR {
	var out []EllyPR
	for _, pr := range g.prs {
		if !g.gh.open[pr.Number] {
			continue
		}
		asked := askedNotAsked
		if g.asked[pr.Number] {
			asked = askedRequested
		}
		out = append(out, EllyPR{Url: pr.URL(), RepoOwner: "acme", RepoName: "widgets", Author: "alice",
			HeadRefOid: g.gh.head[pr.Number], LastUpdated: g.update[pr.Number], Asked: askedAs(asked), MentionsReadable: &readable})
	}
	return out
}

// tick is one pass, crashing at the armed point if any; a pass that
// crashed is followed by a restart, and g.crashed says it did.
func (g *genWorld) tick() error {
	g.elly.prs = g.ellyRows()
	before := len(g.started)
	_ = g.h.tick() // a failed pass is the journal's; the invariants judge it
	g.countLaunches(before)
	g.crashed = g.crash.isDead()
	if g.crashed {
		fmt.Fprintln(g.out, "-- crashed; restart")
		return g.restart()
	}
	g.crash.revive() // an armed point the pass never reached
	return nil
}

// countLaunches counts each --bg launch since before under its PR and the
// PR's head.
func (g *genWorld) countLaunches(before int) {
	for _, argv := range g.started[before:] {
		if !slices.Contains(argv, "--bg") {
			continue
		}
		sid := mustSession(argv[slices.Index(argv, "--resume")+1])
		for _, pr := range g.prs {
			if pr.RoundSessionID() == sid {
				g.launches[pr.URL()+" "+g.gh.head[pr.Number]]++
			}
		}
	}
}

// restart is a new process on the same ledger and the same world.
func (g *genWorld) restart() error {
	g.crash.revive()
	g.restarts++
	if err := g.h.ledger.Close(); err != nil {
		return err
	}
	l, err := OpenLedger(g.path)
	if err != nil {
		return err
	}
	g.h.ledger = l
	return nil
}

// finish ends n's working round with msg; summary is the review it posted.
func (g *genWorld) finish(n int, msg, summary string) {
	sid := g.prs[n-1].RoundSessionID()
	g.setJob(sid, "done", "")
	g.msgs[sid] = msg
	g.gh.summary[n] = summary
}

// click is the ledger side of launch and backgroundClick, under the
// harness's fakes: the PR's lock (held elsewhere, the click fails as a
// real one does), the breaker, the window check, the starting move, the launch through the
// same starter and afterRelaunch, recordClick, and the abandon.
func (g *genWorld) click(n int) error {
	pr := g.prs[n-1]
	h := g.h
	h.out, h.notify, h.say = nil, nil, func(error) {}
	lk, err := lockPR(pr, h.lock)
	if errors.Is(err, errLocked) {
		return nil
	}
	if err != nil {
		return err
	}
	defer lk.Unlock()
	if reason, err := h.ledger.Tripped(); err != nil || reason != "" {
		return err
	}
	if err := h.refuseUnderWindow(lk, pr); err != nil {
		return err
	}
	s, err := h.move(lk, pr, Event{Kind: evClick, From: "elly"})
	if err != nil || maybeAlive(s) {
		return err
	}
	started := false
	defer func() {
		if !started {
			_, _ = h.move(lk, pr, Event{Kind: evAbandoned})
		}
	}()
	at := g.now
	before := len(g.started)
	starter := roundStarter{sess: h.sess, exists: h.exists, run: h.start, wrapper: h.tcfg.Wrapper}
	var from int64
	starter.beforeLaunch = func() { from = h.transcriptSize(pr.RoundSessionID()) }
	res, err := starter.start(h.cfg, pr, "/clone", "upstream", false, func() error { return g.h.settleUnread(lk, pr) })
	g.countLaunches(before)
	if err != nil && res.Kind != relaunchDuplicate {
		return err
	}
	not, err := h.afterRelaunch(lk, pr, res, err, func() error {
		return h.recordClick(lk, clickTarget{pr: pr, head: g.gh.head[n], from: "elly"}, at, from)
	}, func(detail string) error {
		_, err := h.move(lk, pr, Event{Kind: evFailedToStart, Class: detail})
		return err
	})
	started = not == "" && err == nil
	return err
}

// --- Invariants ------------------------------------------------------------

// runningHasARound: a PR running has an unread round or a live worker.
func (g *genWorld) runningHasARound() error {
	states, err := g.h.ledger.PRStates()
	if err != nil {
		return err
	}
	rounds, err := g.h.ledger.UnreadRounds()
	if err != nil {
		return err
	}
	for _, pr := range g.prs {
		s := states[pr.URL()]
		if s.State != stRunning {
			continue
		}
		if !slices.ContainsFunc(rounds, func(r Round) bool { return r.PR == pr.URL() }) && !g.live[pr.RoundSessionID()] {
			return fmt.Errorf("%s is %s with no unread round and no live worker", pr.URL(), describe(s))
		}
	}
	return nil
}

// approveAtHead: every Approve row elly holds is at the PR's head now.
func (g *genWorld) approveAtHead() error {
	rows, err := g.h.ledger.Annotations()
	if err != nil {
		return err
	}
	for _, pr := range g.prs {
		for _, l := range rows[pr.URL()].Links {
			if l.Action != actionApprove {
				continue
			}
			u, err := url.Parse(l.URL)
			if err != nil {
				return err
			}
			if at := u.Query().Get("at"); at != g.gh.head[pr.Number] {
				return fmt.Errorf("%s shows Approve at %s; its head is %s", pr.URL(), at, g.gh.head[pr.Number])
			}
		}
	}
	return nil
}

// quiesce lets the world settle: no crash, GitHub quiet, no clicks, every
// working round ends cut off, and passes run ten minutes apart. It
// returns the PRs still running or starting after k passes.
func (g *genWorld) quiesce(k int) ([]string, error) {
	g.crash.revive()
	for range k {
		for _, pr := range g.prs {
			if g.job(pr.Number) == "working" {
				msg, summary := g.message(pr.Number, "empty")
				g.finish(pr.Number, msg, summary)
			}
		}
		g.now = g.now.Add(10 * time.Minute)
		if err := g.tick(); err != nil {
			return nil, err
		}
	}
	states, err := g.h.ledger.PRStates()
	if err != nil {
		return nil, err
	}
	var stuck []string
	for _, pr := range g.prs {
		if s := states[pr.URL()]; s.State == stRunning || s.State == stStarting {
			stuck = append(stuck, pr.URL()+" "+describe(s))
		}
	}
	return stuck, nil
}

// --- The generator --------------------------------------------------------

// genOutcomes are the last messages a finished round may leave.
var genOutcomes = []string{"approve at head", "approve at an older sha", "nothing", "question", "posted a summary", "other", "empty"}

func (g *genWorld) message(n int, outcome string) (msg, summary string) {
	pr := g.prs[n-1]
	switch outcome {
	case "approve at head":
		return "Clean.\n`! claude-review approve " + pr.URL() + " --at " + g.gh.head[n] + "`", ""
	case "approve at an older sha":
		return "Clean.\n`! claude-review approve " + pr.URL() + " --at " + strings.Repeat("0", 40) + "`", ""
	case "nothing":
		return "No new commits.\n" + nothingLine, ""
	case "question":
		return "Should the job retry?\n\n" + questionLine, ""
	case "posted a summary":
		return "Posted the review.", "https://github.com/acme/widgets/pull/1#pullrequestreview-1"
	case "other":
		return "I looked around.", ""
	}
	return "", ""
}

// genWeighted draws actions: passes and round endings most, a close
// rarely, since it ends the PR's history.
var genWeighted = []string{
	"tick", "tick", "tick", "tick", "tick",
	"finish", "finish", "finish",
	"time", "time", "push", "request", "block", "answer in the session",
	"user approves", "click", "click", "close",
}

// genHistories draws histories and checks them after every step, then
// quiesces them. full also crashes inside the launch crash's window and
// reads a launched round's transcript as defect D leaves it; the default
// keeps both out of the known defects' reach.
func genHistories(t *testing.T, full bool) {
	rapid.Check(t, func(rt *rapid.T) {
		dir, err := os.MkdirTemp("", "claude-review-gen-")
		if err != nil {
			rt.Fatal(err)
		}
		defer func() { _ = os.RemoveAll(dir) }()
		g, err := newGenWorld(dir, rapid.IntRange(1, 2).Draw(rt, "prs"))
		if err != nil {
			rt.Fatal(err)
		}
		defer func() { _ = g.close() }()
		g.crash.spare, g.full = !full, full
		pick := func(t *rapid.T) int { return rapid.IntRange(1, len(g.prs)).Draw(t, "pr") }
		for _, pr := range g.prs {
			g.asked[pr.Number] = true // asked at the start, so rounds get going
		}
		actions := map[string]func(*rapid.T){
			"tick": func(t *rapid.T) {
				if rapid.IntRange(0, 2).Draw(t, "crash?") == 0 {
					g.crash.arm(rapid.SampledFrom(genCrashPoints).Draw(t, "crash at"), rapid.IntRange(1, 3).Draw(t, "nth"))
				}
				if err := g.tick(); err != nil {
					t.Fatal(err)
				}
			},
			"time": func(t *rapid.T) {
				g.now = g.now.Add(time.Duration(rapid.IntRange(1, 90).Draw(t, "minutes")) * time.Minute)
			},
			"push": func(t *rapid.T) {
				n := pick(t)
				g.gh.head[n], g.update[n] = g.sha(), g.now
			},
			"request": func(t *rapid.T) {
				n := pick(t)
				g.asked[n], g.update[n] = rapid.Bool().Draw(t, "requested"), g.now
			},
			"finish": func(t *rapid.T) {
				if n := pick(t); g.job(n) == "working" {
					msg, summary := g.message(n, rapid.SampledFrom(genOutcomes).Draw(t, "outcome"))
					g.finish(n, msg, summary)
				}
			},
			"block": func(t *rapid.T) {
				if n := pick(t); g.job(n) == "working" {
					g.setJob(g.prs[n-1].RoundSessionID(), "blocked", "which config?")
				}
			},
			"answer in the session": func(t *rapid.T) {
				if n := pick(t); g.job(n) == "blocked" {
					g.setJob(g.prs[n-1].RoundSessionID(), "working", "")
				}
			},
			"close": func(t *rapid.T) {
				g.gh.open[pick(t)] = false
			},
			"user approves": func(t *rapid.T) {
				n := pick(t)
				g.gh.user[n] = &Review{ID: reviewID(int64(n)), State: reviewApproved, CommitID: g.gh.head[n], SubmittedAt: g.now}
				g.update[n] = g.now
			},
			"click": func(t *rapid.T) {
				if n := pick(t); g.gh.open[n] {
					_ = g.click(n) // a refused click is the user's to read
				}
			},
		}
		steps := rapid.IntRange(1, 40).Draw(rt, "steps")
		for range steps {
			name := rapid.SampledFrom(genWeighted).Draw(rt, "action")
			actions[name](rt)
			fmt.Fprintf(g.out, "-- after %s\n", name)
			if err := g.runningHasARound(); err != nil {
				rt.Fatalf("after %s: %v; journal:\n%s", name, err, g.out)
			}
			// Approve is checked right after a pass that completed: a push,
			// or a crash before the PR's turn, leaves the row stale until
			// the next one.
			if name == "tick" && !g.crashed {
				if err := g.approveAtHead(); err != nil {
					rt.Fatalf("after %s: %v; journal:\n%s", name, err, g.out)
				}
			}
		}
		stuck, err := g.quiesce(8)
		if err != nil {
			rt.Fatal(err)
		}
		if len(stuck) > 0 {
			rt.Fatalf("after 8 quiet passes still not settled: %q; journal:\n%s", stuck, g.out)
		}
		if err := replayDecisions(g.h.ledger); err != nil {
			rt.Fatal(err)
		}
	})
}

func TestPropGenHistoriesKeepTheInvariants(t *testing.T) {
	genHistories(t, false)
}

// The same histories with crashes inside the launch crash's window and a
// launched round reading the transcript defect D leaves. Before plan step 5
// it failed within a few dozen histories, most often on the retry over a
// question, then on defect A (a crash at the Approve mint after the round
// was read); what is left is the launch crash and D.
func TestPropGenHistoriesWithEveryCrashPoint(t *testing.T) {
	t.Skip("the launch crash; unskip when plan step 6 lands")
	genHistories(t, true)
}

// --- Known defects, one test each ------------------------------------------

// newGenWorldT is a genWorld for one directed test, its first PR asked.
func newGenWorldT(t *testing.T, n int) *genWorld { //nolint:unparam // n PRs; the directed tests so far need one
	t.Helper()
	g, err := newGenWorld(t.TempDir(), n)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = g.close() })
	g.asked[1] = true
	return g
}

func (g *genWorld) mustTick(t *testing.T) {
	t.Helper()
	if err := g.tick(); err != nil {
		t.Fatal(err)
	}
}

func (g *genWorld) state(t *testing.T, n int) PRState { //nolint:unparam // the nth PR; the directed tests so far have one
	t.Helper()
	s, _, err := g.h.ledger.PRState(g.prs[n-1])
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func (g *genWorld) unread(t *testing.T, n int) int { //nolint:unparam // the nth PR; the directed tests so far have one
	t.Helper()
	rounds, err := g.h.ledger.UnreadRounds()
	if err != nil {
		t.Fatal(err)
	}
	return len(slices.DeleteFunc(rounds, func(r Round) bool { return r.PR != g.prs[n-1].URL() }))
}

// passes runs k passes ten minutes apart.
func (g *genWorld) passes(t *testing.T, k int) { //nolint:unparam // the skipped defect tests pass 2 and 3
	t.Helper()
	for range k {
		g.now = g.now.Add(10 * time.Minute)
		g.mustTick(t)
	}
}

// A: move marked the PR's rounds read (efRoundsRead) before it stored the
// state, outside any transaction, so a crash between them left running
// with no unread round, and no rule moved it again. Both are now one
// Commit.
func TestDefectAMoveMarksRoundsReadBeforeStoringTheState(t *testing.T) {
	g := newGenWorldT(t, 1)
	g.mustTick(t)
	if s := g.state(t, 1); s.State != stRunning {
		t.Fatalf("no round started: %s", describe(s))
	}
	msg, _ := g.message(1, "nothing")
	g.finish(1, msg, "")
	g.crash.arm("pr_state", 1) // the ended move's own write
	g.mustTick(t)
	if !g.crashed {
		t.Fatal("the pass did not reach the armed write")
	}
	g.passes(t, 3)
	if s := g.state(t, 1); s.State == stRunning {
		t.Errorf("defect A: a crash after the ended move marked its round read left %s, with %d unread rounds and no worker, for 3 passes", describe(s), g.unread(t, 1))
	}
}

// B: refresh read every state before it took each PR's lock and showed
// that snapshot when no evidence moved it, over a row a click wrote in
// between. It now reads each state under the lock.
func TestDefectBRefreshSendsAStateReadBeforeItsLock(t *testing.T) {
	g := newGenWorldT(t, 1)
	g.asked[1] = false // nothing owed: recordMentions and refresh are the pass's only locks
	pr := g.prs[0]
	if err := g.h.ledger.SetMeta(metaStatesMigrated, "1"); err != nil { // and not the migration's
		t.Fatal(err)
	}
	if err := g.h.ledger.SetPRState(pr, PRState{State: stHandled, Kind: kindNothing, Head: g.gh.head[1]}); err != nil {
		t.Fatal(err)
	}
	lock, calls, clicked := g.h.lock, 0, false
	g.h.lock = func(id string) (func(), error) {
		if id == pr.SessionID().String() {
			calls++
		}
		if !clicked && calls == 2 {
			// The click held the lock just before refresh, and released it.
			clicked = true
			if err := g.click(1); err != nil {
				t.Fatal(err)
			}
		}
		return lock(id)
	}
	g.mustTick(t)
	if calls != 3 {
		t.Fatalf("the PR's lock was taken %d times, want recordMentions', the click's and refresh's", calls)
	}
	stored := g.state(t, 1)
	if stored.State != stRunning {
		t.Fatalf("the click did not start a round: %s", describe(stored))
	}
	want := render(pr, stored, View{}).State
	row, _, err := g.h.ledger.Annotation(pr)
	if err != nil {
		t.Fatal(err)
	}
	if got := g.elly.put[pr.URL()].State; got != want || row.State != want {
		t.Errorf("defect B: the PR is %s, elly shows %q and the ledger's row is %q, want %q: refresh sent the state it read before the click", describe(stored), got, row.State, want)
	}
}

// C: hold moved a PR that changed under its first look without the PR's
// lock, so a click holding it did not stop the move. It now takes it.
func TestDefectCHoldMovesThePRWithoutItsLock(t *testing.T) {
	g := newGenWorldT(t, 1)
	pr := g.prs[0]
	before := PRState{State: stHandled, Kind: kindPosted, Head: g.gh.head[1]}
	if err := g.h.ledger.SetPRState(pr, before); err != nil {
		t.Fatal(err)
	}
	g.gh.rewritten, g.gh.head[1] = true, g.sha() // force-pushed under the first look
	g.locked[pr.SessionID().String()] = true     // a click holds the PR
	g.mustTick(t)
	if s := g.state(t, 1); s.State != before.State || s.Kind != before.Kind {
		t.Errorf("defect C: hold moved %s to %s while a click held its lock", describe(before), describe(s))
	}
}

// turn is one transcript record: the user's prompt, or the assistant's
// text, as LastMessage reads them. transcriptLine writes it.
func turn(role, text string) map[string]any {
	if role == "user" {
		return map[string]any{"type": "user", "message": map[string]any{"content": text}}
	}
	return map[string]any{"type": "assistant", "message": map[string]any{"content": []any{map[string]any{"type": "text", "text": text}}}}
}

func appendTranscript(t *testing.T, path string, lines ...string) {
	t.Helper()
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600) //nolint:gosec // a test's temp file
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	for _, l := range lines {
		if _, err := f.WriteString(l); err != nil {
			t.Fatal(err)
		}
	}
}

// D: LastMessage read the last assistant text of the whole session, with no
// round boundary. It now reads from the transcript's size at the round's
// launch.
func TestDefectDLastMessageCrossesTheRoundBoundary(t *testing.T) {
	dir := t.TempDir()
	sid := widgetsPR.RoundSessionID()
	if err := os.MkdirAll(filepath.Join(dir, "projects", "p"), 0o700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "projects", "p", sid.String()+".jsonl")
	appendTranscript(t, path,
		transcriptLine(t, turn("user", "Review the PR.")),
		transcriptLine(t, turn("assistant", "No new commits.\n"+nothingLine)),
	)
	from := TranscriptSize(dir, sid)                                                      // the next round launches
	appendTranscript(t, path, transcriptLine(t, turn("user", "Review the new commits."))) // and dies before writing
	got, err := LastMessage(dir, sid, from)
	if err != nil {
		t.Fatal(err)
	}
	if got != "" {
		t.Errorf("defect D: a round that wrote nothing reads as %q, the round before's last message", got)
	}
}

// D through tick: a round that died before writing is read as the last
// round's verdict and settled on it, instead of retried.
func TestDefectDARoundThatWroteNothingInheritsTheLastVerdict(t *testing.T) {
	g := newGenWorldT(t, 1)
	dir := t.TempDir()
	sid := g.prs[0].RoundSessionID()
	if err := os.MkdirAll(filepath.Join(dir, "projects", "p"), 0o700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "projects", "p", sid.String()+".jsonl")
	g.h.lastMessage = func(id ids.SessionID, from int64) (string, error) { return LastMessage(dir, id, from) }
	g.h.transcriptSize = func(id ids.SessionID) int64 { return TranscriptSize(dir, id) }
	g.mustTick(t)
	appendTranscript(t, path, transcriptLine(t, turn("user", "Review the PR.")), transcriptLine(t, turn("assistant", "No new commits.\n"+nothingLine)))
	g.setJob(sid, "done", "")
	g.passes(t, 1)
	if s := g.state(t, 1); s.State != stHandled || s.Kind != kindNothing {
		t.Fatalf("the first round did not settle: %s", describe(s))
	}
	g.gh.head[1], g.update[1] = g.sha(), g.now // a push: the next round
	g.passes(t, 1)
	if s := g.state(t, 1); s.State != stRunning {
		t.Fatalf("no round started on the push: %s", describe(s))
	}
	appendTranscript(t, path, transcriptLine(t, turn("user", "Review the new commits.")))
	g.setJob(sid, "done", "") // it died before writing a word
	g.passes(t, 1)
	if s := g.state(t, 1); s.State != stRunning || s.Kind != kindRetry {
		t.Errorf("defect D: the round that wrote nothing settled as %s at %s on the first round's line, not retried", describe(s), s.Head)
	}
}

// A retry reads from its own launch: a retry that wrote nothing does not
// read the words of the attempt it retried.
func TestRetryReadsFromItsOwnLaunch(t *testing.T) {
	g := newGenWorldT(t, 1)
	dir := t.TempDir()
	sid := g.prs[0].RoundSessionID()
	if err := os.MkdirAll(filepath.Join(dir, "projects", "p"), 0o700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "projects", "p", sid.String()+".jsonl")
	var read []string
	g.h.lastMessage = func(id ids.SessionID, from int64) (string, error) {
		msg, err := LastMessage(dir, id, from)
		read = append(read, msg)
		return msg, err
	}
	g.h.transcriptSize = func(id ids.SessionID) int64 { return TranscriptSize(dir, id) }
	g.mustTick(t)
	appendTranscript(t, path, transcriptLine(t, turn("user", "Review the PR.")), transcriptLine(t, turn("assistant", "Reading the diff.")))
	g.setJob(sid, "done", "") // cut off before its summary
	g.passes(t, 1)
	if s := g.state(t, 1); s.State != stRunning || s.Kind != kindRetry {
		t.Fatalf("the cut-off round was not retried: %s", describe(s))
	}
	appendTranscript(t, path, transcriptLine(t, turn("user", "Continue.")))
	g.setJob(sid, "done", "") // the retry dies before writing
	g.passes(t, 1)
	if want := []string{"Reading the diff.", ""}; !slices.Equal(read, want) {
		t.Errorf("read %q, want %q: the retry read the attempt before it", read, want)
	}
	if s := g.state(t, 1); s.State != stStuck || s.Kind != kindFailed {
		t.Errorf("a retry that wrote nothing settled as %s, want stuck{failed_twice}", describe(s))
	}
}

// A transcript that cannot be read leaves the round unread for the next
// pass; the third failure reads it, and the PR is stuck with the reason and
// notifies, without failing the tick.
func TestUnreadableTranscriptIsStuckOnTheThirdPass(t *testing.T) {
	g := newGenWorldT(t, 1)
	sid := g.prs[0].RoundSessionID()
	g.h.lastMessage = func(ids.SessionID, int64) (string, error) { return "", errTranscriptShrunk }
	g.mustTick(t)
	g.setJob(sid, "done", "")
	for pass := 1; pass < maxUnreadable; pass++ {
		g.passes(t, 1)
		if s := g.state(t, 1); s.State != stRunning || g.unread(t, 1) != 1 {
			t.Fatalf("pass %d: %s with %d unread rounds, want running over its unread round", pass, describe(s), g.unread(t, 1))
		}
	}
	rounds, err := g.h.ledger.UnreadRounds()
	if err != nil || len(rounds) != 1 || rounds[0].Unreadable != maxUnreadable-1 {
		t.Fatalf("unread rounds %+v (%v), want one counted unreadable %d times", rounds, err, maxUnreadable-1)
	}
	g.notified = nil
	g.passes(t, 1)
	s := g.state(t, 1)
	if s.State != stStuck || s.Kind != kindFailed || s.Detail != noteUnreadable || g.unread(t, 1) != 0 {
		t.Fatalf("third unreadable pass: %s (%q), %d unread, want stuck{failed_twice} %q, read", describe(s), s.Detail, g.unread(t, 1), noteUnreadable)
	}
	if want := []string{g.prs[0].URL() + " needs you: " + noteUnreadable}; !slices.Equal(g.notified, want) {
		t.Errorf("notified %q, want %q", g.notified, want)
	}
}

// The launch crash: startOne records the round (StartRound, tick.go:752)
// and moves the state only after the worker is launched and listed. A
// crash in between leaves no record, and the next pass launches the same
// review again.
func TestDefectLaunchCrashStartsTheSameReviewTwice(t *testing.T) {
	t.Skip("launch crash: a crash after the launch and before StartRound launches the same review again; unskip when plan step 6 lands")
	g := newGenWorldT(t, 1)
	g.crash.arm(crashAfterLaunch, 1)
	g.mustTick(t)
	if !g.crashed {
		t.Fatal("no launch crashed")
	}
	msg, _ := g.message(1, "nothing")
	g.finish(1, msg, "") // the unrecorded round runs to its end
	g.passes(t, 2)
	key := g.prs[0].URL() + " " + g.gh.head[1]
	if n := g.launches[key]; n != 1 {
		t.Errorf("launch crash: %d rounds launched for the same request at the same head, want 1", n)
	}
}

// The launch crash after StartRound: the owed move never stores running,
// so the state before stays over the new round, an Approve at the old
// head included.
func TestDefectLaunchCrashLeavesTheOldApproveOverTheNewRound(t *testing.T) {
	t.Skip("launch crash: a crash between StartRound and the owed move leaves Approve at the old head; unskip when plan step 6 lands")
	g := newGenWorldT(t, 1)
	g.mustTick(t)
	msg, _ := g.message(1, "approve at head")
	g.finish(1, msg, "")
	g.passes(t, 1)
	if s := g.state(t, 1); s.State != stAwaiting || s.Kind != kindApprove {
		t.Fatalf("no Approve to start from: %s", describe(s))
	}
	g.gh.head[1], g.update[1] = g.sha(), g.now // a push: a round is owed
	g.crash.arm("pr_state", 1)                 // the owed move's write
	g.mustTick(t)
	if !g.crashed || g.unread(t, 1) != 1 {
		t.Fatalf("the crash did not land between StartRound and the move: crashed %v, unread %d", g.crashed, g.unread(t, 1))
	}
	g.passes(t, 2)
	if err := g.approveAtHead(); err != nil {
		t.Errorf("launch crash: %v, over a round running at the head", err)
	}
}

// The unlocked migration: every OpenLedger ran migratePRState, which gives
// each accepted PR without a state one. A closed PR keeps its acceptance
// and loses its state; while its row's receipt is still there (elly did
// not take the DELETE), any process that opened the ledger re-created a
// state for it from that row. Now only tick migrates, under the PR's lock,
// and only until its first complete pass sets metaStatesMigrated.
func TestDefectMigrationRecreatesAClosedPRsState(t *testing.T) {
	g := newGenWorldT(t, 1)
	pr := g.prs[0]
	g.mustTick(t)
	msg, _ := g.message(1, "approve at head")
	g.finish(1, msg, "")
	g.passes(t, 1)
	if s := g.state(t, 1); s.State != stAwaiting || s.Kind != kindApprove {
		t.Fatalf("no Approve to start from: %s", describe(s))
	}
	g.gh.open[1] = false
	g.elly.delErr = errors.New("connection refused") // elly is restarting
	g.passes(t, 1)
	if _, ok, err := g.h.ledger.PRState(pr); err != nil || ok {
		t.Fatalf("the closed PR's state was not deleted: %v %v", ok, err)
	}
	if err := g.restart(); err != nil { // the next tick, a click, a round's post verb
		t.Fatal(err)
	}
	if s, ok, err := g.h.ledger.PRState(pr); err != nil || ok {
		t.Errorf("unlocked migration: opening the ledger re-created %s for a closed PR (err %v)", describe(s), err)
	}
}

// Found by the generator: a retry decided over a state other than running
// moved it with that state's evEnded rule, which marked the round read
// (from a question, from idle). The retry's running{retry} then had no
// unread round, and once its worker ended nothing settled it. Every
// state an ended round moves now has a retry rule that reads nothing.
func TestDefectRetryOverAQuestionReadsItsRound(t *testing.T) {
	g := newGenWorldT(t, 1)
	sid := g.prs[0].RoundSessionID()
	g.mustTick(t)
	g.setJob(sid, "blocked", "which config?")
	g.passes(t, 1)
	if s := g.state(t, 1); s.State != stAwaiting || s.Kind != kindQuestion {
		t.Fatalf("no question: %s", describe(s))
	}
	g.setJob(sid, "working", "") // answered in the session
	g.finish(1, "", "")          // and cut off before its summary
	g.passes(t, 1)
	g.finish(1, "", "") // the retry ends too
	g.passes(t, 3)
	if s := g.state(t, 1); s.State == stRunning {
		t.Errorf("retry over a question: %s with %d unread rounds and no worker after 3 passes", describe(s), g.unread(t, 1))
	}
}
