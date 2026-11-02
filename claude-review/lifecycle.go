package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"
)

// Worker is one background worker in Claude Code's roster
// (~/.claude/daemon/roster.json), as of 2.1.314.
type Worker struct {
	// Short is the roster key, the first 8 characters of the session id;
	// `claude stop` and `claude attach` take it.
	Short     string
	SessionID string
	PID       int
	// ReplPID is the worker's own session process, which also has a
	// sessions/ entry; it is not a window.
	ReplPID int
	// Fork: Claude Code forked LaunchID into a new session instead of
	// continuing it. Seen when `claude --bg --resume <id>` passed flags to a
	// session that had run in the background before (2.1.314).
	Fork     bool
	LaunchID string
}

// rosterFile is the part of roster.json this tool reads.
type rosterFile struct {
	Workers map[string]struct {
		SessionID string `json:"sessionId"`
		PID       int    `json:"pid"`
		ReplPID   int    `json:"replPid"`
		Dispatch  struct {
			Launch struct {
				SessionID string `json:"sessionId"`
				Fork      bool   `json:"fork"`
			} `json:"launch"`
		} `json:"dispatch"`
	} `json:"workers"`
}

// readRoster is three-valued: no file is no workers (nil error); a file
// that cannot be read or parsed is an error, which every launcher treats as
// "do not launch": an unknown roster may hold a worker a launch would fork.
func readRoster(claudeDir string) ([]Worker, error) {
	b, err := os.ReadFile(filepath.Join(claudeDir, "daemon", "roster.json")) //nolint:gosec // path under the user's own config dir
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("reading Claude Code's roster: %w", err)
	}
	var r rosterFile
	if err := json.Unmarshal(b, &r); err != nil {
		return nil, fmt.Errorf("reading Claude Code's roster: %w", err)
	}
	out := make([]Worker, 0, len(r.Workers))
	for short, w := range r.Workers {
		out = append(out, Worker{Short: short, SessionID: w.SessionID, PID: w.PID, ReplPID: w.ReplPID,
			Fork: w.Dispatch.Launch.Fork, LaunchID: w.Dispatch.Launch.SessionID})
	}
	return out, nil
}

// Sessions is what a launcher needs from Claude Code to keep one worker per
// PR under the PR's fixed session id. Every function is a variable so tests
// drive a fake roster.
type Sessions struct {
	Roster   func() ([]Worker, error)
	JobState func(id string) string
	// Window is a live process sessions/ lists for id, or 0: a legacy
	// interactive window, a `claude -p`, or a worker's own repl.
	Window func(id string) int
	Alive  func(pid int) bool
	// Stop runs `claude stop <short>`; it returns before the pid ends.
	Stop  func(short string) error
	Sleep func(time.Duration)
	// Saved is the options id's background session was first launched
	// with; ran is false when it never ran in the background.
	Saved func(id string) (flags []string, ran bool, err error)
}

