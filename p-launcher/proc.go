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

const procRoot = "/proc"

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
