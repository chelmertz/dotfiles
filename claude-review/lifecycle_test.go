package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/chelmertz/dotfiles/claude-review/ids"
	"github.com/chelmertz/dotfiles/claude-review/prlock"
)

// fakeSessions is a roster, job states and pids a test drives: launch adds
// what Claude Code would add, stop takes a worker's pids down after
// pidLinger polls.
type fakeSessions struct {
	workers    []Worker
	unreadable bool
	states     map[ids.SessionID]string
	dead       map[int]bool
	window     int
	windowID   ids.SessionID // when set, the window holds only this id
	windowErr  map[ids.SessionID]error
	unparsed   error         // what Unparsed reports
	failed     []failedEntry // roster entries that did not parse
	stopErr    error
	pidLinger  int // polls a stopped worker's pid stays alive
	lingering  map[int]int
	onLaunch   func(f *fakeSessions)
	saved      map[ids.SessionID][]string // a session's saved background options
	savedErr   error
	restoreErr error
	launchErr  error
	settleErr  error
	events     []string
	sleeps     int
}

func newFakeSessions() *fakeSessions {
	return &fakeSessions{states: map[ids.SessionID]string{}, dead: map[int]bool{}, lingering: map[int]int{}}
}

func (f *fakeSessions) sessions() Sessions {
	return Sessions{
		Roster: func() (Roster, error) {
			if f.unreadable {
				return Roster{}, errors.New("unexpected end of JSON input")
			}
			return Roster{workers: slices.Clone(f.workers), failed: slices.Clone(f.failed)}, nil
		},
		JobState: func(id ids.SessionID) string { return f.states[id] },
		Window: func(id ids.SessionID) (int, error) {
			if err := f.windowErr[id]; err != nil {
				return 0, err
			}
			if !f.windowID.IsZero() && id != f.windowID {
				return 0, nil
			}
			return f.window, nil
		},
		Unparsed: func() error { return f.unparsed },
		Alive: func(pid int) bool {
			if n, ok := f.lingering[pid]; ok {
				if n <= 0 {
					return false
				}
				f.lingering[pid] = n - 1
				return true
			}
			return !f.dead[pid]
		},
		Stop: func(short ids.WorkerKey) error {
			f.events = append(f.events, "stop "+short.String())
			if f.stopErr != nil {
				return f.stopErr
			}
			// The roster drops the worker at once; its pid lingers.
			for i, w := range f.workers {
				if w.Short == short {
					f.lingering[w.PID] = f.pidLinger
					f.workers = slices.Delete(f.workers, i, i+1)
					break
				}
			}
			return nil
		},
		Sleep: func(time.Duration) { f.sleeps++ },
		Saved: func(id ids.SessionID) ([]string, bool, error) {
			flags, ok := f.saved[id]
			return flags, ok, f.savedErr
		},
		Restore: func(id ids.SessionID, flags []string) error {
			f.events = append(f.events, "restore")
			if f.restoreErr != nil {
				return f.restoreErr
			}
			f.saved[id] = slices.Clone(flags)
			return nil
		},
	}
}

func (f *fakeSessions) settle() error {
	f.events = append(f.events, "settle")
	return f.settleErr
}

func (f *fakeSessions) launch() error {
	f.events = append(f.events, "launch")
	if f.launchErr != nil {
		return f.launchErr
	}
	if f.onLaunch != nil {
		f.onLaunch(f)
	} else {
		f.workers = append(f.workers, workerFor(sid, 700))
	}
	return nil
}

// forkID is a session Claude Code forked off sid.
var forkID = mustSession("188e19ec-1111-4111-8111-111111111111")

func workerFor(id ids.SessionID, pid int) Worker {
	return Worker{Short: id.Key(), SessionID: id, PID: pid, LaunchID: id}
}

// rosterOf is a roster listing ws.
func rosterOf(ws ...Worker) Roster { return Roster{workers: ws} }

