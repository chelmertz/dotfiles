package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

// main is exercised for real with exit captured.
func TestMain_PassesExitCode(t *testing.T) {
	oldArgs, oldExit := os.Args, exit
	defer func() { os.Args, exit = oldArgs, oldExit }()
	var code = -1
	exit = func(c int) { code = c }
	os.Args = []string{"claude-review"} // usage error
	main()
	if code != 2 {
		t.Errorf("exit code %d, want 2", code)
	}
}

func TestRealDeps_WiresTheHost(t *testing.T) {
	d := realDeps()
	if d.GOOS == "" || d.Home == "" || d.UserConfigDir == "" || d.ClaudeDir == "" || d.StateDir == "" || len(d.Environ) == 0 || d.Stdout == nil || d.Stderr == nil {
		t.Errorf("%+v", d)
	}
	// Every func field must be wired: a nil one is a panic on the first
	// real click, and no fake-host test can see it.
	v := reflect.ValueOf(d)
	for i := 0; i < v.NumField(); i++ {
		if f := v.Field(i); f.Kind() == reflect.Func && f.IsNil() {
			t.Errorf("realDeps leaves %s nil", v.Type().Field(i).Name)
		}
	}
}

func TestResolveDirs(t *testing.T) {
	env := func(kv map[string]string) func(string) string {
		return func(k string) string { return kv[k] }
	}
	set := resolveDirs("/h", env(map[string]string{
		"CLAUDE_CONFIG_DIR": "/elsewhere/.claude", "XDG_STATE_HOME": "/state", "XDG_RUNTIME_DIR": "/run/user/1",
	}))
	if set != (hostDirs{claude: "/elsewhere/.claude", claudeJSON: "/elsewhere/.claude/.claude.json", state: "/state", runtime: "/run/user/1"}) {
		t.Errorf("set: %+v", set)
	}
	// An empty variable means the default, as an unset one does.
	for _, kv := range []map[string]string{nil, {"CLAUDE_CONFIG_DIR": "", "XDG_STATE_HOME": "", "XDG_RUNTIME_DIR": ""}} {
		if got := resolveDirs("/h", env(kv)); got != (hostDirs{claude: "/h/.claude", claudeJSON: "/h/.claude.json", state: "/h/.local/state"}) {
			t.Errorf("defaults from %v: %+v", kv, got)
		}
	}
}

