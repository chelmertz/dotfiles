package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Asking for the synopsis is success, and it goes to stdout: a session that
// guesses at --help must not get a sticky critical notification carrying the
// text it asked for.
func TestHelpIsNotAnError(t *testing.T) {
	for _, verb := range []string{"help", "--help", "-h"} {
		if err := run([]string{verb}); err != nil {
			t.Errorf("%s: %v", verb, err)
		}
	}
}

// Every verb the switch accepts appears in the help, and vice versa: the two
// drift apart silently otherwise, which is how a caller ends up guessing.
func TestHelpListsEveryVerb(t *testing.T) {
	var b strings.Builder
	printHelp(&b)
	out := b.String()
	for _, verb := range []string{"list", "open", "create", "describe", "adopt", "archive",
		"link", "links", "menu", "hook", "session-brief", "desktop", "tend", "kv",
		"backup", "brief", "report", "help"} {
		if !strings.Contains(out, "\n  "+verb) && !strings.Contains(out, "\n  "+verb+" ") {
			t.Errorf("help does not list %q:\n%s", verb, out)
		}
	}
	if strings.Contains(out, "usage: usage") || strings.Contains(out, " | ") {
		t.Errorf("help is still the one-liner:\n%s", out)
	}
	// ctx is plumbing for the statusline, deliberately undocumented
	if strings.Contains(out, "\n  ctx") {
		t.Errorf("ctx should stay out of the help:\n%s", out)
	}
}

// A failure a claude session can read on stderr raises no desktop
// notification; the same failure from the hotkey path still does.
func TestReportSkipsNotifyUnderClaude(t *testing.T) {
	log := filepath.Join(t.TempDir(), "notify.log")
	t.Setenv("P_LAUNCHER_NOTIFY_LOG", log)

	me := os.Getpid()
	withClaude := fakeProc(t, map[int][3]string{
		30: {".claude-unwrapp", "claude\x00--add-dir\x00/x", "1"},
		me: {"p-launcher", "p-launcher\x00list", "30"},
	})
	old := procRoot
	t.Cleanup(func() { procRoot = old })

	procRoot = withClaude
	report(errUsageProbe{})
	if b, err := os.ReadFile(log); err == nil && len(b) > 0 {
		t.Errorf("notified a claude caller: %s", b)
	}

	procRoot = fakeProc(t, map[int][3]string{
		10: {"i3", "i3", "1"},
		me: {"p-launcher", "p-launcher\x00menu", "10"},
	})
	report(errUsageProbe{})
	b, err := os.ReadFile(log)
	if err != nil || !strings.Contains(string(b), "critical") {
		t.Errorf("hotkey path lost its notification: %q (%v)", b, err)
	}
}

type errUsageProbe struct{}

func (errUsageProbe) Error() string { return "probe" }