// rosterAll is every worker r lists under sid or forkID, the ids these
// tests use.
func rosterAll(t *testing.T, r Roster) []Worker {
	t.Helper()
	ws, err := r.For(sid, forkID)
	if err != nil {
		t.Fatal(err)
	}
	return ws
}

func TestRosterIsThreeValued(t *testing.T) {
	dir := t.TempDir()
	if r, err := readRoster(dir); err != nil || len(rosterAll(t, r)) != 0 {
		t.Errorf("absent roster: %v %v, want no workers and no error", r, err)
	}
	writeRoster(t, dir, "{not json")
	if _, err := readRoster(dir); err == nil {
		t.Error("an unreadable roster read as empty")
	}
	writeRoster(t, dir, `{"workers":{"x; rm -rf ~": {"pid": 5, "sessionId": "x"}}}`)
	if r, err := readRoster(dir); err != nil {
		t.Fatal(err)
	} else if ws, err := r.For(sid); err == nil || !strings.Contains(err.Error(), "x; rm -rf ~") {
		t.Errorf("a roster key that is no worker id was read: %+v %v", ws, err)
	}
	writeRoster(t, dir, fmt.Sprintf(`{"workers":{
	  "188e19ec": {"pid": 5, "replPid": 6, "sessionId": %q, "dispatch": {"launch": {"sessionId": %q, "fork": true}}},
	  "d2cc10f2": {"pid": 7, "sessionId": %q, "dispatch": {"launch": {"sessionId": %q, "fork": false}}}}}`, forkID, sid, sid, sid))
	r, err := readRoster(dir)
	if err != nil {
		t.Fatal(err)
	}
	ws := rosterAll(t, r)
	slices.SortFunc(ws, func(a, b Worker) int { return strings.Compare(a.Short.String(), b.Short.String()) })
	want := []Worker{
		{Short: forkID.Key(), SessionID: forkID, PID: 5, ReplPID: 6, Fork: true, LaunchID: sid},
		{Short: sid.Key(), SessionID: sid, PID: 7, LaunchID: sid},
	}
	if !slices.Equal(ws, want) {
		t.Errorf("roster:\n got %+v\nwant %+v", ws, want)
	}
}

func TestRelaunchWithNoWorkerLaunchesAndHoldsUntilListed(t *testing.T) {
	f := newFakeSessions()
	f.onLaunch = func(*fakeSessions) {} // not listed yet
	polls := 0
	s := f.sessions()
	roster := s.Roster
	s.Roster = func() (Roster, error) {
		polls++
		if polls == 4 {
			f.workers = []Worker{workerFor(sid, 700)}
		}
		return roster()
	}
	res, err := s.Relaunch(sid, f.settle, f.launch)
	if err != nil || res.Kind != relaunched {
		t.Fatalf("got %+v %v", res, err)
	}
	if strings.Join(f.events, ",") != "launch" || polls < 4 {
		t.Errorf("events %q, roster polls %d: must launch, then hold until the worker is listed", f.events, polls)
	}
}

func TestRelaunchUnreadableRosterLaunchesNothing(t *testing.T) {
	f := newFakeSessions()
	f.unreadable = true
	res, err := f.sessions().Relaunch(sid, f.settle, f.launch)
	if err != nil || res.Kind != relaunchUnknown || len(f.events) != 0 {
		t.Errorf("got %+v %v, events %q", res, err, f.events)
	}
}

// Only an explicit done, or a question waiting on the user, is stopped: any
// other state, or none, is a round that may still be going.
func TestRelaunchStopsAWorkerOnlyOnDone(t *testing.T) {
	f := newFakeSessions()
	f.workers = []Worker{workerFor(sid, 100)}
	f.states[sid] = jobBlocked
	if res, err := f.sessions().Relaunch(sid, f.settle, f.launch); err != nil || !slices.Equal(f.events[:2], []string{"settle", "stop " + sid.Key().String()}) {
		t.Errorf("a question's worker: got %+v %v, events %q", res, err, f.events)
	}
	for _, state := range []string{"working", "", "stopped", "something new"} {
		f := newFakeSessions()
		f.workers = []Worker{workerFor(sid, 100)}
		f.states[sid] = state
		res, err := f.sessions().Relaunch(sid, f.settle, f.launch)
		if err != nil || res.Kind != relaunchBusy || len(f.events) != 0 {
			t.Errorf("state %q: got %+v %v, events %q", state, res, err, f.events)
		}
	}
}

