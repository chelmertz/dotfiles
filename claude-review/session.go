package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/chelmertz/dotfiles/claude-review/ids"
)

// SessionExists reports whether Claude Code has a transcript for id under
// any project in claudeDir (normally ~/.claude). Transcripts live at
// projects/<encoded cwd>/<id>.jsonl; the glob across projects mirrors how
// `claude --resume <id>` searches, so a clone that moved still resumes.
func SessionExists(claudeDir string, id ids.SessionID) bool {
	matches, _ := filepath.Glob(filepath.Join(claudeDir, "projects", "*", id.String()+".jsonl"))
	return len(matches) > 0
}

// Live is the process that holds a session open; PID 0 means none does.
type Live struct {
	PID int
	// Background is a supervisor worker, which `claude attach` opens.
	Background bool
	// Err: Claude Code's files cannot say, for an entry naming the
	// session did not parse. Nothing may start a copy of it.
	Err error
}

// SessionRunning reports the live process for id. `--resume` on a session
// that is already open starts a second copy instead of continuing it, so a
// click outside review_repos (the window path) checks this first. A background worker is in the roster; every
// other live session, a terminal window or a `claude -p`, is only in
// sessions/. A worker wins: it may have its own sessions/ entry too, and it
// is the one kind that can be attached to.
func SessionRunning(claudeDir string, id ids.SessionID) Live {
	pid, err := workerPID(claudeDir, id)
	if err != nil {
		return Live{Err: err}
	}
	if pid > 0 {
		return Live{PID: pid, Background: true}
	}
	pid, err = sessionPID(claudeDir, id)
	return Live{PID: pid, Err: err}
}

// workerPID is the live background worker for id from the roster, or 0.
// No roster is none; one that cannot be read, or an entry naming id that
// does not parse, is an error.
func workerPID(claudeDir string, id ids.SessionID) (int, error) {
	r, err := readRoster(claudeDir)
	if err != nil {
		return 0, err
	}
	ws, err := r.For(id)
	if err != nil {
		return 0, err
	}
	for _, w := range ws {
		if w.SessionID == id && w.PID > 0 && processAlive(w.PID) {
			return w.PID, nil
		}
	}
	return 0, nil
}

// liveSession is the part of ~/.claude/sessions/<pid>.json this tool reads.
// Claude Code writes one per running session, interactive or -p, and removes
// it on a clean exit; a crash or a reboot leaves it behind with a pid that
// may since belong to something else, hence procStart. Observed on 2.1.310:
// a window started with `claude --resume <id>` records <id>, not a fresh id.
type liveSession struct {
	PID       int           `json:"pid"`
	SessionID ids.SessionID `json:"sessionId"`
	ProcStart string        `json:"procStart"`
	Kind      string        `json:"kind"`
}

// sessionScan is one read of sessions/: each file parsed on its own.
type sessionScan struct {
	live   []liveSession
	failed []failedEntry
}

// scanSessions reads every sessions/ file. A file whose pid is dead is
// ignored, as is one whose pid was recycled since it was written. A file
// that does not parse, or parses without a session id, is kept as failed
// unless its name is a dead pid: Claude Code names each file by its pid.
func scanSessions(claudeDir string) sessionScan {
	var scan sessionScan
	files, _ := filepath.Glob(filepath.Join(claudeDir, "sessions", "*.json"))
	for _, f := range files {
		b, err := os.ReadFile(f) //nolint:gosec // path under the user's own config dir
		var s liveSession
		if err == nil {
			err = json.Unmarshal(b, &s)
		}
		if err == nil && s.SessionID.IsZero() {
			err = errors.New("no session id")
		}
		if err != nil {
			if n, perr := strconv.Atoi(strings.TrimSuffix(filepath.Base(f), ".json")); perr == nil && (n <= 0 || !processAlive(n)) {
				continue
			}
			scan.failed = append(scan.failed, failedEntry{where: f, raw: b, err: err})
			continue
		}
		if s.PID <= 0 || !processAlive(s.PID) {
			continue
		}
		// Claude Code's own rule: no recorded start time, or none readable
		// now, trusts the live pid.
		if now := procStart(s.PID); s.ProcStart == "" || now == "" || now == s.ProcStart {
			scan.live = append(scan.live, s)
		}
	}
	return scan
}

// of is the live session for id and its kind ("bg" for a background
// worker, whether or not the roster lists it), or 0. An error names the
// files that did not parse and name id.
func (scan sessionScan) of(id ids.SessionID) (int, string, error) {
	var errs []error
	for _, f := range scan.failed {
		if f.names("", id) {
			errs = append(errs, fmt.Errorf("reading Claude Code's session file %s: %w", f.where, f.err))
		}
	}
	if len(errs) > 0 {
		return 0, "", errors.Join(errs...)
	}
	for _, s := range scan.live {
		if s.SessionID == id {
			return s.PID, s.Kind, nil
		}
	}
	return 0, "", nil
}

// unparsed names every file that did not parse, or is nil.
func (scan sessionScan) unparsed() error {
	var errs []error
	for _, f := range scan.failed {
		errs = append(errs, fmt.Errorf("reading Claude Code's session file %s: %w", f.where, f.err))
	}
	return errors.Join(errs...)
}

// sessionPID is the live process sessions/ lists for id, or 0; an error is
// unknown for id.
func sessionPID(claudeDir string, id ids.SessionID) (int, error) {
	pid, _, err := scanSessions(claudeDir).of(id)
	return pid, err
}

