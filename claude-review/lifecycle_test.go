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
)

// fakeSessions is a roster, job states and pids a test drives: launch adds
// what Claude Code would add, stop takes a worker's pids down after
// pidLinger polls.
type fakeSessions struct {
	workers    []Worker
	unreadable bool
	states     map[string]string
	dead       map[int]bool
	window     int
	stopErr    error
	pidLinger  int // polls a stopped worker's pid stays alive
	lingering  map[int]int
	onLaunch   func(f *fakeSessions)
	saved      map[string][]string // a session's saved background options
	savedErr   error
	launchErr  error
	settleErr  error
	events     []string
	sleeps     int
}

func newFakeSessions() *fakeSessions {
	return &fakeSessions{states: map[string]string{}, dead: map[int]bool{}, lingering: map[int]int{}}
}

func (f *fakeSessions) sessions() Sessions {
	return Sessions{
		Roster: func() ([]Worker, error) {
			if f.unreadable {
				return nil, errors.New("unexpected end of JSON input")
			}
			return slices.Clone(f.workers), nil
		},
		JobState: func(id string) string { return f.states[id] },
		Window:   func(string) int { return f.window },
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
		Stop: func(short WorkerKey) error {
			f.events = append(f.events, "stop "+string(short))
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
		Saved: func(id string) ([]string, bool, error) {
			flags, ok := f.saved[id]
			return flags, ok, f.savedErr
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

func workerFor(id string, pid int) Worker {
	return Worker{Short: WorkerKey(id[:8]), SessionID: id, PID: pid, LaunchID: id}
}

func TestRosterIsThreeValued(t *testing.T) {
	dir := t.TempDir()
	if ws, err := readRoster(dir); err != nil || len(ws) != 0 {
		t.Errorf("absent roster: %v %v, want no workers and no error", ws, err)
	}
	writeRoster(t, dir, "{not json")
	if _, err := readRoster(dir); err == nil {
		t.Error("an unreadable roster read as empty")
	}
	writeRoster(t, dir, `{"workers":{"x; rm -rf ~": {"pid": 5, "sessionId": "x"}}}`)
	if _, err := readRoster(dir); err == nil {
		t.Error("a roster key that is no worker id was read")
	}
	writeRoster(t, dir, fmt.Sprintf(`{"workers":{
	  "188e19ec": {"pid": 5, "replPid": 6, "sessionId": "188e19ec-0000", "dispatch": {"launch": {"sessionId": %q, "fork": true}}},
	  "d2cc10f2": {"pid": 7, "sessionId": %q, "dispatch": {"launch": {"sessionId": %q, "fork": false}}}}}`, sid, sid, sid))
	ws, err := readRoster(dir)
	if err != nil {
		t.Fatal(err)
	}
	slices.SortFunc(ws, func(a, b Worker) int { return strings.Compare(string(a.Short), string(b.Short)) })
	want := []Worker{
		{Short: "188e19ec", SessionID: "188e19ec-0000", PID: 5, ReplPID: 6, Fork: true, LaunchID: sid},
		{Short: "d2cc10f2", SessionID: sid, PID: 7, LaunchID: sid},
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
	s.Roster = func() ([]Worker, error) {
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

// Only an explicit done is stopped: any other state, or none, is a round
// that may still be going.
func TestRelaunchStopsAWorkerOnlyOnDone(t *testing.T) {
	for _, state := range []string{"working", "blocked", "", "stopped", "something new"} {
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
		"fork":   {Short: "188e19ec", SessionID: "188e19ec-1111", PID: 800, Fork: true, LaunchID: sid},
		"second": {Short: "d2cc10f2", SessionID: sid, PID: 801, LaunchID: sid},
	} {
		f := newFakeSessions()
		f.onLaunch = func(f *fakeSessions) { f.workers = append(f.workers, extra, workerFor(sid, 700)) }
		res, err := f.sessions().Relaunch(sid, f.settle, f.launch)
		if err != nil || res.Kind != relaunchDuplicate {
			t.Errorf("%s: got %+v %v", name, res, err)
		}
		if !slices.Contains(f.events, "stop "+string(extra.Short)) {
			t.Errorf("%s: the extra worker was not stopped: %q", name, f.events)
		}
	}
	// A fork already there before the launch: nothing is launched on top.
	f := newFakeSessions()
	f.workers = []Worker{{Short: "188e19ec", SessionID: "188e19ec-1111", PID: 800, Fork: true, LaunchID: sid}}
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
	f.workers = []Worker{{Short: WorkerKey(sid[:8]), SessionID: sid, PID: 100, ReplPID: 4242, LaunchID: sid}}
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
	argv, err := stopArgv("linux", "d2cc10f2")
	if err != nil || strings.Join(argv, "|") != "script|-qec|claude stop d2cc10f2|/dev/null" {
		t.Errorf("linux: %q %v", argv, err)
	}
	argv, err = stopArgv(darwin, "d2cc10f2")
	if err != nil || strings.Join(argv, "|") != "script|-q|/dev/null|claude|stop|d2cc10f2" {
		t.Errorf("darwin: %q %v", argv, err)
	}
	if _, err := stopArgv("linux", "x; rm -rf ~"); err == nil {
		t.Error("a roster key that is no worker id reached the shell")
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
	f.saved = map[string][]string{sid: slices.Clone(policy)}
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

func writeJobBody(t *testing.T, dir, body string) {
	t.Helper()
	d := filepath.Join(dir, "jobs", sid[:8])
	if err := os.MkdirAll(d, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(d, "state.json"), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}