func TestOSHelpers(t *testing.T) {
	var h osHost
	dir := t.TempDir()
	if err := h.execAttached(dir, []string{sh, "-c", "test \"$(pwd)\" = " + shellQuote(dir)}); err != nil {
		t.Errorf("execAttached did not run in dir: %v", err)
	}
	if err := h.execAttached(dir, []string{sh, "-c", "exit 3"}); err == nil {
		t.Error("execAttached must report the exit status")
	}
	if out, err := h.execOutput(dir, []string{sh, "-c", "echo hi; echo err >&2; exit 4"}); err == nil || out != "hi\nerr\n" {
		t.Errorf("execOutput: %q %v", out, err)
	}
	if out, err := h.execStdout(dir, []string{sh, "-c", "pwd; echo warn >&2"}); err != nil || out != dir+"\n" {
		t.Errorf("execStdout: %q %v", out, err)
	}
	if out, err := h.execStdout(dir, []string{sh, "-c", "echo hi; echo why >&2; exit 4"}); err == nil || out != "hi\n" || !strings.HasSuffix(err.Error(), ": why") {
		t.Errorf("execStdout failure: %q %v", out, err)
	}
	if err := h.spawnDetached(dir, []string{sh, "-c", "true"}); err != nil {
		t.Errorf("spawnDetached: %v", err)
	}
	if err := h.spawnDetached(dir, []string{"/definitely/not/here"}); err == nil {
		t.Error("spawnDetached must fail on a missing binary")
	}
	cache := t.TempDir()
	p, err := osHost{cacheBase: cache}.writeLauncherScript("claude-review-test.sh", "#!/bin/sh\nexit 0\n")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(p, filepath.Join(cache, "claude-review")+"/") {
		t.Errorf("script not under the user cache dir: %s", p)
	}
	if fi, err := os.Stat(p); err != nil || fi.Mode().Perm()&0o100 == 0 {
		t.Errorf("script not executable: %v %v", fi, err)
	}
	if fi, _ := os.Stat(filepath.Dir(p)); fi.Mode().Perm() != 0o700 {
		t.Errorf("launcher dir must be private, got %v", fi.Mode().Perm())
	}
	blocker := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(blocker, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	// a file where the dir must go
	if _, err := (osHost{cacheBase: blocker}).writeLauncherScript("x.sh", ""); err == nil {
		t.Error("writeLauncherScript must fail when the cache dir cannot be created")
	}
	occupied := t.TempDir()
	if err := os.MkdirAll(filepath.Join(occupied, "claude-review", "x.sh"), 0o700); err != nil { // a dir where the file must go
		t.Fatal(err)
	}
	if _, err := (osHost{cacheBase: occupied}).writeLauncherScript("x.sh", ""); err == nil {
		t.Error("writeLauncherScript must fail when the file cannot be written")
	}
	for _, base := range []string{"", "relative"} {
		if _, err := (osHost{cacheBase: base}).writeLauncherScript("x.sh", ""); err == nil {
			t.Errorf("writeLauncherScript must fail when no cache dir can be determined (%q)", base)
		}
	}
	_ = stdinIsTerminal() // whatever go test's stdin is; must not panic
	if _, err := exec.LookPath("gh"); err != nil {
		t.Skip("gh not installed:", err)
	}
	if out, err := ghRun("--version"); err != nil || !strings.HasPrefix(out, "gh version") {
		t.Errorf("ghRun --version: out %q err %v", out, err)
	}
	if out, err := ghRun("definitely-not-a-command"); err == nil || !strings.Contains(err.Error(), "unknown command") || out != "" {
		t.Errorf("ghRun should put gh's stderr in the error only: out %q err %v", out, err)
	}
}

func TestIsTerminal(t *testing.T) {
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	defer w.Close()
	if isTerminal(r) {
		t.Error("a pipe is not a terminal")
	}
	devnull, err := os.Open(os.DevNull)
	if err != nil {
		t.Fatal(err)
	}
	defer devnull.Close()
	if isTerminal(devnull) {
		t.Error("/dev/null is a char device but not a terminal; a URL handler gets it as stdin")
	}
	pty, err := os.Open("/dev/ptmx")
	if err != nil {
		t.Skip("no /dev/ptmx:", err)
	}
	defer pty.Close()
	if !isTerminal(pty) {
		t.Error("a pty is a terminal")
	}
	closed, _ := os.Open(os.DevNull)
	_ = closed.Close()
	if isTerminal(closed) {
		t.Error("a closed file must report false, not panic")
	}
}

func TestCleanEnv(t *testing.T) {
	got := cleanEnv([]string{"PATH=/bin", "CLAUDE_CODE_CHILD_SESSION=1", "CLAUDE_CONFIG_DIR=/c", "CLAUDECODE=1", "BROKEN"})
	if strings.Join(got, ",") != "PATH=/bin,CLAUDE_CONFIG_DIR=/c,BROKEN" {
		t.Errorf("got %q", got)
	}
}

func TestChildProcessesDoNotInheritSessionMarkers(t *testing.T) {
	h := osHost{environ: []string{"CLAUDE_CODE_CHILD_SESSION=1", "CLAUDE_CONFIG_DIR=/kept"}}
	out, err := h.execOutput(t.TempDir(), []string{sh, "-c", "echo \"[$CLAUDE_CODE_CHILD_SESSION][$CLAUDE_CONFIG_DIR]\""})
	if err != nil || strings.TrimSpace(out) != "[][/kept]" {
		t.Errorf("out %q err %v", out, err)
	}
	dir := t.TempDir()
	if err := h.execAttached(dir, []string{sh, "-c", "test -z \"$CLAUDE_CODE_CHILD_SESSION\""}); err != nil {
		t.Errorf("execAttached leaked the marker: %v", err)
	}
	marker := dir + "/spawned"
	if err := h.spawnDetached(dir, []string{sh, "-c", "test -z \"$CLAUDE_CODE_CHILD_SESSION\" && : > " + marker}); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 50; i++ {
		if _, err := os.Stat(marker); err == nil {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Error("spawnDetached leaked the marker (or never ran)")
}