// procStart is the start time Claude Code records as procStart for pid, so a
// recycled pid can be told from the process that wrote the file. "" when it
// cannot be read. A variable so tests need no live processes.
var procStart = func(pid int) string { return readProcStart("/proc", pid, psLstart) }

// readProcStart is what Claude Code itself does (2.1.310): field 22 of
// /proc/<pid>/stat, the start time in clock ticks since boot, where there is
// a /proc; otherwise `ps -o lstart=` in the C locale and UTC (macOS).
func readProcStart(procRoot string, pid int, ps func(pid int) (string, error)) string {
	if b, err := os.ReadFile(filepath.Join(procRoot, strconv.Itoa(pid), "stat")); err == nil { //nolint:gosec // path built from a pid
		// The command name in field 2 may hold spaces and parentheses, so
		// count fields from after the last ')': field 3 is index 0.
		fields := strings.Fields(string(b)[strings.LastIndexByte(string(b), ')')+1:])
		if len(fields) > 19 {
			return fields[19]
		}
		return ""
	}
	out, err := ps(pid)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(out)
}

func psLstart(pid int) (string, error) {
	cmd := exec.Command("ps", "-o", "lstart=", "-p", strconv.Itoa(pid)) //nolint:gosec // a pid
	// Only the two variables that decide its output: the rest of the
	// environment cannot change lstart's text in the C locale and UTC.
	cmd.Env = []string{"LC_ALL=C", "TZ=UTC"}
	out, err := cmd.Output()
	return string(out), err
}

// processAlive is signal 0 on the pid; it is a variable so tests can run
// without live processes.
// jobNow is the clock for a job's age; a variable for tests.
var jobNow = time.Now

var processAlive = func(pid int) bool {
	return syscall.Kill(pid, 0) == nil
}

// JobState is Claude Code's state for a background session's job, from
// jobs/<short id>/state.json: "working", "blocked", "done", "stopped", or ""
// when there is no readable job, which no caller reads as done or blocked.
// Observed on 2.1.314.
func JobState(claudeDir string, id ids.SessionID) string {
	return readJob(claudeDir, id).State
}

// BlockedOn reports whether id's job is blocked, and why in the job's own
// words: needs when it asks the user something, else detail (a permission
// it lacks reads "needs Bash permission" there). Observed on 2.1.314.
func BlockedOn(claudeDir string, id ids.SessionID) (string, bool) {
	j := readJob(claudeDir, id)
	if j.State != jobBlocked {
		return "", false
	}
	if j.Needs != "" {
		return j.Needs, true
	}
	return j.Detail, true
}

// job is the part of jobs/<short id>/state.json this tool reads; the file
// is named by the session id's key, so one that does not parse is unknown
// for that session alone.
type job struct {
	State     string    `json:"state"`
	Detail    string    `json:"detail"`
	Needs     string    `json:"needs"`
	Tempo     string    `json:"tempo"`
	UpdatedAt time.Time `json:"updatedAt"`
	// RespawnFlags are the options a background launch saved (readSaved).
	RespawnFlags *[]string `json:"respawnFlags"`
}

// idleEnded is how long a job may sit at working, idle and unchanged before
// its turn counts as over. Seen 2026-11-20 on 2.1.325: a worker handed to a
// bg-spare process kept working/idle for good after its last message.
const idleEnded = 10 * time.Minute

// stale is a job left at working that has done nothing for idleEnded.
func (j job) stale() bool {
	return j.State == "working" && j.Tempo == "idle" && !j.UpdatedAt.IsZero() && jobNow().Sub(j.UpdatedAt) >= idleEnded
}

// readJobFile is the one parser of a job file. ok is false when there is
// none; an error names the file.
func readJobFile(claudeDir string, id ids.SessionID) (job, bool, error) {
	var j job
	path := filepath.Join(claudeDir, "jobs", id.Key().String(), "state.json")
	b, err := os.ReadFile(path) //nolint:gosec // path under the user's own config dir
	if errors.Is(err, fs.ErrNotExist) {
		return j, false, nil
	}
	if err == nil {
		err = json.Unmarshal(b, &j)
	}
	if err != nil {
		return job{}, true, fmt.Errorf("%s: %w", path, err)
	}
	return j, true, nil
}

// readJob is the zero job when there is none or it cannot be read: unknown,
// which no caller reads as done or blocked.
func readJob(claudeDir string, id ids.SessionID) job {
	if id.IsZero() {
		return job{}
	}
	j, _, _ := readJobFile(claudeDir, id)
	return j
}

// RoundRunning reports whether a round may still be in progress in id. A
// background worker stays alive and idle after its round ends, so for one
// the job state decides: done, blocked (settle handles it), stopped and a
// stale working job are not running; no readable state, or one never seen,
// counts as running. A background session is one the roster lists or whose
// session file says bg: the roster can drop a worker Claude Code keeps alive.
// An unreadable roster, or a roster entry or session file naming id that
// does not parse, counts as running too. Any other live session, a window
// or a `claude -p`, is running.
func RoundRunning(claudeDir string, id ids.SessionID) bool {
	if _, err := readRoster(claudeDir); err != nil {
		return true
	}
	live := SessionRunning(claudeDir, id)
	if live.Err != nil {
		return true
	}
	if live.PID == 0 {
		return false
	}
	if !live.Background {
		if _, kind, err := scanSessions(claudeDir).of(id); err != nil || kind != "bg" {
			return true
		}
	}
	j := readJob(claudeDir, id)
	switch j.State {
	case jobDone, jobBlocked, "stopped":
		return false
	}
	return !j.stale()
}
