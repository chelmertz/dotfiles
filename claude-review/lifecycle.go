package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/chelmertz/dotfiles/claude-review/ids"
)

// Worker is one background worker in Claude Code's roster
// (~/.claude/daemon/roster.json), as of 2.1.314.
type Worker struct {
	// Short is the roster key; `claude stop` and `claude attach` take it.
	Short     ids.WorkerKey
	SessionID ids.SessionID
	PID       int
	// ReplPID is the worker's own session process, which also has a
	// sessions/ entry; it is not a window.
	ReplPID int
	// Fork: Claude Code forked LaunchID into a new session instead of
	// continuing it. Seen when `claude --bg --resume <id>` passed flags to a
	// session that had run in the background before (2.1.314).
	Fork     bool
	LaunchID ids.SessionID
}

// rosterEntry is the part of one roster.json worker this tool reads.
type rosterEntry struct {
	SessionID ids.SessionID `json:"sessionId"`
	PID       int           `json:"pid"`
	ReplPID   int           `json:"replPid"`
	Dispatch  struct {
		Launch struct {
			SessionID launchRef `json:"sessionId"`
			Fork      bool      `json:"fork"`
		} `json:"launch"`
	} `json:"dispatch"`
}

// launchRef is the session a worker was launched from. Claude Code writes
// an id, or, for a fork of an interactive session (2.1.325), that session's
// transcript path, whose file name is the id. Any other shape is refused,
// which makes that worker's entry unreadable.
type launchRef ids.SessionID

func (l *launchRef) UnmarshalJSON(b []byte) error {
	var raw string
	if err := json.Unmarshal(b, &raw); err != nil {
		return err
	}
	if base, ok := strings.CutSuffix(filepath.Base(raw), ".jsonl"); ok && strings.Contains(raw, "/") {
		raw = base
	}
	var id ids.SessionID
	if err := id.UnmarshalJSON([]byte(strconv.Quote(raw))); err != nil {
		return err
	}
	*l = launchRef(id)
	return nil
}

// Roster is one read of Claude Code's roster. Each worker's entry is read
// on its own, so one this tool cannot read is unknown only for the
// sessions it names, and ignored for the rest.
type Roster struct {
	workers []Worker
	failed  []failedEntry
}

// failedEntry is a roster entry, or a sessions/ file, that did not parse:
// where it is, its raw bytes, and why.
type failedEntry struct {
	where string
	raw   []byte
	err   error
}

// names reports whether f names id: as its roster key (the id's first 8
// characters) or anywhere in its raw bytes.
func (f failedEntry) names(key string, id ids.SessionID) bool {
	return key == id.Key().String() || bytes.Contains(f.raw, []byte(id.String()))
}

// anySessionID finds a session id's shape anywhere in raw.
var anySessionID = regexp.MustCompile(`[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}`)

// For is the listed workers of any of the ids: their own, or a fork of
// one. An error is unknown for them: an entry that did not parse names one
// of them, or names no session at all, so it may be anyone's.
func (r Roster) For(sessions ...ids.SessionID) ([]Worker, error) {
	for _, f := range r.failed {
		key, _ := strings.CutPrefix(f.where, rosterKeyPrefix)
		var k ids.WorkerKey
		idless := k.UnmarshalText([]byte(key)) != nil && !anySessionID.Match(f.raw)
		if idless || slices.ContainsFunc(sessions, func(id ids.SessionID) bool { return f.names(key, id) }) {
			return nil, fmt.Errorf("reading Claude Code's roster, %s: %w", f.where, f.err)
		}
	}
	var out []Worker
	for _, w := range r.workers {
		if slices.Contains(sessions, w.SessionID) || (w.Fork && slices.Contains(sessions, w.LaunchID)) {
			out = append(out, w)
		}
	}
	return out, nil
}

// rosterKeyPrefix names a roster entry in an error, by its key.
const rosterKeyPrefix = "worker "

