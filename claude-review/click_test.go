package main

import (
	"errors"
	"html"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/chelmertz/dotfiles/claude-review/ids"
)

const reviewRepoCfg = `{"review_repos":["acme/*"],"github_user":"me","elly_url":"http://127.0.0.1:1"}`

// clickHost is a click on a PR in review_repos: a ledger under a temp home,
// gh answering with an open PR at h7, and a fake roster where the
// background launch lists a worker for the PR's id.
func clickHost(t *testing.T, exists bool) (*fakeHost, *fakeSessions) {
	t.Helper()
	h := newHost(t, reviewRepoCfg, true, exists)
	h.d.Home = t.TempDir()
	h.d.StateDir = h.d.Home + "/.local/state" // the ledger stays in the test's dir
	h.d.Gh = func(args ...string) (string, error) {
		h.ghCalls = append(h.ghCalls, args)
		if args[1] == paginate {
			return "[]", nil
		}
		return `{"state":"open","head":{"sha":"h7"},"base":{"ref":"main"}}`, nil
	}
	fs := newFakeSessions()
	fs.saved = map[ids.SessionID][]string{}
	h.d.Sessions = fs.sessions()
	h.d.Trust = func(dir string) error { fs.events = append(fs.events, "trust "+dir); return nil }
	h.d.Output = func(dir string, argv []string) (string, error) {
		h.outputs = append(h.outputs, argv)
		if strings.Contains(argv[len(argv)-1], "'--bg'") {
			fs.events = append(fs.events, "launch")
			fs.workers = append(fs.workers, workerFor(testPR.RoundSessionID(), 700))
			if _, ok := fs.saved[testPR.RoundSessionID()]; !ok && strings.Contains(argv[len(argv)-1], "'--allowedTools'") {
				fs.saved[testPR.RoundSessionID()] = PolicyFlags(testPR, upstream)
			}
		}
		return "", h.outErr
	}
	return h, fs
}

func (h *fakeHost) ledger(t *testing.T) *Ledger {
	t.Helper()
	l, err := OpenLedger(LedgerPath(h.d.StateDir))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = l.Close() })
	return l
}

func notified(h *fakeHost) string {
	var all []string
	for _, argv := range append(append([][]string{}, h.outputs...), h.spawns...) {
		if argv[0] == "notify-send" {
			all = append(all, argv[len(argv)-1])
		}
	}
	return strings.Join(all, "\n")
}

// A click on a PR in review_repos never opens a window: the round runs in
// the PR's background worker, under the pinned policy, and is recorded so
// tick settles it.
func TestClickOnAReviewRepoRunsInTheBackground(t *testing.T) {
	for _, exists := range []bool{true, false} {
		h, fs := clickHost(t, exists)
		if code := run([]string{target}, h.d); code != 0 {
			t.Fatalf("exists=%v: exit %d: %s", exists, code, h.stderr)
		}
		want := viaInteractiveShell(shellLine(cloneOf, RoundArgs(h.cfg(t), testPR, upstream, exists)))
		if !slices.ContainsFunc(h.outputs, func(argv []string) bool { return slices.Equal(argv, want) }) {
			t.Errorf("exists=%v: no pinned background launch in %q", exists, h.outputs)
		}
		if bootstrapped := slices.ContainsFunc(h.outputs, func(argv []string) bool { return slices.Equal(argv, roundBootstrapLine()) }); bootstrapped == exists {
			t.Errorf("exists=%v: bootstrap ran %v", exists, bootstrapped)
		}
		if len(h.scripts)+len(h.execs) != 0 || slices.ContainsFunc(h.spawns, func(a []string) bool { return a[0] == ghostty }) {
			t.Errorf("exists=%v: a window opened: %q %q", exists, h.scripts, h.spawns)
		}
		if got := strings.Join(fs.events, ","); got != "trust "+cloneOf+",launch" {
			t.Errorf("exists=%v: events %s", exists, got)
		}
		if n := notified(h); n != "review of acme/widgets/pull/42 started (first handover)" {
			t.Errorf("exists=%v: notified %q", exists, n)
		}
		rounds, _ := h.ledger(t).UnreadRounds()
		if len(rounds) != 1 || rounds[0].Head != "h7" || !strings.HasPrefix(rounds[0].Trigger, "click@") {
			t.Errorf("exists=%v: rounds %+v", exists, rounds)
		}
	}
}