func TestRelaunchSettlesStopsWaitsForThePidThenLaunches(t *testing.T) {
	f := newFakeSessions()
	f.workers = []Worker{workerFor(sid, 100)}
	f.states[sid] = "done"
	f.pidLinger = 5
	s := f.sessions()
	alive := s.Alive
	s.Alive = func(pid int) bool {
		a := alive(pid)
		if pid == 100 && a {
			f.events = append(f.events, "alive")
		}
		return a
	}
	res, err := s.Relaunch(sid, f.settle, f.launch)
	if err != nil || res.Kind != relaunched {
		t.Fatalf("got %+v %v", res, err)
	}
	// The roster dropped the worker at once; the launch waits for the pid.
	if got := strings.Join(f.events, ","); !strings.HasPrefix(got, "alive,settle,stop d2cc10f2,alive,alive,alive,alive,alive,launch") {
		t.Errorf("events %s", got)
	}
}

func TestRelaunchLaunchesNothingWhenTheStopDoesNotTake(t *testing.T) {
	for name, f := range map[string]*fakeSessions{
		"settle fails": func() *fakeSessions { f := newFakeSessions(); f.settleErr = errors.New("gh down"); return f }(),
		"stop fails":   func() *fakeSessions { f := newFakeSessions(); f.stopErr = errors.New("no tty"); return f }(),
		"pid lingers":  func() *fakeSessions { f := newFakeSessions(); f.pidLinger = 1 << 20; return f }(),
	} {
		f.workers = []Worker{workerFor(sid, 100)}
		f.states[sid] = "done"
		_, err := f.sessions().Relaunch(sid, f.settle, f.launch)
		if err == nil || slices.Contains(f.events, "launch") {
			t.Errorf("%s: err %v, events %q", name, err, f.events)
		}
		if name == "settle fails" && slices.ContainsFunc(f.events, func(e string) bool { return strings.HasPrefix(e, "stop") }) {
			t.Errorf("stopped a worker whose round was not settled: %q", f.events)
		}
	}
}

func TestRelaunchNeverListedIsReported(t *testing.T) {
	f := newFakeSessions()
	f.onLaunch = func(*fakeSessions) {}
	res, err := f.sessions().Relaunch(sid, f.settle, f.launch)
	if err != nil || res.Kind != relaunchNotListed || f.sleeps == 0 {
		t.Errorf("got %+v %v after %d polls", res, err, f.sleeps)
	}
}

func TestRelaunchFailedLaunchIsAnError(t *testing.T) {
	f := newFakeSessions()
	f.launchErr = errors.New("Workspace not trusted")
	if _, err := f.sessions().Relaunch(sid, f.settle, f.launch); err == nil || !strings.Contains(err.Error(), "Workspace not trusted") {
		t.Errorf("err %v", err)
	}
}

// Claude Code forked an idle live worker instead of continuing it (seen on
// 2.1.314): a fork, or any second worker for the PR, is stopped, and the
// caller hears about it.
func TestRelaunchStopsAForkOrASecondWorker(t *testing.T) {
	for name, extra := range map[string]Worker{
		"fork":   {Short: forkID.Key(), SessionID: forkID, PID: 800, Fork: true, LaunchID: sid},
		"second": {Short: sid.Key(), SessionID: sid, PID: 801, LaunchID: sid},
	} {
		f := newFakeSessions()
		f.onLaunch = func(f *fakeSessions) { f.workers = append(f.workers, extra, workerFor(sid, 700)) }
		res, err := f.sessions().Relaunch(sid, f.settle, f.launch)
		if err != nil || res.Kind != relaunchDuplicate {
			t.Errorf("%s: got %+v %v", name, res, err)
		}
		if !slices.Contains(f.events, "stop "+extra.Short.String()) {
			t.Errorf("%s: the extra worker was not stopped: %q", name, f.events)
		}
	}
	// A fork already there before the launch: nothing is launched on top.
	f := newFakeSessions()
	f.workers = []Worker{{Short: forkID.Key(), SessionID: forkID, PID: 800, Fork: true, LaunchID: sid}}
	res, err := f.sessions().Relaunch(sid, f.settle, f.launch)
	if err != nil || res.Kind != relaunchDuplicate || slices.Contains(f.events, "launch") {
		t.Errorf("pre-existing fork: got %+v %v, events %q", res, err, f.events)
	}
}