// readRoster reads the roster. No file is no workers; a file that cannot
// be read, or is not a map of workers, is an error, which every launcher
// treats as "do not launch": an unknown roster may hold a worker a launch
// would fork. A worker's entry that does not parse is kept for For.
func readRoster(claudeDir string) (Roster, error) {
	b, err := os.ReadFile(filepath.Join(claudeDir, "daemon", "roster.json")) //nolint:gosec // path under the user's own config dir
	if errors.Is(err, fs.ErrNotExist) {
		return Roster{}, nil
	}
	if err != nil {
		return Roster{}, fmt.Errorf("reading Claude Code's roster: %w", err)
	}
	var file struct {
		Workers map[string]json.RawMessage `json:"workers"`
	}
	if err := json.Unmarshal(b, &file); err != nil {
		return Roster{}, fmt.Errorf("reading Claude Code's roster: %w", err)
	}
	var r Roster
	for key, raw := range file.Workers {
		var k ids.WorkerKey
		var w rosterEntry
		err := k.UnmarshalText([]byte(key))
		if err == nil {
			err = json.Unmarshal(raw, &w)
		}
		if err != nil {
			r.failed = append(r.failed, failedEntry{where: rosterKeyPrefix + key, raw: raw, err: err})
			continue
		}
		r.workers = append(r.workers, Worker{Short: k, SessionID: w.SessionID, PID: w.PID, ReplPID: w.ReplPID,
			Fork: w.Dispatch.Launch.Fork, LaunchID: ids.SessionID(w.Dispatch.Launch.SessionID)})
	}
	return r, nil
}

// Sessions is what a launcher needs from Claude Code to keep one worker per
// PR under the PR's fixed session id. Every function is a variable so tests
// drive a fake roster.
type Sessions struct {
	Roster   func() (Roster, error)
	JobState func(id ids.SessionID) string
	// Window is a live process sessions/ lists for id, or 0: a legacy
	// interactive window, a `claude -p`, or a worker's own repl. An error
	// is unknown for id: a sessions/ file that did not parse names it.
	Window func(id ids.SessionID) (int, error)
	// Unparsed names every live sessions/ file that did not parse, or is
	// nil. A pass where it is not nil closes no window.
	Unparsed func() error
	Alive    func(pid int) bool
	// Stop runs `claude stop <key>`; it returns before the pid ends.
	Stop  func(key ids.WorkerKey) error
	Sleep func(time.Duration)
	// Saved is the options id's background session was first launched
	// with; ran is false when it never ran in the background.
	Saved func(id ids.SessionID) (flags []string, ran bool, err error)
	// Restore replaces the options id's background session saved; only
	// while no worker runs for it, or the worker saves its own over them.
	Restore func(id ids.SessionID, flags []string) error
}

// readSaved reads the options Claude Code saved for id's background
// session from jobs/<short>/state.json (respawnFlags, the launch's flags
// without --bg, --resume and the prompt; 2.1.314). No job file means the
// session never ran in the background. A job without readable options is
// an error: its saved options are unknown.
func readSaved(claudeDir string, id ids.SessionID) ([]string, bool, error) {
	if id.IsZero() {
		return nil, false, errors.New("no session id")
	}
	j, ok, err := readJobFile(claudeDir, id)
	switch {
	case err != nil:
		return nil, true, fmt.Errorf("reading the session's saved options: %w", err)
	case !ok:
		return nil, false, nil
	case j.RespawnFlags == nil:
		return nil, true, errors.New("the session's job records no saved options")
	}
	return *j.RespawnFlags, true, nil
}

// LaunchArgv picks the launch for id's next round: first, which carries
// policy, when the session never ran in the background; later, which
// carries no flags, when the session saved exactly policy. Claude Code
// keeps a background session's first options and forks the session when a
// later launch passes flags (2.1.314), so any other saved options, or ones
// that cannot be read, mean no launch.
func (s Sessions) LaunchArgv(id ids.SessionID, policy, first, later []string) ([]string, error) {
	saved, ran, err := s.Saved(id)
	switch {
	case err != nil:
		return nil, err
	case !ran:
		return first, nil
	case slices.Equal(saved, policy):
		return later, nil
	}
	if mode, ok := modeOnly(saved, policy); ok {
		want, _, _ := splitMode(policy)
		return nil, fmt.Errorf("the session's permission mode was changed to %q; the round policy's is %q", mode, want)
	}
	return nil, fmt.Errorf("the session was started with other options than the round policy: %q", saved)
}

