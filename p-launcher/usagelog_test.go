package main

import (
	"net"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// listenJournal points journalSocket at a temp datagram socket and returns a
// function reading the next entry, so a test can assert on what journald
// would have received.
func listenJournal(t *testing.T) func() string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "journal.sock")
	conn, err := net.ListenPacket("unixgram", path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.Close() })
	old := journalSocket
	journalSocket = path
	t.Cleanup(func() { journalSocket = old })
	return func() string {
		t.Helper()
		if err := conn.SetReadDeadline(time.Now().Add(2 * time.Second)); err != nil {
			t.Fatal(err)
		}
		buf := make([]byte, 64*1024)
		n, _, err := conn.ReadFrom(buf)
		if err != nil {
			t.Fatalf("nothing reached the journal: %v", err)
		}
		return string(buf[:n])
	}
}

func TestUsageFields(t *testing.T) {
	proc := fakeProc(t, map[int][3]string{
		30: {".claude-unwrapp", "claude\x00--add-dir\x00/x", "20"},
		40: {"zsh", "/usr/bin/zsh\x00-c\x00p-launcher list --json", "30"},
		50: {"p-launcher", "p-launcher\x00list\x00--json", "40"},
	})
	fakeCwd(t, proc, 30, "/home/x/p/m/thing/repo")

	got := map[string]string{}
	for _, f := range usageFields(proc, "/home/x/p", 50, []string{"list", "--json"}, "/home/x/code/elsewhere") {
		k, v, _ := strings.Cut(f, "=")
		got[k] = v
	}
	if got["SYSLOG_IDENTIFIER"] != "p-launcher" {
		t.Errorf("tag = %q, want p-launcher", got["SYSLOG_IDENTIFIER"])
	}
	if got["P_LAUNCHER_ARGV"] != `["list" "--json"]` {
		t.Errorf("argv = %q", got["P_LAUNCHER_ARGV"])
	}
	// the claude session's cwd names the project, not p-launcher's own
	if got["P_LAUNCHER_PROJECT"] != "m/thing" {
		t.Errorf("project = %q, want m/thing", got["P_LAUNCHER_PROJECT"])
	}
	if got["P_LAUNCHER_CLAUDE_PID"] != "30" {
		t.Errorf("claude pid = %q, want 30", got["P_LAUNCHER_CLAUDE_PID"])
	}
	// the shell line is the thing that got the arguments wrong, so it must
	// survive into the entry; the walk stops at claude
	callers := got["P_LAUNCHER_CALLERS"]
	if !strings.Contains(callers, "p-launcher list --json") || !strings.HasSuffix(callers, "30:claude --add-dir /x") {
		t.Errorf("callers = %q", callers)
	}
	if !strings.Contains(got["MESSAGE"], "rejected invocation") {
		t.Errorf("message = %q", got["MESSAGE"])
	}
}

// A project cannot be derived when no claude is above the call (the i3
// binding) - the entry still has to say what was run.
func TestUsageFieldsWithoutClaude(t *testing.T) {
	proc := fakeProc(t, map[int][3]string{
		10: {"i3", "i3", "1"},
		50: {"p-launcher", "p-launcher\x00menu\x00--toggle\x00F5", "10"},
	})
	fields := strings.Join(usageFields(proc, "/home/x/p", 50, []string{"menu", "--toggle", "F5"}, ""), "\n")
	if !strings.Contains(fields, "P_LAUNCHER_PROJECT=\n") || !strings.Contains(fields, "claude_pid=0") {
		t.Errorf("fields = %q", fields)
	}
	if !strings.Contains(fields, `["menu" "--toggle" "F5"]`) {
		t.Errorf("fields = %q", fields)
	}
}

// Every branch that prints the usage text logs, which is only true while the
// logging lives in usage() itself.
func TestRunLogsEveryUsageError(t *testing.T) {
	read := listenJournal(t)
	for _, args := range [][]string{
		{"bogus"},
		{"list", "--json"},
		{"open"},
		{"tend", "--max", "-1"},
	} {
		if err := run(args); err == nil {
			t.Fatalf("%v: want a usage error", args)
		}
		entry := read()
		if !strings.Contains(entry, "SYSLOG_IDENTIFIER=p-launcher") ||
			!strings.Contains(entry, "P_LAUNCHER_ARGV="+quoteArgs(args)) {
			t.Errorf("%v logged %q", args, entry)
		}
	}
}

func quoteArgs(args []string) string {
	q := make([]string, len(args))
	for i, a := range args {
		q[i] = `"` + a + `"`
	}
	return "[" + strings.Join(q, " ") + "]"
}

// A caller's command line is kept from the end: Claude Code's shell preamble
// runs to a kilobyte before the command it was asked to run.
func TestProcCmdlineKeepsTail(t *testing.T) {
	long := strings.Repeat("x", cmdlineMax) + "\x00p-launcher list --json"
	proc := fakeProc(t, map[int][3]string{9: {"zsh", long, "1"}})
	got := procCmdline(proc, 9)
	if len(got) > cmdlineMax+len("…") || !strings.HasSuffix(got, "p-launcher list --json") {
		t.Errorf("cmdline = %q (%d bytes)", got, len(got))
	}
}