func TestRelaunchLeavesALegacyWindowAlone(t *testing.T) {
	f := newFakeSessions()
	f.window = 4242
	res, err := f.sessions().Relaunch(sid, f.settle, f.launch)
	if err != nil || res.Kind != relaunchWindow || len(f.events) != 0 {
		t.Errorf("got %+v %v, events %q", res, err, f.events)
	}
	// A worker's own sessions/ entry is not a window.
	f = newFakeSessions()
	f.workers = []Worker{{Short: sid.Key(), SessionID: sid, PID: 100, ReplPID: 4242, LaunchID: sid}}
	f.states[sid] = "working"
	f.window = 4242
	if res, _ := f.sessions().Relaunch(sid, f.settle, f.launch); res.Kind != relaunchBusy {
		t.Errorf("a worker's repl read as a window: %+v", res)
	}
}

func TestRelaunchIgnoresDeadWorkers(t *testing.T) {
	f := newFakeSessions()
	f.workers = []Worker{workerFor(sid, 100)}
	f.dead[100] = true
	f.states[sid] = "working"
	f.onLaunch = func(f *fakeSessions) { f.workers = append(f.workers, workerFor(sid, 700)) }
	if res, err := f.sessions().Relaunch(sid, f.settle, f.launch); err != nil || res.Kind != relaunched {
		t.Errorf("a dead worker held the PR: %+v %v", res, err)
	}
}

func TestStopRunsUnderAPty(t *testing.T) {
	argv, err := stopArgv("linux", sid.Key())
	if err != nil || strings.Join(argv, "|") != "script|-qec|claude stop d2cc10f2|/dev/null" {
		t.Errorf("linux: %q %v", argv, err)
	}
	argv, err = stopArgv(darwin, sid.Key())
	if err != nil || strings.Join(argv, "|") != "script|-q|/dev/null|claude|stop|d2cc10f2" {
		t.Errorf("darwin: %q %v", argv, err)
	}
	if _, err := stopArgv("linux", ids.WorkerKey{}); err == nil {
		t.Error("an empty worker id reached the shell")
	}
}

func TestReadSavedOptions(t *testing.T) {
	dir := t.TempDir()
	if flags, ran, err := readSaved(dir, sid); err != nil || ran || flags != nil {
		t.Errorf("never in the background: %q %v %v", flags, ran, err)
	}
	writeJobBody(t, dir, `{"state":"done","respawnFlags":["--permission-mode","dontAsk","--setting-sources","","--allowedTools","Agent"]}`)
	if flags, ran, err := readSaved(dir, sid); err != nil || !ran || !slices.Equal(flags, []string{"--permission-mode", "dontAsk", "--setting-sources", "", "--allowedTools", "Agent"}) {
		t.Errorf("saved: %q %v %v", flags, ran, err)
	}
	for _, body := range []string{`{"state":"done"}`, `{not json`} {
		writeJobBody(t, dir, body)
		if _, _, err := readSaved(dir, sid); err == nil {
			t.Errorf("%s read as known options", body)
		}
	}
}

