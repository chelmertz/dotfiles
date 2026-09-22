package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// Session liveness via the Claude Code process. A hook runs as a descendant
// of the `claude` process (claude → shell → p-launcher); the hook records
// that ancestor's PID on the session, and readers drop session_state rows
// whose process is gone. WM-independent, so it also covers sessions not
// started from the launcher.

// procRoot is the process table. A variable so tests can substitute a fake.
var procRoot = "/proc"

// claudePID walks up from pid and returns the first ancestor that is a
// claude process, or 0 when none is found within a few levels.
func claudePID(proc string, pid int) int {
	for i := 0; i < 6 && pid > 1; i++ {
		if isClaude(proc, pid) {
			return pid
		}
		pid = parentPID(proc, pid)
	}
	return 0
}

// isClaude reports whether pid is alive and runs claude: argv[0]'s basename
// is "claude" (the nix wrapper's comm is ".claude-unwrapp", so comm is only
// a fallback). Checking the command guards against PID reuse.
func isClaude(proc string, pid int) bool {
	cmd, err := os.ReadFile(filepath.Join(proc, strconv.Itoa(pid), "cmdline"))
	if err != nil {
		return false
	}
	argv0, _, _ := bytes.Cut(cmd, []byte{0})
	if filepath.Base(string(argv0)) == "claude" {
		return true
	}
	comm, err := os.ReadFile(filepath.Join(proc, strconv.Itoa(pid), "comm"))
	return err == nil && strings.Contains(string(comm), "claude")
}

// parentPID reads the PPID from /proc/<pid>/stat: the field after the
// parenthesised comm, which may itself contain spaces and parentheses.
func parentPID(proc string, pid int) int {
	b, err := os.ReadFile(filepath.Join(proc, strconv.Itoa(pid), "stat"))
	if err != nil {
		return 0
	}
	i := bytes.LastIndexByte(b, ')')
	if i < 0 {
		return 0
	}
	f := strings.Fields(string(b[i+1:]))
	if len(f) < 2 {
		return 0
	}
	n, _ := strconv.Atoi(f[1])
	return n
}

// liveFromProc reports which projects have a claude process running inside
// them, keyed by "namespace/name".
//
// This is the half of liveness the database cannot see. `session_state` is
// hook-derived, so a session that has not yet submitted a prompt has no row at
// all, and `open` is the i3 window tag, which misses a claude started in an
// untagged terminal or under tmux. On 2026-09-13 the signals were compared
// across twelve projects: the session rows found three, /proc found four, and
// the one they missed - personal/1password-systemauth - was a session that had
// never typed anything.
//
// Unreadable entries are skipped, never guessed at: /proc/<pid>/cwd is denied
// for other users' processes, and a pid can exit mid-scan.
func liveFromProc(proc, root string) map[string]bool {
	live := map[string]bool{}
	ents, err := os.ReadDir(proc)
	if err != nil {
		return live
	}
	for _, e := range ents {
		pid, err := strconv.Atoi(e.Name())
		if err != nil || !isClaude(proc, pid) {
			continue
		}
		cwd, err := os.Readlink(filepath.Join(proc, e.Name(), "cwd"))
		if err != nil {
			continue
		}
		if p := projectPathFor(root, cwd); p != "" {
			live[p] = true
		}
	}
	return live
}