// RoundArgv is LaunchArgv for a round, where the user may have attached to
// the session and changed its permission mode, which Claude Code saves
// (dontAsk to plan, 2.1.322). The reviewer carries on: when the mode is the
// only difference, it launches flagless too, and restore says the policy
// must be put back (Restore) once the worker is stopped. A flagless resume
// runs under the saved options, not the transcript's last mode (checked
// live 2026-11-16).
func (s Sessions) RoundArgv(id ids.SessionID, policy, first, later []string) (argv []string, restore bool, err error) {
	argv, err = s.LaunchArgv(id, policy, first, later)
	if err == nil {
		return argv, false, nil
	}
	if saved, _, serr := s.Saved(id); serr == nil {
		if _, ok := modeOnly(saved, policy); ok {
			return later, true, nil
		}
	}
	return nil, false, err
}

// modeOnly reports whether saved is policy with another permission mode,
// and that mode. Claude Code moves the changed pair to the end of the saved
// options, so the pair is compared apart from the rest, whose order holds.
func modeOnly(saved, policy []string) (string, bool) {
	sm, srest, ok1 := splitMode(saved)
	pm, prest, ok2 := splitMode(policy)
	if !ok1 || !ok2 || sm == pm || !slices.Equal(srest, prest) {
		return "", false
	}
	return sm, true
}

// splitMode takes the one --permission-mode pair out of flags.
func splitMode(flags []string) (mode string, rest []string, ok bool) {
	i := slices.Index(flags, flagMode)
	if i < 0 || i+1 >= len(flags) || slices.Contains(flags[i+1:], flagMode) {
		return "", nil, false
	}
	rest = append(slices.Clone(flags[:i]), flags[i+2:]...)
	return flags[i+1], rest, true
}

// writeSaved replaces respawnFlags in id's job file, keeping every other
// field, through a temp file and a rename, then reads it back.
func writeSaved(claudeDir string, id ids.SessionID, flags []string) error {
	path := filepath.Join(claudeDir, "jobs", id.Key().String(), "state.json")
	b, err := os.ReadFile(path) //nolint:gosec // path under the user's own config dir
	if err != nil {
		return fmt.Errorf("restoring the session's saved options: %w", err)
	}
	var job map[string]json.RawMessage
	if err := json.Unmarshal(b, &job); err != nil {
		return fmt.Errorf("restoring the session's saved options: %w", err)
	}
	if job["respawnFlags"], err = json.Marshal(flags); err != nil {
		return err
	}
	if err := writeJSONAtomic(path, job); err != nil {
		return fmt.Errorf("restoring the session's saved options: %w", err)
	}
	got, _, err := readSaved(claudeDir, id)
	if err != nil {
		return fmt.Errorf("restoring the session's saved options: %w", err)
	}
	if !slices.Equal(got, flags) {
		return fmt.Errorf("restoring the session's saved options did not hold: %q", got)
	}
	return nil
}

// RelaunchKind is how Relaunch ended without an error.
type RelaunchKind int

const (
	// relaunched: the worker for the fixed id is listed and is the only one.
	// The zero value is no outcome.
	relaunched RelaunchKind = iota + 1
	// relaunchBusy: a worker for the id is in any state but done; untouched.
	relaunchBusy
	// relaunchUnknown: the roster could not be read; nothing touched.
	relaunchUnknown
	// relaunchWindow: a live non-worker session holds the id.
	relaunchWindow
	// relaunchDuplicate: a fork or a second worker for the id; all were
	// stopped. The caller marks the PR needs_you and trips the breaker.
	relaunchDuplicate
	// relaunchNotListed: launched, but no worker for the id appeared in
	// time. Nothing may be recorded.
	relaunchNotListed
	// relaunchPolicy: the session saved options other than the round
	// policy, or they cannot be read; nothing touched.
	relaunchPolicy
)

// RelaunchResult is Relaunch's outcome; Detail says why for the caller's note.
type RelaunchResult struct {
	Kind   RelaunchKind
	Detail string
}

// jobDone is the one job state that lets a worker be stopped.
const jobDone = "done"

// jobBlocked: the worker waits on the user (a question). A launch replaces
// it as it replaces a done one: its round is read, then it is stopped.
const jobBlocked = "blocked"

