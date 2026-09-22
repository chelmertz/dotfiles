package main

import (
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// A usage error is the one failure that is nobody's fault but the caller's,
// and it arrives as a desktop notification several times a day saying only
// that the arguments were wrong - never which arguments, or who passed them.
// Every rejected invocation therefore goes to the journal with its argv, its
// cwd, the process chain above it up to the claude session that spawned it,
// and that session's project. Tagged p-launcher, the identifier the i3
// binding already gets from `systemd-cat -t p-launcher`, so one query reads
// both: journalctl --user -t p-launcher.
//
// Nothing here may change what the command does: the journal is written
// best-effort and every failure is dropped, since a usage error is already
// being reported to the user by the caller.

// journalSocket is journald's native datagram socket. Overridden in tests.
var journalSocket = "/run/systemd/journal/socket"

// cmdlineMax bounds each caller's command line in the entry.
const cmdlineMax = 400

// logUsage records a rejected invocation. Called from usage(), so it covers
// every branch that returns one.
func logUsage(args []string) {
	cwd, _ := os.Getwd()
	home, err := os.UserHomeDir()
	if err != nil {
		home = ""
	}
	_ = journalSend(usageFields(procRoot, filepath.Join(home, "p"), os.Getpid(), args, cwd))
}

// usageFields builds the journal entry for a rejected invocation, in
// journald's native "NAME=value" form. The custom P_LAUNCHER_* fields carry
// the same facts as the message so `journalctl -o json` can group them:
// argv is what to fix, project and claude pid are who to tell.
func usageFields(proc, root string, pid int, args []string, cwd string) []string {
	chain := callerChain(proc, pid)
	claude := claudePID(proc, pid)
	project := projectPathFor(root, cwd)
	if claude > 0 {
		// The session's own cwd beats p-launcher's: a hook or a launched
		// command can run from anywhere, the session sits in the project.
		if dir, err := os.Readlink(filepath.Join(proc, strconv.Itoa(claude), "cwd")); err == nil {
			if p := projectPathFor(root, dir); p != "" {
				project = p
			}
		}
	}
	msg := fmt.Sprintf("rejected invocation: argv=%q cwd=%s project=%s claude_pid=%d callers=%s",
		args, cwd, orNone(project), claude, orNone(strings.Join(chain, " <- ")))
	return []string{
		"MESSAGE=" + msg,
		"PRIORITY=4", // warning
		"SYSLOG_IDENTIFIER=p-launcher",
		"P_LAUNCHER_EVENT=usage",
		"P_LAUNCHER_ARGV=" + fmt.Sprintf("%q", args),
		"P_LAUNCHER_CWD=" + cwd,
		"P_LAUNCHER_PROJECT=" + project,
		"P_LAUNCHER_CLAUDE_PID=" + strconv.Itoa(claude),
		"P_LAUNCHER_CALLERS=" + strings.Join(chain, " <- "),
	}
}

func orNone(s string) string {
	if s == "" {
		return "-"
	}
	return s
}

// callerChain lists the command lines above pid, nearest first, stopping at
// the claude session that owns the call (its shell command is the thing that
// got the arguments wrong) or after a few levels. The i3 binding has no
// claude ancestor and simply runs out of levels.
func callerChain(proc string, pid int) []string {
	var chain []string
	for i := 0; i < 6; i++ {
		pid = parentPID(proc, pid)
		if pid <= 1 {
			break
		}
		cmd := procCmdline(proc, pid)
		if cmd == "" {
			break
		}
		chain = append(chain, strconv.Itoa(pid)+":"+cmd)
		if isClaude(proc, pid) {
			break
		}
	}
	return chain
}

// procCmdline reads pid's argv as one line, NULs turned into spaces, keeping
// the last cmdlineMax bytes of a long one. Claude Code runs every Bash tool
// call through a shell whose argv starts with a kilobyte of snapshot-sourcing
// preamble and ends with the command that was actually run, so the tail is
// the half worth keeping.
func procCmdline(proc string, pid int) string {
	b, err := os.ReadFile(filepath.Join(proc, strconv.Itoa(pid), "cmdline"))
	if err != nil {
		return ""
	}
	line := strings.TrimSpace(strings.ReplaceAll(strings.Trim(string(b), "\x00"), "\x00", " "))
	if len(line) > cmdlineMax {
		line = "…" + line[len(line)-cmdlineMax:]
	}
	return line
}

// journalSend writes one entry to journald. Values are single-line: the
// native protocol has a binary form for embedded newlines, and a command line
// that needs it is not worth the framing, so newlines become spaces.
func journalSend(fields []string) error {
	conn, err := net.Dial("unixgram", journalSocket)
	if err != nil {
		return err
	}
	defer conn.Close()
	var b strings.Builder
	for _, f := range fields {
		b.WriteString(strings.ReplaceAll(f, "\n", " "))
		b.WriteByte('\n')
	}
	_, err = conn.Write([]byte(b.String()))
	return err
}