// readSaved reads the options Claude Code saved for id's background
// session from jobs/<short>/state.json (respawnFlags, the launch's flags
// without --bg, --resume and the prompt; 2.1.314). No job file means the
// session never ran in the background. A job without readable options is
// an error: its saved options are unknown.
func readSaved(claudeDir, id string) ([]string, bool, error) {
	if len(id) < 8 {
		return nil, false, fmt.Errorf("not a session id: %q", id)
	}
	b, err := os.ReadFile(filepath.Join(claudeDir, "jobs", id[:8], "state.json")) //nolint:gosec // path under the user's own config dir
	if errors.Is(err, fs.ErrNotExist) {
		return nil, false, nil
	}
	if err != nil {
		return nil, true, err
	}
	var j struct {
		RespawnFlags *[]string `json:"respawnFlags"`
	}
	if err := json.Unmarshal(b, &j); err != nil {
		return nil, true, fmt.Errorf("reading the session's saved options: %w", err)
	}
	if j.RespawnFlags == nil {
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
func (s Sessions) LaunchArgv(id string, policy, first, later []string) ([]string, error) {
	saved, ran, err := s.Saved(id)
	switch {
	case err != nil:
		return nil, err
	case !ran:
		return first, nil
	case slices.Equal(saved, policy):
		return later, nil
	}
	return nil, fmt.Errorf("the session was started with other options than the round policy: %q", saved)
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

// Polls bound the waits. A stopped worker's pid ended about 0.75 s after
// `claude stop` returned (2.1.314); a fresh worker was listed before
// `claude --bg` returned.
const (
	lifecyclePoll   = 250 * time.Millisecond
	stopWaitPolls   = 120 // 30 s
	listedWaitPolls = 240 // 60 s
)

// workersFor are the live workers that belong to id: its own, and any fork
// of it.
func (s Sessions) workersFor(id string, all []Worker) []Worker {
	var out []Worker
	for _, w := range all {
		if (w.SessionID == id || (w.Fork && w.LaunchID == id)) && w.PID > 0 && s.Alive(w.PID) {
			out = append(out, w)
		}
	}
	return out
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
func (s Sessions) Relaunch(id string, settle, launch func() error) (RelaunchResult, error) {
	res, err := s.clear(id, settle)
	if err != nil || res.Kind != 0 {
		return res, err
	}
	if err := launch(); err != nil {
		return RelaunchResult{}, err
	}
	return s.awaitListed(id)
}

// clear makes way for a launch in id: nothing there, or an idle worker
// settled and stopped. A non-zero Kind is why no launch may follow.
func (s Sessions) clear(id string, settle func() error) (RelaunchResult, error) {
	all, err := s.Roster()
	if err != nil {
		// Unknown is an outcome, not a failure: the caller skips the PR.
		return RelaunchResult{Kind: relaunchUnknown, Detail: err.Error()}, nil //nolint:nilerr // see above
	}
	mine := s.workersFor(id, all)
	if pid := s.Window(id); pid > 0 && !ownedBy(pid, mine) {
		return RelaunchResult{Kind: relaunchWindow, Detail: fmt.Sprintf("pid %d", pid)}, nil
	}
	if forked(id, mine) {
		return s.stopAll(mine, "a second or forked worker was already running")
	}
	if len(mine) == 0 {
		return RelaunchResult{}, nil
	}
	if state := s.JobState(id); state != jobDone {
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
func (s Sessions) awaitListed(id string) (RelaunchResult, error) {
	for range listedWaitPolls {
		if all, err := s.Roster(); err == nil {
			mine := s.workersFor(id, all)
			if forked(id, mine) {
				return s.stopAll(mine, "the launch left a second or forked worker")
			}
			if len(mine) == 1 {
				return RelaunchResult{Kind: relaunched}, nil
			}
		}
		s.Sleep(lifecyclePoll)
	}
	return RelaunchResult{Kind: relaunchNotListed, Detail: "no worker was listed for it after the launch"}, nil
}

// forked: more than one worker for id, or one that is a fork of it.
func forked(id string, mine []Worker) bool {
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
		shorts = append(shorts, w.Short)
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

// WindowOpen reports a live session for id that is no worker's: a legacy
// interactive window (or a `claude -p` mid-turn). An unreadable roster
// cannot rule a window out, so it counts as one.
func (s Sessions) WindowOpen(id string) bool {
	pid := s.Window(id)
	if pid <= 0 {
		return false
	}
	all, err := s.Roster()
	return err != nil || !ownedBy(pid, s.workersFor(id, all))
}

// shortID is a roster key as Claude Code writes it; anything else is not
// passed to a shell.
var shortID = regexp.MustCompile(`^[0-9a-f]{8}$`)

// stopArgv is `claude stop <short>` under a pty: it refuses to run without
// a terminal (2.1.314), and neither tick nor a click has one.
func stopArgv(goos, short string) ([]string, error) {
	if !shortID.MatchString(short) {
		return nil, fmt.Errorf("not a worker id: %q", short)
	}
	if goos == darwin {
		return []string{"script", "-q", "/dev/null", claudeCmd, "stop", short}, nil
	}
	return []string{"script", "-qec", claudeCmd + " stop " + short, "/dev/null"}, nil
}

// realSessions wires Sessions to Claude Code's files under claudeDir; run
// executes a command line the way the launchers do (the interactive shell,
// for PATH).
func realSessions(claudeDir, goos string, run func(dir string, argv []string) (string, error)) Sessions {
	return Sessions{
		Roster:   func() ([]Worker, error) { return readRoster(claudeDir) },
		JobState: func(id string) string { return JobState(claudeDir, id) },
		Window:   func(id string) int { return sessionPID(claudeDir, id) },
		Alive:    func(pid int) bool { return processAlive(pid) },
		Stop: func(short string) error {
			argv, err := stopArgv(goos, short)
			if err != nil {
				return err
			}
			if out, err := run("/", viaInteractiveShell(shellLine("/", argv))); err != nil {
				return fmt.Errorf("%s", lastLineOr(out, err.Error()))
			}
			return nil
		},
		Sleep: time.Sleep,
		Saved: func(id string) ([]string, bool, error) { return readSaved(claudeDir, id) },
	}
}