// A background session keeps its first launch's options, and a later
// launch with flags forks it: so the first launch carries the policy, every
// later one none, and only under the same saved policy.
func TestLaunchArgvIsFlaglessOnlyUnderTheSamePolicy(t *testing.T) {
	policy := []string{"--permission-mode", "dontAsk", "--allowedTools", "Agent"}
	first, later := []string{"first"}, []string{"later"}
	f := newFakeSessions()
	s := f.sessions()
	if argv, err := s.LaunchArgv(sid, policy, first, later); err != nil || !slices.Equal(argv, first) {
		t.Errorf("never launched: %q %v", argv, err)
	}
	f.saved = map[ids.SessionID][]string{sid: slices.Clone(policy)}
	if argv, err := s.LaunchArgv(sid, policy, first, later); err != nil || !slices.Equal(argv, later) {
		t.Errorf("same policy: %q %v", argv, err)
	}
	f.saved[sid] = []string{"--permission-mode", "dontAsk", "--setting-sources", "project,local"}
	if argv, err := s.LaunchArgv(sid, policy, first, later); err == nil || argv != nil || !strings.Contains(err.Error(), "project,local") {
		t.Errorf("other saved options: %q %v", argv, err)
	}
	f.savedErr = errors.New("no respawnFlags")
	if argv, err := s.LaunchArgv(sid, policy, first, later); err == nil || argv != nil {
		t.Errorf("unreadable saved options: %q %v", argv, err)
	}
}

// The user can change an attached round session's permission mode, and
// Claude Code saves it (dontAsk to plan on a real PR, 2026-11-15). The
// reviewer carries on: a mode-only difference launches flagless after the
// policy is put back; any other difference still launches nothing.
func TestRoundArgvRestoresOnlyAChangedPermissionMode(t *testing.T) {
	policy := []string{"--permission-mode", "dontAsk", "--allowedTools", "Agent"}
	first, later := []string{"first"}, []string{"later"}
	f := newFakeSessions()
	s := f.sessions()
	// In place, and moved to the end, as Claude Code saves it.
	for _, changed := range [][]string{
		{"--permission-mode", "plan", "--allowedTools", "Agent"},
		{"--allowedTools", "Agent", "--permission-mode", "plan"},
	} {
		f.saved = map[ids.SessionID][]string{sid: changed}
		if argv, restore, err := s.RoundArgv(sid, policy, first, later); err != nil || !restore || !slices.Equal(argv, later) {
			t.Errorf("mode changed %q: %q %v %v", changed, argv, restore, err)
		}
	}
	if _, err := s.LaunchArgv(sid, policy, first, later); err == nil || !strings.Contains(err.Error(), `"plan"`) || strings.Contains(err.Error(), "Agent") {
		t.Errorf("the refusal does not name just the mode: %v", err)
	}
	f.saved[sid] = slices.Clone(policy)
	if argv, restore, err := s.RoundArgv(sid, policy, first, later); err != nil || restore || !slices.Equal(argv, later) {
		t.Errorf("same policy: %q %v %v", argv, restore, err)
	}
	for _, other := range [][]string{
		{"--permission-mode", "plan", "--allowedTools", "Bash"},
		{"--permission-mode", "dontAsk", "--allowedTools", "Bash"},
		{"--permission-mode", "plan", "--allowedTools", "Agent", "Bash"},
		{"--allowedTools", "Agent", "--permission-mode", "plan", "--permission-mode", "plan"},
		{"--allowedTools", "Agent"},
	} {
		f.saved[sid] = other
		if argv, restore, err := s.RoundArgv(sid, policy, first, later); err == nil || restore || argv != nil {
			t.Errorf("%q launched: %q %v", other, argv, restore)
		}
	}
}

