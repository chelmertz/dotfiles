package main

import (
	"errors"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"database/sql"
)

// wantRunning checks pr's ledger row: running, naming who started it, with
// the attach link and an expiry one round lifetime after at.
func wantRunning(t *testing.T, l *Ledger, pr PR, by string, at time.Time) {
	t.Helper()
	a, ok, err := l.Annotation(pr)
	if err != nil || !ok {
		t.Fatalf("no annotation: %v", err)
	}
	if a.State != stateRunning || a.Note != "started by "+by {
		t.Errorf("annotation %+v, want running started by %s", a, by)
	}
	if !slices.ContainsFunc(a.Links, func(l Link) bool { return l.URL == verbURL(pr, "attach") }) {
		t.Errorf("links %+v lack the attach verb", a.Links)
	}
	if !a.Expires.Equal(at.Add(roundLifetime)) {
		t.Errorf("expires %v, want %v", a.Expires, at.Add(roundLifetime))
	}
}

// A round tick starts shows in elly at once, expiring one round lifetime
// after its launch, and never notifies.
func TestTickMarksAStartedRoundRunning(t *testing.T) {
	w := newTickWorld(t)
	pr := w.accept(t, 42)
	w.elly.prs = []EllyPR{requestedPR(42)}
	if err := w.h.tick(); err != nil {
		t.Fatal(err)
	}
	wantRunning(t, w.h.ledger, pr, "tick", t0)
	if a := w.elly.put[pr.URL()]; a.State != stateRunning {
		t.Errorf("elly got %+v", a)
	}
	if got := w.elly.expires[pr.URL()]; !got.Equal(t0.Add(roundLifetime)) {
		t.Errorf("elly expiry %v, want the round's lifetime, not a refresh's", got)
	}
	if len(w.notified) != 0 {
		t.Errorf("running notified %q", w.notified)
	}
}

func TestTickMarksARetryRunning(t *testing.T) {
	w := newTickWorld(t)
	pr := w.accept(t, 1)
	if _, err := w.h.ledger.StartRound(pr, "h1", "new_head", t0.Add(-time.Hour)); err != nil {
		t.Fatal(err)
	}
	w.gh.facts = RoundFacts{State: "open", Head: "h1"} // nothing to show: retried
	if err := w.h.tick(); err != nil {
		t.Fatal(err)
	}
	if len(w.started) != 1 {
		t.Fatalf("no retry started: %q", w.started)
	}
	wantRunning(t, w.h.ledger, pr, "tick (retry)", t0)
}

// Settling the round replaces running with its outcome.
func TestTickSettleReplacesRunning(t *testing.T) {
	w := newTickWorld(t)
	pr := w.accept(t, 42)
	w.elly.prs = []EllyPR{requestedPR(42)}
	if err := w.h.tick(); err != nil {
		t.Fatal(err)
	}
	w.elly.prs = nil
	w.msgs[pr.RoundSessionID()] = "Which one?\nclaude-review: question for the user"
	w.gh.facts = RoundFacts{State: "open", Head: "h1"}
	w.fs.states[pr.RoundSessionID()] = "done"
	if err := w.h.tick(); err != nil {
		t.Fatal(err)
	}
	if a, _, _ := w.h.ledger.Annotation(pr); a.State != stateNeedsYou || !a.Expires.IsZero() {
		t.Errorf("settled annotation %+v, want needs_you on the refresh's expiry", a)
	}
	if w.elly.put[pr.URL()].State != stateNeedsYou {
		t.Errorf("elly still shows %+v", w.elly.put[pr.URL()])
	}
}

// A launch that fails shows no round running, also when the last round's
// running row is still there.
func TestTickAFailedLaunchLeavesNoRunning(t *testing.T) {
	w := newTickWorld(t)
	pr := w.accept(t, 1)
	if _, err := w.h.ledger.StartRound(pr, "h1", "new_head", t0.Add(-time.Hour)); err != nil {
		t.Fatal(err)
	}
	if err := w.h.markRunning(pr, "tick", t0.Add(-time.Hour)); err != nil {
		t.Fatal(err)
	}
	w.elly.put = map[string]Annotation{}               // what the earlier launch sent
	w.gh.facts = RoundFacts{State: "open", Head: "h1"} // retried
	w.h.start = func(string, []string) error { return errors.New("claude: not found") }
	fresh := w.accept(t, 2)
	w.elly.prs = []EllyPR{requestedPR(2)}
	if err := w.h.tick(); err != nil {
		t.Fatal(err)
	}
	for _, p := range []PR{pr, fresh} {
		if a, ok, _ := w.h.ledger.Annotation(p); ok && a.State == stateRunning {
			t.Errorf("%s: a failed launch left %+v", p.Slug(), a)
		}
		if a, ok := w.elly.put[p.URL()]; ok && a.State == stateRunning {
			t.Errorf("%s: elly shows %+v", p.Slug(), a)
		}
	}
	if !slices.Contains(w.elly.deleted, pr.URL()) {
		t.Errorf("the stale running row was not deleted from elly: %q", w.elly.deleted)
	}
}

// An elly that does not know running refuses it; the round runs and is
// recorded all the same.
func TestTickRoundRunsWhenEllyRefusesRunning(t *testing.T) {
	w := newTickWorld(t)
	pr := w.accept(t, 42)
	w.elly.reject = stateRunning
	w.elly.prs = []EllyPR{requestedPR(42)}
	if err := w.h.tick(); err != nil {
		t.Fatalf("a refused running row failed the tick: %v", err)
	}
	if len(w.started) != 1 {
		t.Fatalf("started %q", w.started)
	}
	if rounds, _ := w.h.ledger.UnreadRounds(); len(rounds) != 1 {
		t.Errorf("the round was not recorded: %+v", rounds)
	}
	if !strings.Contains(w.out.String(), "400") {
		t.Errorf("the refusal was not logged: %q", w.out.String())
	}
	// Kept, so the first refresh after elly learns the state sends it.
	if a, _, _ := w.h.ledger.Annotation(pr); a.State != stateRunning {
		t.Errorf("the ledger lost the running row: %+v", a)
	}
}