// Polls bound the waits. A stopped worker's pid ended about 0.75 s after
// `claude stop` returned (2.1.314); a fresh worker was listed before
// `claude --bg` returned.
const (
	lifecyclePoll   = 250 * time.Millisecond
	stopWaitPolls   = 120 // 30 s
	listedWaitPolls = 240 // 60 s
)

// workersFor are the live workers that belong to any of the ids: their
// own, and any fork of one. An error is unknown for them (Roster.For).
func (s Sessions) workersFor(r Roster, sessions ...ids.SessionID) ([]Worker, error) {
	listed, err := r.For(sessions...)
	if err != nil {
		return nil, err
	}
	var out []Worker
	for _, w := range listed {
		if w.PID > 0 && s.Alive(w.PID) {
			out = append(out, w)
		}
	}
	return out, nil
}

// Relaunch starts the next round in id's one background worker, the only
// way a round starts. The caller holds the PR's lock throughout.
//
// No worker: launch. A worker whose job state is explicitly done: settle
// its round (settle may be nil), stop it, wait until its pids are gone,
// launch. Any other state is a round that may still be going, and is left
// alone. After launching, Relaunch holds until the roster lists a worker
// for id, and then checks that it is the only one: a `--bg --resume` with
// flags on a session that ran in the background before forks it (2.1.314;
// LaunchArgv keeps launches flagless), and a fork is a round tick cannot
// track.
func (s Sessions) Relaunch(id ids.SessionID, settle, launch func() error) (RelaunchResult, error) {
	res, err := s.clear(id, settle)
	if err != nil || res.Kind != 0 {
		return res, err
	}
	if err := launch(); err != nil {
		if errors.Is(err, errTimedOut) {
			// Killed mid-launch: its worker may run. Unknown holds the PR
			// until the roster says; "not started" would launch beside it.
			return RelaunchResult{Kind: relaunchUnknown, Detail: err.Error()}, nil
		}
		return RelaunchResult{}, err
	}
	return s.awaitListed(id)
}

// clear makes way for a launch in id: nothing there, or an idle worker
// settled and stopped. A non-zero Kind is why no launch may follow.
func (s Sessions) clear(id ids.SessionID, settle func() error) (RelaunchResult, error) {
	all, err := s.Roster()
	var mine []Worker
	if err == nil {
		mine, err = s.workersFor(all, id)
	}
	var pid int
	if err == nil {
		pid, err = s.Window(id)
	}
	if err != nil {
		// Unknown is an outcome, not a failure: the caller skips the PR.
		return RelaunchResult{Kind: relaunchUnknown, Detail: err.Error()}, nil //nolint:nilerr // see above
	}
	if pid > 0 && !ownedBy(pid, mine) {
		return RelaunchResult{Kind: relaunchWindow, Detail: fmt.Sprintf("pid %d", pid)}, nil
	}
	if forked(id, mine) {
		return s.stopAll(mine, "a second or forked worker was already running")
	}
	if len(mine) == 0 {
		return RelaunchResult{}, nil
	}
	if state := s.JobState(id); state != jobDone && state != jobBlocked {
		return RelaunchResult{Kind: relaunchBusy, Detail: fmt.Sprintf("job state %q", state)}, nil
	}
	if settle != nil {
		if err := settle(); err != nil {
			return RelaunchResult{}, fmt.Errorf("settling the last round first: %w", err)
		}
	}
	return RelaunchResult{}, s.stopAndWait(mine[0])
}

// awaitListed holds until the roster lists a worker for id, then checks it
// is the only one.
func (s Sessions) awaitListed(id ids.SessionID) (RelaunchResult, error) {
	for range listedWaitPolls {
		all, err := s.Roster()
		if err == nil {
			var mine []Worker
			mine, err = s.workersFor(all, id)
			if err == nil && forked(id, mine) {
				return s.stopAll(mine, "the launch left a second or forked worker")
			}
			if err == nil && len(mine) == 1 {
				return RelaunchResult{Kind: relaunched}, nil
			}
		}
		s.Sleep(lifecyclePoll)
	}
	return RelaunchResult{Kind: relaunchNotListed, Detail: "no worker was listed for it after the launch"}, nil
}

// forked: more than one worker for id, or one that is a fork of it.
func forked(id ids.SessionID, mine []Worker) bool {
	return len(mine) > 1 || (len(mine) == 1 && mine[0].SessionID != id)
}