// The policy goes back only once the idle worker is stopped, so the worker
// cannot save its own options over it, and before the flagless launch.
func TestRoundStarterRestoresAfterTheStopBeforeTheLaunch(t *testing.T) {
	f := newFakeSessions()
	f.workers = []Worker{{SessionID: widgetsPR.RoundSessionID(), Short: widgetsPR.RoundSessionID().Key(), PID: 7}}
	f.states[widgetsPR.RoundSessionID()] = jobDone
	policy := PolicyFlags(widgetsPR, "origin")
	// As Claude Code saved it on a real PR: the changed pair moved last.
	changed := append(slices.Clone(policy[2:]), "--permission-mode", "plan")
	if policy[0] != "--permission-mode" {
		t.Fatalf("the policy no longer opens with the mode: %q", policy[:2])
	}
	f.saved = map[ids.SessionID][]string{widgetsPR.RoundSessionID(): changed}
	f.onLaunch = func(f *fakeSessions) {
		f.workers = []Worker{{SessionID: widgetsPR.RoundSessionID(), Short: widgetsPR.RoundSessionID().Key(), PID: 8}}
	}
	var ran [][]string
	r := roundStarter{sess: f.sessions(), exists: func(ids.SessionID) bool { return true },
		run: func(_ string, argv []string) error { ran = append(ran, argv); return f.launch() }}
	res, err := r.start(Config{}, widgetsPR, "/clone", "origin", true, f.settle)
	if err != nil || res.Kind != relaunched {
		t.Fatalf("%+v %v", res, err)
	}
	if got := strings.Join(f.events, ","); got != "settle,stop "+widgetsPR.RoundSessionID().Key().String()+",restore,launch" {
		t.Errorf("events %s", got)
	}
	if len(ran) != 1 || slices.Contains(ran[0], "--permission-mode") {
		t.Errorf("not a flagless resume: %q", ran)
	}
	if !slices.Equal(f.saved[widgetsPR.RoundSessionID()], policy) {
		t.Errorf("saved %q", f.saved[widgetsPR.RoundSessionID()])
	}
}

// writeSaved replaces only respawnFlags; the rest of Claude Code's job file
// stays as it was.
func TestWriteSavedKeepsTheRestOfTheJob(t *testing.T) {
	dir := t.TempDir()
	writeJobBody(t, dir, `{"state":"blocked","needs":"a question","respawnFlags":["--permission-mode","plan"]}`)
	if err := writeSaved(dir, sid, []string{"--permission-mode", "dontAsk"}); err != nil {
		t.Fatal(err)
	}
	if flags, _, err := readSaved(dir, sid); err != nil || !slices.Equal(flags, []string{"--permission-mode", "dontAsk"}) {
		t.Errorf("saved %q %v", flags, err)
	}
	b, err := os.ReadFile(filepath.Join(dir, "jobs", sid.Key().String(), "state.json"))
	if err != nil || !strings.Contains(string(b), `"needs": "a question"`) || !strings.Contains(string(b), `"state": "blocked"`) {
		t.Errorf("job file %s %v", b, err)
	}
	if err := writeSaved(t.TempDir(), sid, nil); err == nil {
		t.Error("wrote options for a session with no job")
	}
}