// A click on a merged PR starts nothing, records no acceptance and says so.
func TestClickOnAClosedPRStartsNothing(t *testing.T) {
	h, fs := clickHost(t, true)
	h.d.Gh = func(...string) (string, error) {
		return `{"state":"closed","merged":true,"head":{"sha":"h7"},"base":{"ref":"main"}}`, nil
	}
	if code := run([]string{target}, h.d); code == 0 {
		t.Errorf("exit 0 on a merged PR")
	}
	if slices.Contains(fs.events, "launch") {
		t.Errorf("launched on a merged PR: %q", fs.events)
	}
	if n := notified(h); n != "acme/widgets/pull/42 is merged/closed; no round" {
		t.Errorf("notified %q", n)
	}
	if _, ok, _ := h.ledger(t).Acceptance(testPR); ok {
		t.Error("a merged PR was accepted for unattended rounds")
	}
}

func TestClickMidRoundStartsNothing(t *testing.T) {
	h, fs := clickHost(t, true)
	fs.workers = []Worker{workerFor(testPR.RoundSessionID(), 500)}
	fs.states[testPR.RoundSessionID()] = "working"
	if code := run([]string{target}, h.d); code != 0 {
		t.Fatalf("exit %d: %s", code, h.stderr)
	}
	if slices.Contains(fs.events, "launch") {
		t.Errorf("launched beside a running round: %q", fs.events)
	}
	if n := notified(h); n != "acme/widgets/pull/42 is mid-round; it continues in the background" {
		t.Errorf("notified %q", n)
	}
	if rounds, _ := h.ledger(t).UnreadRounds(); len(rounds) != 0 {
		t.Errorf("rounds %+v", rounds)
	}
}

// The idle worker of an unread round is stopped for the click's round only
// after that round was settled.
func TestClickSettlesTheLastRoundBeforeStoppingItsWorker(t *testing.T) {
	h, fs := clickHost(t, true)
	if _, err := h.ledger(t).StartRound(testPR, "h6", "new_head", t0); err != nil {
		t.Fatal(err)
	}
	fs.workers = []Worker{workerFor(testPR.RoundSessionID(), 500)}
	fs.states[testPR.RoundSessionID()] = "done"
	if code := run([]string{target}, h.d); code != 0 {
		t.Fatalf("exit %d: %s", code, h.stderr)
	}
	rounds, _ := h.ledger(t).UnreadRounds()
	if len(rounds) != 1 || rounds[0].Head != "h7" {
		t.Errorf("the old round was not settled before the new one: %+v", rounds)
	}
	if got := strings.Join(fs.events, ","); !strings.HasSuffix(got, "stop "+testPR.RoundSessionID().Key().String()+",launch") {
		t.Errorf("events %s", got)
	}

	// GitHub down: the round cannot be read, so its worker stays.
	h, fs = clickHost(t, true)
	if _, err := h.ledger(t).StartRound(testPR, "h6", "new_head", t0); err != nil {
		t.Fatal(err)
	}
	fs.workers = []Worker{workerFor(testPR.RoundSessionID(), 500)}
	fs.states[testPR.RoundSessionID()] = "done"
	gh := h.d.Gh
	h.d.Gh = func(args ...string) (string, error) {
		if strings.HasSuffix(args[len(args)-1], "pulls/42") && len(h.ghCalls) > 0 {
			return "", errors.New("502")
		}
		return gh(args...)
	}
	if code := run([]string{target}, h.d); code != 1 {
		t.Errorf("exit %d", code)
	}
	if slices.ContainsFunc(fs.events, func(e string) bool { return strings.HasPrefix(e, "stop") || e == "launch" }) {
		t.Errorf("stopped an unsettled round's worker: %q", fs.events)
	}
	if !strings.Contains(notified(h), "502") {
		t.Errorf("notified %q", notified(h))
	}
}

func TestClickRefusedWhileTheBreakerIsTripped(t *testing.T) {
	h, fs := clickHost(t, true)
	if err := h.ledger(t).Trip("self-check failed"); err != nil {
		t.Fatal(err)
	}
	if code := run([]string{target}, h.d); code != 1 || slices.Contains(fs.events, "launch") {
		t.Errorf("exit %d, events %q", code, fs.events)
	}
	if !strings.Contains(notified(h), "self-check failed") {
		t.Errorf("notified %q", notified(h))
	}
}

func TestClickBesideALegacyWindowStartsNothing(t *testing.T) {
	h, fs := clickHost(t, true)
	fs.window = 4242
	if code := run([]string{target}, h.d); code != 1 || slices.Contains(fs.events, "launch") {
		t.Errorf("exit %d, events %q", code, fs.events)
	}
	if !strings.Contains(notified(h), windowNote) {
		t.Errorf("notified %q", notified(h))
	}
}