// refresh re-sends running with the expiry it was given, never extends it,
// and drops it once that has passed.
func TestTickRefreshKeepsTheRoundLifetime(t *testing.T) {
	w := newTickWorld(t)
	pr := w.accept(t, 42)
	at := t0.Add(-time.Hour)
	if err := w.h.markRunning(pr, "a click", at); err != nil {
		t.Fatal(err)
	}
	w.live[pr.RoundSessionID()] = true
	if err := w.h.tick(); err != nil {
		t.Fatal(err)
	}
	if got := w.elly.expires[pr.URL()]; !got.Equal(at.Add(roundLifetime)) {
		t.Errorf("refresh sent expiry %v, want %v", got, at.Add(roundLifetime))
	}
	w.h.now = func() time.Time { return at.Add(roundLifetime + time.Second) }
	w.elly.put = map[string]Annotation{}
	if err := w.h.tick(); err != nil {
		t.Fatal(err)
	}
	if _, ok := w.elly.put[pr.URL()]; ok {
		t.Error("an expired running row was sent again")
	}
	if _, ok, _ := w.h.ledger.Annotation(pr); ok {
		t.Error("the ledger still holds an expired running row")
	}
}

// The sweep that stops a closed PR's worker clears its running row.
func TestTickSweepClearsRunningOnAClosedPR(t *testing.T) {
	w := newTickWorld(t)
	w.gh.closedPRs = map[int]bool{}
	w.gh.facts = RoundFacts{State: "open", Head: "h1"}
	pr := w.closedWithWorker(t, 43, 502)
	if err := w.h.markRunning(pr, "a click", t0); err != nil {
		t.Fatal(err)
	}
	if err := w.h.tick(); err != nil {
		t.Fatal(err)
	}
	if _, ok, _ := w.h.ledger.Annotation(pr); ok {
		t.Error("the closed PR still holds its running row")
	}
	if !slices.Contains(w.elly.deleted, pr.URL()) {
		t.Errorf("elly still shows it: deleted %q", w.elly.deleted)
	}
}

func TestClickMarksItsRoundRunning(t *testing.T) {
	h, _ := clickHost(t, true)
	before := time.Now()
	if code := run([]string{target}, h.d); code != 0 {
		t.Fatalf("exit %d: %s", code, h.stderr)
	}
	a, ok, err := h.ledger(t).Annotation(testPR)
	if err != nil || !ok || a.State != stateRunning || a.Note != "started by a click" {
		t.Fatalf("annotation %+v %v", a, err)
	}
	if a.Expires.Before(before.Add(roundLifetime).Truncate(time.Second)) || a.Expires.After(time.Now().Add(roundLifetime)) {
		t.Errorf("expires %v, want one round lifetime after the launch", a.Expires)
	}
	// elly is down in this host: the click still succeeded, and says why
	// elly shows nothing.
	if !strings.Contains(h.stderr.String(), "elly") {
		t.Errorf("stderr %q does not report the failed send", h.stderr.String())
	}
}

func TestClickAFailedLaunchLeavesNoRunning(t *testing.T) {
	h, _ := clickHost(t, true)
	if err := (tickHost{ledger: h.ledger(t)}).markRunning(testPR, "tick", t0); err != nil {
		t.Fatal(err)
	}
	h.outErr = errors.New("claude: not found")
	if code := run([]string{target}, h.d); code == 0 {
		t.Fatal("a failed launch exited 0")
	}
	if a, ok, _ := h.ledger(t).Annotation(testPR); ok && a.State == stateRunning {
		t.Errorf("a failed click launch left %+v", a)
	}
}

func TestLedgerKeepsAnAnnotationsExpiry(t *testing.T) {
	l := openTestLedger(t)
	exp := t0.Add(2 * time.Hour)
	if err := l.SetAnnotation(widgetsPR, Annotation{State: stateRunning, Expires: exp}); err != nil {
		t.Fatal(err)
	}
	if a, _, _ := l.Annotation(widgetsPR); !a.Expires.Equal(exp) {
		t.Errorf("expires %v, want %v", a.Expires, exp)
	}
	if err := l.SetAnnotation(widgetsPR, Annotation{State: stateHandled}); err != nil {
		t.Fatal(err)
	}
	if a, _, _ := l.Annotation(widgetsPR); !a.Expires.IsZero() {
		t.Errorf("a refresh-managed annotation reads back with expiry %v", a.Expires)
	}
}

// A ledger written before annotations had an expiry opens and keeps its rows.
func TestLedgerAddsTheExpiryColumn(t *testing.T) {
	path := filepath.Join(t.TempDir(), "ledger.db")
	old, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := old.Exec(`create table annotations (pr text primary key, state text not null, note text not null, links text not null);
		insert into annotations values (?, 'handled', 'old', '[]')`, widgetsPR.URL()); err != nil {
		t.Fatal(err)
	}
	_ = old.Close()
	for range 2 {
		l, err := OpenLedger(path)
		if err != nil {
			t.Fatal(err)
		}
		all, err := l.Annotations()
		_ = l.Close()
		if err != nil || len(all) != 1 || !all[widgetsPR.URL()].Expires.IsZero() {
			t.Fatalf("got %+v %v", all, err)
		}
	}
}
