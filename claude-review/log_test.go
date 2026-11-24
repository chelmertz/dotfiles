package main

import (
	"os"
	"slices"
	"strings"
	"testing"
	"time"
)

// logHost is a verb host whose ledger exists, with the PR state given.
func logHost(t *testing.T, s PRState) *fakeHost {
	t.Helper()
	h := verbHost(t)
	l, err := OpenLedger(LedgerPath(h.d.StateDir))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = l.Close() }()
	if err := l.SetPRState(widgetsPR, s); err != nil {
		t.Fatal(err)
	}
	return h
}

// commitSteps commits one step per event on pr, a minute apart from at.
func commitSteps(t *testing.T, l *Ledger, pr PR, at time.Time, events ...Event) {
	t.Helper()
	for i, e := range events {
		s, ok, err := l.PRState(pr)
		if err != nil {
			t.Fatal(err)
		}
		if !ok {
			s = PRState{State: stIdle}
		}
		p, ok := planStep(s, at.Add(time.Duration(i)*time.Minute), e)
		if !ok {
			t.Fatalf("no rule for %s from %s", e.Kind, describe(s))
		}
		p.origin = originTick
		if err := l.Commit(freeLock(t, pr), pr, p); err != nil {
			t.Fatal(err)
		}
	}
}

// log prints the PR's decisions oldest first, one line each, and none of
// another PR's.
func TestLogPrintsAPRsDecisionsOldestFirst(t *testing.T) {
	h := verbHost(t)
	l, err := OpenLedger(LedgerPath(h.d.StateDir))
	if err != nil {
		t.Fatal(err)
	}
	other := PR{Owner: "acme", Repo: "widgets", Number: 43}
	commitSteps(t, l, widgetsPR, t0, Event{Kind: evOwed, Head: sha, Trigger: Requested{At: t0}}, Event{Kind: evClosed})
	commitSteps(t, l, other, t0.Add(-time.Hour), Event{Kind: evOwed, Head: "beef", Trigger: Requested{At: t0}})
	_ = l.Close()
	if code := run([]string{"log", widgetsPR.URL()}, h.d); code != 0 {
		t.Fatalf("exit %d: %s", code, h.stderr)
	}
	lines := strings.Split(strings.TrimSuffix(h.stdout.String(), "\n"), "\n")
	if len(lines) != 2 {
		t.Fatalf("want 2 lines, got %q", h.stdout)
	}
	first := "2026-10-31T12:00:00.000Z tick decided owed idle → running{tick} [] owed {"
	if !strings.HasPrefix(lines[0], first) || !strings.Contains(lines[0], sha) {
		t.Errorf("first line %q, want it to start %q and carry the owed facts", lines[0], first)
	}
	if lines[1] != "2026-10-31T12:01:00.000Z tick decided closed running{tick} → deleted [stop_workers,rounds_read] close {}" {
		t.Errorf("second line %q", lines[1])
	}
	if strings.Contains(h.stdout.String(), "beef") {
		t.Errorf("another PR's decision printed: %q", h.stdout)
	}
}

// Whatever reaches a column, log prints no control character: a row is
// data, and the terminal it prints to would act on one.
func TestLogEscapesControlCharacters(t *testing.T) {
	h := verbHost(t)
	l, err := OpenLedger(LedgerPath(h.d.StateDir))
	if err != nil {
		t.Fatal(err)
	}
	_, err = l.db.Exec(`insert into decisions (pr, at_ms, origin, build, step, effects, facts_kind, facts)
		values (?, ?, 'tick', ?, 'global', '[]', 'close', ?)`, widgetsPR.URL(), t0.UnixMilli(), "dev\x1b]0;x\a", "{\"x\":\"\x1b[31mred\u202e\"}")
	_ = l.Close()
	if err != nil {
		t.Fatal(err)
	}
	if code := run([]string{"log", widgetsPR.URL()}, h.d); code != 0 {
		t.Fatalf("exit %d: %s", code, h.stderr)
	}
	out := strings.TrimSuffix(h.stdout.String(), "\n")
	if strings.ContainsAny(out, "\x1b\a\n\u202e") {
		t.Errorf("a control character reached the output: %q", out)
	}
	if !strings.Contains(out, `\x1b[31mred`) {
		t.Errorf("the escaped text is gone: %q", out)
	}
}

// log is a human verb: from inside a Claude Code session it refuses,
// before it opens anything.
func TestLogRefusesInsideAClaudeSession(t *testing.T) {
	h := logHost(t, PRState{State: stIdle})
	for _, marker := range []string{"CLAUDECODE=1", "CLAUDE_CODE_SESSION_ID=abc"} {
		h.stdout.Reset()
		h.stderr.Reset()
		h.d.Environ = []string{"PATH=/bin", marker}
		if code := run([]string{"log", widgetsPR.URL()}, h.d); code == 0 || h.stdout.Len() != 0 {
			t.Errorf("%s: exit %d, stdout %q", marker, code, h.stdout)
		}
		if !strings.Contains(h.stderr.String(), "Claude Code session") {
			t.Errorf("%s: the refusal does not say why: %q", marker, h.stderr)
		}
	}
}

