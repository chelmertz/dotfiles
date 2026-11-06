package main

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"

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

// roster is the part of ~/.claude/daemon/roster.json the window path reads:
// the background workers the supervisor currently runs. The file is Claude
// Code's own and may change shape; here every failure to read it means "not
// running", which only costs a forked copy. Rounds go through readRoster
// instead, where an unreadable roster means no launch.
type roster struct {
	Workers map[string]struct {
		SessionID ids.SessionID `json:"sessionId"`
		PID       int           `json:"pid"`
	} `json:"workers"`
}

// Live is the process that holds a session open; PID 0 means none does.
type Live struct {
	PID int
	// Background is a supervisor worker, which `claude attach` opens.
	Background bool
}

// SessionRunning reports the live process for id. `--resume` on a session
// that is already open starts a second copy instead of continuing it, so a
// click outside review_repos (the window path) checks this first. A background worker is in the roster; every
// other live session, a terminal window or a `claude -p`, is only in
// sessions/. A worker wins: it may have its own sessions/ entry too, and it
// is the one kind that can be attached to.
func SessionRunning(claudeDir string, id ids.SessionID) Live {
	if pid := workerPID(claudeDir, id); pid > 0 {
		return Live{PID: pid, Background: true}
	}
	return Live{PID: sessionPID(claudeDir, id)}
}

// workerPID is the live background worker for id from the roster, or 0.
func workerPID(claudeDir string, id ids.SessionID) int {
	b, err := os.ReadFile(filepath.Join(claudeDir, "daemon", "roster.json")) //nolint:gosec // path under the user's own config dir
	if err != nil {
		return 0
	}
	var r roster
	if json.Unmarshal(b, &r) != nil {
		return 0
	}
	for _, w := range r.Workers {
		if w.SessionID == id && w.PID > 0 && processAlive(w.PID) {
			return w.PID
		}
	}
	return 0
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
}

// sessionPID is the live process sessions/ lists for id, or 0. Like the
// roster, anything unreadable counts as not running.
func sessionPID(claudeDir string, id ids.SessionID) int {
	files, _ := filepath.Glob(filepath.Join(claudeDir, "sessions", "*.json"))
	for _, f := range files {
		b, err := os.ReadFile(f) //nolint:gosec // path under the user's own config dir
		if err != nil {
			continue
		}
		var s liveSession
		if json.Unmarshal(b, &s) != nil || s.SessionID != id || s.PID <= 0 || !processAlive(s.PID) {
			continue
		}
		// Claude Code's own rule: no recorded start time, or none readable
		// now, trusts the live pid.
		if now := procStart(s.PID); s.ProcStart == "" || now == "" || now == s.ProcStart {
			return s.PID
		}
	}
	return 0
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
var processAlive = func(pid int) bool {
	return syscall.Kill(pid, 0) == nil
}

// JobState is Claude Code's state for a background session's job, from
// jobs/<short id>/state.json: "working", "blocked", "done", "stopped", or ""
// when there is no readable job. Observed on 2.1.314.
func JobState(claudeDir string, id ids.SessionID) string {
	return readJob(claudeDir, id).State
}

// BlockedOn reports whether id's job is blocked, and why in the job's own
// words: needs when it asks the user something, else detail (a permission
// it lacks reads "needs Bash permission" there). Observed on 2.1.314.
func BlockedOn(claudeDir string, id ids.SessionID) (string, bool) {
	j := readJob(claudeDir, id)
	if j.State != "blocked" {
		return "", false
	}
	if j.Needs != "" {
		return j.Needs, true
	}
	return j.Detail, true
}

type job struct {
	State  string `json:"state"`
	Detail string `json:"detail"`
	Needs  string `json:"needs"`
}

// readJob is the zero job when there is none or it cannot be read.
func readJob(claudeDir string, id ids.SessionID) job {
	var j job
	if id.IsZero() {
		return j
	}
	b, err := os.ReadFile(filepath.Join(claudeDir, "jobs", id.Key().String(), "state.json")) //nolint:gosec // path under the user's own config dir
	if err != nil || json.Unmarshal(b, &j) != nil {
		return job{}
	}
	return j
}

// RoundRunning reports whether a round may still be in progress in id. A
// background worker stays alive and idle after its round ends, so for one
// the job state decides: only done, blocked (settle handles it) and stopped
// are not running; no readable state, or one never seen, counts as running.
// An unreadable roster counts as running too. Any other live session, a
// window or a `claude -p`, is running.
func RoundRunning(claudeDir string, id ids.SessionID) bool {
	if _, err := readRoster(claudeDir); err != nil {
		return true
	}
	live := SessionRunning(claudeDir, id)
	if !live.Background {
		return live.PID > 0
	}
	switch JobState(claudeDir, id) {
	case jobDone, "blocked", "stopped":
		return false
	}
	return true
}
