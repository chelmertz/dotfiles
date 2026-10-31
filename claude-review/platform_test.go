package main

import (
	"os/exec"
	"strings"
	"testing"
)

func TestShellQuote_RoundTripsThroughSh(t *testing.T) {
	for _, in := range []string{"plain", "has space", "it's", `"dq"`, "a\nb", "`tick` $HOME \\back", "''"} {
		out, err := exec.Command(sh, "-c", "printf %s "+shellQuote(in)).Output()
		if err != nil || string(out) != in {
			t.Errorf("%q -> %q (%v)", in, out, err)
		}
	}
}

func TestShellLine(t *testing.T) {
	got := shellLine("/x/y z", []string{"claude", "a'b"})
	if got != `cd '/x/y z' && 'claude' 'a'\''b'` {
		t.Errorf("got %s", got)
	}
}

func TestViaInteractiveShell(t *testing.T) {
	got := viaInteractiveShell("cd /x && claude")
	want := []string{sh, "-c", `exec "${SHELL:-/bin/sh}" -ic 'cd /x && claude'`}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Errorf("got %q", got)
	}
}

// The launcher script is executed by a real sh with SHELL pointed at a stub
// that records its arguments, so the quoting is proven rather than eyeballed.
func TestLauncherScript_RunsClaudeInDirThenKeepsShell(t *testing.T) {
	dir := t.TempDir()
	stub := dir + "/stubshell"
	// The stub shell: for "-ic <line>" run the line with real sh; for "-i"
	// (the trailing exec) print a marker and exit.
	if err := writeFile(stub, "#!/bin/sh\nif [ \"$1\" = -ic ]; then exec /bin/sh -c \"$2\"; fi\necho SHELL-KEPT; exit 0\n"); err != nil {
		t.Fatal(err)
	}
	claude := dir + "/claude"
	if err := writeFile(claude, "#!/bin/sh\necho \"CLAUDE in $(pwd) args=$#:$*\"\n"); err != nil {
		t.Fatal(err)
	}
	script := dir + "/launch.sh"
	if err := writeFile(script, launcherScript(dir, []string{claude, "--resume", "id", "line one\nline 'two'"})); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(script)
	cmd.Env = []string{"SHELL=" + stub, minimalPath}
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	s := string(out)
	if !strings.Contains(s, "CLAUDE in "+dir+" args=3:--resume id line one\nline 'two'") {
		t.Errorf("claude not run as expected:\n%s", s)
	}
	if !strings.HasSuffix(strings.TrimSpace(s), "SHELL-KEPT") {
		t.Errorf("interactive shell not exec'd last:\n%s", s)
	}
}

// A failing step stops the chain, so a failed bootstrap never resumes a
// session that does not exist, and the window still drops to a shell with
// the error on screen.
func TestLauncherScript_FailedStepSkipsTheRestAndKeepsShell(t *testing.T) {
	dir := t.TempDir()
	stub := dir + "/stubshell"
	if err := writeFile(stub, "#!/bin/sh\nif [ \"$1\" = -ic ]; then exec /bin/sh -c \"$2\"; fi\necho SHELL-KEPT; exit 0\n"); err != nil {
		t.Fatal(err)
	}
	failing, after := dir+"/failing", dir+"/after"
	if err := writeFile(failing, "#!/bin/sh\necho 'Workspace not trusted'; exit 1\n"); err != nil {
		t.Fatal(err)
	}
	if err := writeFile(after, "#!/bin/sh\necho AFTER-RAN\n"); err != nil {
		t.Fatal(err)
	}
	script := dir + "/launch.sh"
	if err := writeFile(script, launcherScript(dir, []string{"echo", "first"}, []string{failing}, []string{after})); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(script)
	cmd.Env = []string{"SHELL=" + stub, minimalPath}
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	s := string(out)
	if !strings.Contains(s, "first\nWorkspace not trusted\n") || strings.Contains(s, "AFTER-RAN") || !strings.HasSuffix(strings.TrimSpace(s), "SHELL-KEPT") {
		t.Errorf("want first, the error, no later step, then the shell:\n%s", s)
	}
}