// log reads the ledger read-only: a missing one is an error, never created.
func TestLogNeverCreatesTheLedger(t *testing.T) {
	h := verbHost(t)
	if code := run([]string{"log", widgetsPR.URL()}, h.d); code == 0 {
		t.Errorf("exit 0 without a ledger: %q", h.stdout)
	}
	if _, err := os.Stat(LedgerPath(h.d.StateDir)); !os.IsNotExist(err) {
		t.Errorf("log created the ledger: %v", err)
	}
}

// A round and a fork are launched with every marker stripped (cleanEnv),
// and Claude Code sets its own for each Bash call it runs. Which of them a
// background round or a fork sets is not pinned anywhere, so log refuses on
// any one of them, spelled out here: a test ranging over sessionMarkers
// would pass through a marker dropped from it. The launch's environment
// alone (no marker) is a terminal's, and log runs there.
func TestLogRefusesTheEnvironmentRoundsAndForksRunIn(t *testing.T) {
	h := logHost(t, PRState{State: stIdle})
	parent := []string{"PATH=/bin", "HOME=/home/u", "GH_TOKEN=x", "CLAUDECODE=1", "CLAUDE_PID=9",
		"CLAUDE_CODE_CHILD_SESSION=1", "CLAUDE_CODE_SESSION_ID=s", "CLAUDE_CODE_SESSION_ATTENDED=1",
		"CLAUDE_CODE_ENTRYPOINT=cli", "CLAUDE_CODE_EXECPATH=/bin/claude",
		"CLAUDE_CODE_MESSAGING_SOCKET=/run/s", "CLAUDE_CODE_MESSAGING_TOKEN=t"}
	launched := cleanEnv(parent)
	h.d.Environ = launched
	if code := run([]string{"log", widgetsPR.URL()}, h.d); code != 0 {
		t.Fatalf("the launch environment without markers: exit %d: %s", code, h.stderr)
	}
	for _, marker := range parent[3:] {
		h.stdout.Reset()
		h.stderr.Reset()
		h.d.Environ = append(slices.Clone(launched), marker)
		if code := run([]string{"log", widgetsPR.URL()}, h.d); code != 1 || h.stdout.Len() != 0 {
			t.Errorf("%s: exit %d, stdout %q", marker, code, h.stdout)
		}
	}
}

// approveRows is pr's approve rows: origin, step and facts.
func approveRows(t *testing.T, l *Ledger) [][3]string {
	t.Helper()
	rows, err := l.db.Query(`select origin, step, facts from decisions where facts_kind = 'approve' order by id`)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rows.Close() }()
	var out [][3]string
	for rows.Next() {
		var r [3]string
		if err := rows.Scan(&r[0], &r[1], &r[2]); err != nil {
			t.Fatal(err)
		}
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return out
}

// approveHistory mints a token, spends it on the link, then approves by
// typing: the history the token tests read back. It returns the token.
func approveHistory(t *testing.T, h *fakeHost) string {
	t.Helper()
	l, err := OpenLedger(LedgerPath(h.d.StateDir))
	if err != nil {
		t.Fatal(err)
	}
	tok, err := l.MintToken(widgetsPR, sha)
	_ = l.Close()
	if err != nil {
		t.Fatal(err)
	}
	h.d.Gh = func(args ...string) (string, error) {
		if slices.Contains(args, "event=APPROVE") {
			return `{"html_url":"approved"}`, nil
		}
		return "open " + sha, nil
	}
	if code := run([]string{"claude-review://acme/widgets/42/approve?at=" + sha + "&token=" + tok.String()}, h.d); code != 0 {
		t.Fatalf("link: exit %d: %s", code, h.stderr)
	}
	if code := run([]string{"approve", widgetsPR.URL(), "--at", sha}, h.d); code != 0 {
		t.Fatalf("typed: exit %d: %s", code, h.stderr)
	}
	return tok.String()
}