func writeJobBody(t *testing.T, dir, body string) {
	t.Helper()
	d := filepath.Join(dir, "jobs", sid.Key().String())
	if err := os.MkdirAll(d, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(d, "state.json"), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

// Every launch result lands the same way whether tick launched (from idle)
// or a click did (from its starting state): one table, afterRelaunch.
func TestEveryLaunchResultLandsTheSameOnBothPaths(t *testing.T) {
	type want struct {
		state    stateKey
		class    string
		recorded bool
		skip     skip
		err      bool
	}
	for kind, wnt := range map[RelaunchKind]want{
		relaunched:        {recorded: true},
		relaunchWindow:    {state: stateKey{stHeld, kindWindow}, skip: skipWindowOpen},
		relaunchDuplicate: {state: stateKey{stHeld, kindLaunch}, class: launchDuplicate, skip: "x; rounds are paused for every PR", err: true},
		relaunchNotListed: {state: stateKey{stHeld, kindLaunch}, class: launchUnlisted, skip: "the round was launched but x; nothing was recorded"},
		relaunchPolicy:    {state: stateKey{stHeld, kindLaunch}, class: launchNotStarted, skip: "nothing started: x"},
		relaunchBusy:      {skip: skipBusy},
		relaunchUnknown:   {err: true},
	} {
		for _, from := range []PRState{{State: stIdle, Head: "h1"}, {State: stStarting, Kind: kindClick, Head: "h1", Prev: &PRState{State: stHandled, Kind: kindPosted, Head: "h1"}}} {
			w := newTickWorld(t)
			pr := w.accept(t, 42)
			if err := w.h.ledger.SetPRState(pr, from); err != nil {
				t.Fatal(err)
			}
			recorded := false
			lk := mustLock(t, w.h, pr)
			not, err := w.h.afterRelaunch(lk, pr, RelaunchResult{Kind: kind, Detail: "x"}, nil, &launchRecord{
				listed: func(ids.RoundID) error { recorded = true; return nil },
				failed: w.h.failedToStart(lk, pr),
			})
			got, _, _ := w.h.ledger.PRState(pr)
			if (err != nil) != wnt.err || not != wnt.skip || recorded != wnt.recorded {
				t.Errorf("kind %d from %s: skip %q err %v recorded %v", kind, from.State, not, err, recorded)
			}
			if wnt.state == (stateKey{}) {
				if got.State != from.State {
					t.Errorf("kind %d from %s moved to %s", kind, from.State, got.State)
				}
				continue
			}
			if (stateKey{got.State, got.Kind}) != wnt.state || got.Detail != wnt.class && wnt.class != "" {
				t.Errorf("kind %d from %s: landed on %s{%s} %q, want %v %q", kind, from.State, got.State, got.Kind, got.Detail, wnt.state, wnt.class)
			}
		}
	}
	if _, err := newTickWorld(t).h.afterRelaunch(prlock.Locked{}, widgetsPR, RelaunchResult{}, nil, &launchRecord{}); err == nil {
		t.Error("a launch with no outcome read as busy")
	}
}

// Claude Code 2.1.325 records a worker forked off an interactive session by
// that session's transcript path, not its id. One such worker, which is not
// this tool's, made the whole roster unreadable and so held every launch
// and close-out (2026-11-23). The path's file name is the id; any other
// shape leaves that worker's entry unknown for the sessions it names.
func TestRosterReadsALaunchRecordedAsATranscriptPath(t *testing.T) {
	dir := t.TempDir()
	writeRoster(t, dir, fmt.Sprintf(`{"workers":{"188e19ec": {"pid": 5, "sessionId": %q,
	  "dispatch": {"launch": {"sessionId": "/home/u/.claude/projects/-home-u-p/%s.jsonl", "fork": true}}}}}`, forkID, sid))
	r, err := readRoster(dir)
	if err != nil {
		t.Fatal(err)
	}
	ws := rosterAll(t, r)
	if len(ws) != 1 || ws[0].LaunchID != sid || !ws[0].Fork {
		t.Errorf("roster: %+v, want one fork of %s", ws, sid)
	}
	for _, bad := range []string{"/home/u/notes.jsonl", "/home/u/" + sid.String() + ".txt", "x" + sid.String() + ".jsonl"} {
		writeRoster(t, dir, fmt.Sprintf(`{"workers":{"188e19ec": {"pid": 5, "sessionId": %q,
		  "dispatch": {"launch": {"sessionId": %q}}}}}`, forkID, bad))
		r, err := readRoster(dir)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := r.For(forkID); err == nil {
			t.Errorf("launch %q read as a session", bad)
		}
	}
}

// splitMode refuses a --permission-mode with no value after it, or two.
func TestSplitModeRefusesAMissingOrRepeatedMode(t *testing.T) {
	for _, flags := range [][]string{{"--x", flagMode}, {flagMode, "auto", flagMode, "plan"}, {"--x"}} {
		if _, _, ok := splitMode(flags); ok {
			t.Errorf("%q split", flags)
		}
	}
	mode, rest, ok := splitMode([]string{"--x", flagMode, "auto", "--y"})
	if !ok || mode != "auto" || !slices.Equal(rest, []string{"--x", "--y"}) {
		t.Errorf("got %q %q %v", mode, rest, ok)
	}
}