func ownedBy(pid int, ws []Worker) bool {
	for _, w := range ws {
		if pid == w.PID || pid == w.ReplPID {
			return true
		}
	}
	return false
}

// stopAll stops every worker in ws and reports a duplicate.
func (s Sessions) stopAll(ws []Worker, why string) (RelaunchResult, error) {
	var errs []error
	shorts := make([]string, 0, len(ws))
	for _, w := range ws {
		shorts = append(shorts, w.Short.String())
		errs = append(errs, s.stopAndWait(w))
	}
	return RelaunchResult{Kind: relaunchDuplicate, Detail: why + " (" + strings.Join(shorts, ", ") + "); stopped"}, errors.Join(errs...)
}

// stopAndWait stops w and waits until its pids are dead. The roster drops a
// worker before its process ends, so the pid is what is waited on.
func (s Sessions) stopAndWait(w Worker) error {
	if err := s.Stop(w.Short); err != nil {
		return fmt.Errorf("stopping worker %s: %w", w.Short, err)
	}
	for range stopWaitPolls {
		if !s.Alive(w.PID) && (w.ReplPID <= 0 || !s.Alive(w.ReplPID)) {
			return nil
		}
		s.Sleep(lifecyclePoll)
	}
	return fmt.Errorf("worker %s (pid %d) still runs after `claude stop`", w.Short, w.PID)
}

// WindowState is what Claude Code's files say about a window on a PR or a
// session. Unknown holds the PR's launches for the pass and is never
// stored; the zero value is Unknown, so nothing unset reads as Closed.
type WindowState int

const (
	winUnknown WindowState = iota
	winClosed
	winOpen
)

func (w WindowState) String() string {
	return [...]string{winUnknown: "Unknown", winClosed: "Closed", winOpen: "Open"}[w]
}

// WindowOf is a live session for id that is no worker's: a legacy
// interactive window (or a `claude -p` mid-turn). A sessions/ file or a
// roster entry that did not parse and names id cannot rule one out, nor
// in: Unknown.
func (s Sessions) WindowOf(id ids.SessionID) WindowState {
	pid, err := s.Window(id)
	if err != nil {
		return winUnknown
	}
	if pid <= 0 {
		return winClosed
	}
	all, err := s.Roster()
	if err != nil {
		return winUnknown
	}
	mine, err := s.workersFor(all, id)
	switch {
	case err != nil:
		return winUnknown
	case ownedBy(pid, mine):
		return winClosed
	}
	return winOpen
}

// stopArgv is `claude stop <key>` under a pty: it refuses to run without
// a terminal (2.1.314), and neither tick nor a click has one.
func stopArgv(goos string, key ids.WorkerKey) ([]string, error) {
	if key.IsZero() {
		return nil, errors.New("no worker id")
	}
	if goos == darwin {
		return []string{"script", "-q", "/dev/null", claudeCmd, "stop", key.String()}, nil
	}
	return []string{"script", "-qec", claudeCmd + " stop " + key.String(), "/dev/null"}, nil
}

// realSessions wires Sessions to Claude Code's files under claudeDir; run
// executes a command line the way the launchers do (the interactive shell,
// for PATH).
func realSessions(claudeDir, goos string, run func(dir string, argv []string) (string, error)) Sessions {
	return Sessions{
		Roster:   func() (Roster, error) { return readRoster(claudeDir) },
		JobState: func(id ids.SessionID) string { return JobState(claudeDir, id) },
		Window:   func(id ids.SessionID) (int, error) { return sessionPID(claudeDir, id) },
		Unparsed: func() error { return scanSessions(claudeDir).unparsed() },
		Alive:    func(pid int) bool { return processAlive(pid) },
		Stop: func(key ids.WorkerKey) error {
			argv, err := stopArgv(goos, key)
			if err != nil {
				return err
			}
			if out, err := run("/", viaInteractiveShell(shellLine("/", argv))); err != nil {
				return fmt.Errorf("%s", lastLineOr(out, err.Error()))
			}
			return nil
		},
		Sleep:   time.Sleep,
		Saved:   func(id ids.SessionID) ([]string, bool, error) { return readSaved(claudeDir, id) },
		Restore: func(id ids.SessionID, flags []string) error { return writeSaved(claudeDir, id, flags) },
	}
}