func TestClickDryRunShowsTheBackgroundRound(t *testing.T) {
	h, _ := clickHost(t, true)
	if code := run([]string{dryRunFlag, target}, h.d); code != 0 {
		t.Fatalf("exit %d: %s", code, h.stderr)
	}
	if want := shellLine(cloneOf, RoundArgs(h.cfg(t), testPR, upstream, true)) + "\n"; h.stdout.String() != want {
		t.Errorf("dry run:\n%s\nwant\n%s", h.stdout, want)
	}
	if len(h.outputs) != 0 {
		t.Errorf("a dry run ran %q", h.outputs)
	}
}

func roundBootstrapLine() []string {
	return viaInteractiveShell(shellLine(cloneOf, RoundBootstrapArgs(testPR)))
}

// A click continues the round session flagless once it saved the policy,
// and starts nothing in one saved under other options.
func TestClickResumesFlaglessUnderTheSavedPolicy(t *testing.T) {
	h, fs := clickHost(t, true)
	fs.saved[testPR.RoundSessionID()] = PolicyFlags(testPR, upstream)
	if code := run([]string{target}, h.d); code != 0 {
		t.Fatalf("exit %d: %s", code, h.stderr)
	}
	want := viaInteractiveShell(shellLine(cloneOf, ResumeArgs(h.cfg(t), testPR, upstream, true)))
	if !slices.ContainsFunc(h.outputs, func(argv []string) bool { return slices.Equal(argv, want) }) {
		t.Errorf("no flagless resume in %d commands", len(h.outputs))
	}
	h, fs = clickHost(t, true)
	fs.saved[testPR.RoundSessionID()] = []string{"--permission-mode", "auto"}
	if code := run([]string{target}, h.d); code != 1 || slices.Contains(fs.events, "launch") {
		t.Errorf("exit %d, events %q", code, fs.events)
	}
	if !strings.Contains(notified(h), "other options") {
		t.Errorf("notified %q", notified(h))
	}
}

// A click on a PR handed over before says so, with what came of it.
func TestClickNotificationStatesTheHandoverHistory(t *testing.T) {
	h, _ := clickHost(t, true)
	l := h.ledger(t)
	for i, at := range []time.Time{t0, t0.Add(26 * time.Hour)} {
		id, err := l.StartRound(testPR, "h"+strconv.Itoa(i), "new_head", at)
		if err != nil {
			t.Fatal(err)
		}
		if err := l.MarkRead(id); err != nil {
			t.Fatal(err)
		}
	}
	note := "the round has a question for you: " + strings.Repeat("ö", 100)
	if err := l.SetAnnotation(testPR, Annotation{State: stateNeedsYou, Note: note}); err != nil {
		t.Fatal(err)
	}
	if code := run([]string{target}, h.d); code != 0 {
		t.Fatalf("exit %d: %s", code, h.stderr)
	}
	want := html.EscapeString("review of acme/widgets/pull/42 started: handed over Oct 31, 2 rounds, last: " + string([]rune(note)[:79]) + "…")
	if n := notified(h); n != want {
		t.Errorf("notified\n%q, want\n%q", n, want)
	}
}

func TestHandoverWords(t *testing.T) {
	for _, c := range []struct {
		h    Handover
		want string
	}{
		{Handover{}, " (first handover)"},
		{Handover{First: t0, Rounds: 1}, ": handed over Oct 31, 1 round"},
		{Handover{First: t0, Rounds: 3, LastNote: "round posted its review"}, ": handed over Oct 31, 3 rounds, last: round posted its review"},
	} {
		if got := c.h.words(); got != c.want {
			t.Errorf("%+v: got %q, want %q", c.h, got, c.want)
		}
	}
}

// A click logs how long each stage took, so a slow click names its stage:
// the bootstrap only when the round session is new. The clock advances one
// second per reading.
func TestClickLogsItsStageTimings(t *testing.T) {
	for _, exists := range []bool{true, false} {
		h, _ := clickHost(t, exists)
		var tick time.Duration
		h.d.Now = func() time.Time { tick += time.Second; return time.Unix(0, 0).Add(tick) }
		if code := run([]string{target}, h.d); code != 0 {
			t.Fatalf("exists=%v: exit %d: %s", exists, code, h.stderr)
		}
		want := "click timing: accept 1.0s prepare 1.0s bootstrap 1.0s clear 1.0s launch 1.0s listed 1.0s total 6.0s"
		if exists {
			want = "click timing: accept 1.0s prepare 1.0s clear 1.0s launch 1.0s listed 1.0s total 5.0s"
		}
		if !strings.Contains(h.stderr.String(), testPR.URL()+": "+want+"\n") {
			t.Errorf("exists=%v: stderr lacks %q:\n%s", exists, want, h.stderr)
		}
	}
}