// The link's token spend and the typed approve each log a row, with head,
// origin and outcome; a typed approve GitHub refuses logs its outcome too.
func TestApprovalsAreLogged(t *testing.T) {
	h := logHost(t, PRState{State: stAwaiting, Kind: kindApprove, Head: sha, Since: t0})
	approveHistory(t, h)
	moved := strings.Repeat("b", 40)
	if code := run([]string{"approve", widgetsPR.URL(), "--at", moved}, h.d); code != 1 {
		t.Fatalf("a stale typed approve: exit %d", code)
	}
	l, err := OpenLedgerReadOnly(LedgerPath(h.d.StateDir))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = l.Close() }()
	got := approveRows(t, l)
	want := [][3]string{
		{"link", "recorded", `{"head":"` + sha + `","origin":"link","outcome":"approved"}`},
		{"verb", "recorded", `{"head":"` + sha + `","origin":"verb","outcome":"approved"}`},
		{"verb", "recorded", `{"head":"` + moved + `","origin":"verb","outcome":"stale"}`},
	}
	if !slices.Equal(got, want) {
		t.Errorf("approve rows\ngot  %q\nwant %q", got, want)
	}
}

// Neither the log nor its printout carries a token, after a history that
// mints one and spends it.
func TestLogPrintsNoToken(t *testing.T) {
	h := logHost(t, PRState{State: stAwaiting, Kind: kindApprove, Head: sha, Since: t0})
	tok := approveHistory(t, h)
	h.stdout.Reset()
	if code := run([]string{"log", widgetsPR.URL()}, h.d); code != 0 {
		t.Fatalf("exit %d: %s", code, h.stderr)
	}
	out := h.stdout.String()
	if strings.Count(out, " approve {") != 2 {
		t.Fatalf("want both approvals printed: %q", out)
	}
	if strings.Contains(out, tok) {
		t.Errorf("log printed the token: %q", out)
	}
}

// insertDecisionAt logs a bare global row at at, for the retention tests.
func insertDecisionAt(t *testing.T, l *Ledger, at time.Time) {
	t.Helper()
	if _, err := l.db.Exec(`insert into decisions (pr, at_ms, origin, build, step, effects, facts_kind, facts)
		values (?, ?, 'tick', 'dev', 'global', '[]', 'close', '{}')`, widgetsPR.URL(), at.UnixMilli()); err != nil {
		t.Fatal(err)
	}
}

func decisionTimes(t *testing.T, l *Ledger) []int64 {
	t.Helper()
	rows, err := l.db.Query(`select at_ms from decisions order by at_ms`)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rows.Close() }()
	var out []int64
	for rows.Next() {
		var at int64
		if err := rows.Scan(&at); err != nil {
			t.Fatal(err)
		}
		out = append(out, at)
	}
	return out
}

// Tick drops the rows older than 90 days, and keeps the one on the
// boundary and every newer one.
func TestTickPrunesDecisionsOlderThan90Days(t *testing.T) {
	w := newTickWorld(t)
	now := t0.Add(1000 * time.Hour)
	w.h.now = func() time.Time { return now }
	boundary := now.Add(-90 * 24 * time.Hour)
	for _, at := range []time.Time{boundary.Add(-time.Millisecond), boundary.Add(-30 * 24 * time.Hour), boundary, boundary.Add(time.Millisecond), now} {
		insertDecisionAt(t, w.h.ledger, at)
	}
	if err := w.h.tick(); err != nil {
		t.Fatal(err)
	}
	want := []int64{boundary.UnixMilli(), boundary.Add(time.Millisecond).UnixMilli(), now.UnixMilli()}
	if got := decisionTimes(t, w.h.ledger); !slices.Equal(got, want) {
		t.Errorf("rows at %v, want %v", got, want)
	}
}

// A dry run writes nothing, a prune included.
func TestDryTickPrunesNothing(t *testing.T) {
	w := newTickWorld(t)
	w.h.dry = true
	insertDecisionAt(t, w.h.ledger, t0.Add(-365*24*time.Hour))
	if err := w.h.tick(); err != nil {
		t.Fatal(err)
	}
	if got := decisionTimes(t, w.h.ledger); len(got) != 1 {
		t.Errorf("a dry tick pruned: %v", got)
	}
}

// Verbs never prune: an old row outlives an approval and a log.
func TestVerbsNeverPrune(t *testing.T) {
	h := logHost(t, PRState{State: stAwaiting, Kind: kindApprove, Head: sha, Since: t0})
	l, err := OpenLedger(LedgerPath(h.d.StateDir))
	if err != nil {
		t.Fatal(err)
	}
	insertDecisionAt(t, l, time.Unix(0, 0))
	_ = l.Close()
	approveHistory(t, h)
	if code := run([]string{"log", widgetsPR.URL()}, h.d); code != 0 {
		t.Fatalf("exit %d: %s", code, h.stderr)
	}
	l, err = OpenLedgerReadOnly(LedgerPath(h.d.StateDir))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = l.Close() }()
	if got := decisionTimes(t, l); len(got) == 0 || got[0] != 0 {
		t.Errorf("a verb pruned the old row: %v", got)
	}
}