// A URL handler may start without SHELL. The window must still end in a
// shell rather than on `exec "" -i`, which fails and closes it.
func TestLauncherScript_KeepsAShellWithoutSHELL(t *testing.T) {
	dir := t.TempDir()
	script := dir + "/launch.sh"
	if err := writeFile(script, launcherScript(dir, []string{"echo", "ran"})); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(script)
	cmd.Env = []string{minimalPath}
	cmd.Stdin = strings.NewReader("echo SHELL-KEPT\n")
	out, err := cmd.CombinedOutput()
	if s := string(out); err != nil || !strings.Contains(s, "ran\n") || !strings.Contains(s, "SHELL-KEPT") {
		t.Errorf("err %v, want the step then a working shell:\n%s", err, s)
	}
}

func TestDefaultTerminalTemplate(t *testing.T) {
	if got := defaultTerminalTemplate("darwin"); got != "open -a Terminal {script}" {
		t.Errorf("darwin: %q", got)
	}
	if got := defaultTerminalTemplate("linux"); got != "ghostty --working-directory={dir} -e {script}" {
		t.Errorf("linux: %q", got)
	}
}

func TestTerminalArgv_ExpandsPlaceholdersInsideWords(t *testing.T) {
	got, err := terminalArgv(`ghostty --working-directory={dir} -e {script}`, "/my dir", "/scratch/s.sh")
	if err != nil {
		t.Fatal(err)
	}
	want := []string{ghostty, "--working-directory=/my dir", "-e", "/scratch/s.sh"}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Errorf("got %q", got)
	}
}

func TestTerminalArgv_Errors(t *testing.T) {
	if _, err := terminalArgv("   ", "/d", "/s"); err == nil || !strings.Contains(err.Error(), "empty") {
		t.Errorf("empty: %v", err)
	}
	if _, err := terminalArgv(`open -a "Terminal`, "/d", "/s"); err == nil || !strings.Contains(err.Error(), "unbalanced") {
		t.Errorf("unbalanced: %v", err)
	}
}

func TestSplitWords(t *testing.T) {
	cases := map[string][]string{
		`a b  c`:           {"a", "b", "c"},
		`a 'b c' d`:        {"a", "b c", "d"},
		`a "b c" d`:        {"a", "b c", "d"},
		`a "b \" c"`:       {"a", `b " c`},
		`a 'b \ c'`:        {"a", `b \ c`},
		`a\ b c`:           {"a b", "c"},
		`x=""`:             {"x="},
		"\tlead\ntrail \n": {"lead", "trail"},
		`''`:               {""},
		`trailing\`:        {`trailing\`},
	}
	for in, want := range cases {
		got, err := splitWords(in)
		if err != nil {
			t.Errorf("%q: %v", in, err)
			continue
		}
		if strings.Join(got, "|") != strings.Join(want, "|") || len(got) != len(want) {
			t.Errorf("%q: got %q want %q", in, got, want)
		}
	}
}

func TestSplitWords_TrailingBackslashInsideQuotesIsUnbalanced(t *testing.T) {
	for _, in := range []string{`"ab\`, `'ab\`, `"ab\\`} {
		if _, err := splitWords(in); err == nil {
			t.Errorf("%q: expected unbalanced-quote error", in)
		}
	}
	// A backslash before the closing double quote escapes it, so the quote
	// stays open: also unbalanced, and must not panic.
	if _, err := splitWords(`"a\"`); err == nil {
		t.Error(`"a\" should be unbalanced`)
	}
}

func TestNotifyArgv(t *testing.T) {
	got := notifyArgv("linux", "claude-review", "repo not found")
	if strings.Join(got, "|") != "notify-send|claude-review|repo not found" {
		t.Errorf("linux: %q", got)
	}
	got = notifyArgv("darwin", `t"1`, `say "hi" \ there`)
	want := []string{"osascript", "-e", `display notification "say \"hi\" \\ there" with title "t\"1"`}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Errorf("darwin: %q", got)
	}
}
