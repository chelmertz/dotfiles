package main

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

type fakeHost struct {
	d         Deps
	stdout    *bytes.Buffer
	stderr    *bytes.Buffer
	execs     [][]string // attached runs (inline claude)
	execDir   []string
	outputs   [][]string // captured runs (bootstrap, --bg)
	spawns    [][]string
	scripts   map[string]string
	execErr   error
	outErr    error
	outText   string
	spawnErr  error
	scriptErr error
	running   bool
	live      []int // successive PIDs SessionRunning reports after running; 0 once used up
	locked    bool  // another click holds the lock
	unlocked  bool
	sleeps    int
	events    []string // lock, running, spawn, unlock in call order
	ghCalls   [][]string
	captures  [][]string
	captured  []string // what each Capture returns, in order; "" once used up
}

const (
	dryRunFlag = "--dry-run"
	target     = "https://github.com/acme/widgets/pull/42"
	cloneOf    = "/srv/t/code/widgets"
	bgOut      = "Started background session\n  claude attach ff4f7a83    open in this terminal\n"
)

func newHost(t *testing.T, cfgJSON string, repoFound bool, exists bool) *fakeHost {
	t.Helper()
	h := &fakeHost{stdout: &bytes.Buffer{}, stderr: &bytes.Buffer{}, scripts: map[string]string{}}
	cfgDir := t.TempDir()
	if cfgJSON != "" {
		if err := os.MkdirAll(filepath.Join(cfgDir, "claude-review"), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(ConfigPath(cfgDir), []byte(cfgJSON), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	h.d = Deps{
		GOOS:          "linux",
		UserConfigDir: cfgDir,
		Home:          "/srv/t",
		ClaudeDir:     "/claude",
		Stdout:        h.stdout,
		Stderr:        h.stderr,
		FindRepo: func(roots []string, clonePath string, pr PR) (string, string, error) {
			if !repoFound {
				return "", "", errors.New("claude-review: repo not found: nope\n  git clone x")
			}
			return "/srv/t/code/" + pr.Repo, upstream, nil
		},
		SessionExists: func(string, string) bool { return exists },
		SessionRunning: func(string, string) Live {
			h.events = append(h.events, "running")
			if h.running {
				return Live{PID: 9, Background: true}
			}
			if len(h.live) == 0 {
				return Live{}
			}
			pid := h.live[0]
			h.live = h.live[1:]
			return Live{PID: pid}
		},
		Lock: func(string) (func(), error) {
			h.events = append(h.events, "lock")
			if h.locked {
				return nil, errLocked
			}
			return func() { h.unlocked = true; h.events = append(h.events, "unlock") }, nil
		},
		Sleep: func(time.Duration) { h.sleeps++ },
		Exec: func(dir string, argv []string) error {
			h.execs = append(h.execs, argv)
			h.execDir = append(h.execDir, dir)
			return h.execErr
		},
		Output: func(dir string, argv []string) (string, error) {
			h.outputs = append(h.outputs, argv)
			return h.outText, h.outErr
		},
		Gh: func(args ...string) (string, error) {
			h.ghCalls = append(h.ghCalls, args)
			if args[1] == paginate {
				return "[]", nil
			}
			return `{"html_url":"posted"}`, nil
		},
		Capture: func(dir string, argv []string) (string, error) {
			h.captures = append(h.captures, argv)
			if len(h.captured) == 0 {
				return "", nil
			}
			out := h.captured[0]
			h.captured = h.captured[1:]
			return out, nil
		},
		Spawn: func(dir string, argv []string) error {
			h.events = append(h.events, "spawn")
			h.spawns = append(h.spawns, argv)
			return h.spawnErr
		},
		WriteScript: func(name, content string) (string, error) {
			if h.scriptErr != nil {
				return "", h.scriptErr
			}
			h.scripts[name] = content
			return "/scratch/" + name, nil
		},
	}
	return h
}

func (h *fakeHost) cfg(t *testing.T) Config {
	t.Helper()
	cfg, err := LoadConfig(ConfigPath(h.d.UserConfigDir), "/srv/t")
	if err != nil {
		t.Fatal(err)
	}
	return cfg
}

func (h *fakeHost) script(t *testing.T) string {
	t.Helper()
	s, ok := h.scripts["claude-review-"+testPR.SessionID()+".sh"]
	if !ok {
		t.Fatalf("no launcher script written: %q", h.scripts)
	}
	return s
}

func bootstrapLine() []string { return viaInteractiveShell(shellLine(cloneOf, BootstrapArgs(testPR))) }

func sameArgv(t *testing.T, what string, got, want []string) {
	t.Helper()
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Errorf("%s:\n got %q\nwant %q", what, got, want)
	}
}

func TestRun_PostSubcommandRoutesToRunPost(t *testing.T) {
	h := newHost(t, "", true, false)
	code := run([]string{"post", target, kindComment, bodyFlag, "hi"}, h.d)
	if code != 0 || h.stdout.String() != "posted\n" || len(h.ghCalls) != 2 {
		t.Errorf("code %d out %q gh %q stderr %q", code, h.stdout.String(), h.ghCalls, h.stderr.String())
	}
	if code := run([]string{"post"}, h.d); code != 2 {
		t.Errorf("usage: code %d", code)
	}
}

func TestRun_ApproveSubcommandRoutesToRunApprove(t *testing.T) {
	h := newHost(t, "", true, false)
	if code := run([]string{"approve", target}, h.d); code != 2 || len(h.ghCalls) != 0 || !strings.Contains(h.stderr.String(), "--at SHA") {
		t.Errorf("code %d gh %q stderr %q", code, h.ghCalls, h.stderr.String())
	}
}

func TestRun_ThreadsSubcommandRoutesToRunThreads(t *testing.T) {
	h := newHost(t, "", true, false)
	if code := run([]string{"threads", target}, h.d); code != 0 || len(h.ghCalls) != 3 {
		t.Errorf("code %d gh %q stderr %q", code, h.ghCalls, h.stderr.String())
	}
}

func TestRun_Version(t *testing.T) {
	h := newHost(t, "", true, false)
	if code := run([]string{"--version"}, h.d); code != 0 || h.stdout.String() != "claude-review dev\n" {
		t.Errorf("code %d out %q", code, h.stdout.String())
	}
}

func TestRun_UsageErrors(t *testing.T) {
	for _, args := range [][]string{{}, {"a", "b"}, {bogusFlag}, {"-h"}} {
		h := newHost(t, "", true, false)
		if code := run(args, h.d); code != 2 {
			t.Errorf("%q: code %d", args, code)
		}
		if !strings.Contains(h.stderr.String(), "usage: claude-review") {
			t.Errorf("%q: no usage on stderr: %q", args, h.stderr.String())
		}
	}
}

func TestRun_BadConfigFails(t *testing.T) {
	h := newHost(t, `{"terminal": 7}`, true, false)
	if code := run([]string{target}, h.d); code != 1 || !strings.Contains(h.stderr.String(), `"terminal"`) {
		t.Errorf("code %d stderr %q", code, h.stderr.String())
	}
}

func TestRun_BadTargetFails(t *testing.T) {
	h := newHost(t, "", true, false)
	if code := run([]string{"what"}, h.d); code != 1 || !strings.Contains(h.stderr.String(), `"what"`) {
		t.Errorf("code %d stderr %q", code, h.stderr.String())
	}
}

func TestRun_RepoNotFound_NotifiesWhenNoTTY(t *testing.T) {
	h := newHost(t, "", false, false)
	if code := run([]string{target}, h.d); code != 1 {
		t.Fatalf("code %d", code)
	}
	if !strings.Contains(h.stderr.String(), "repo not found") {
		t.Errorf("stderr: %q", h.stderr.String())
	}
	if len(h.spawns) != 1 || strings.Join(h.spawns[0], "|") != "notify-send|claude-review|repo not found: nope\n  git clone x" {
		t.Errorf("notification: %q", h.spawns)
	}
}

func TestRun_RepoNotFound_NoNotificationOnTTY(t *testing.T) {
	h := newHost(t, "", false, false)
	h.d.IsTTY = true
	if code := run([]string{target}, h.d); code != 1 || len(h.spawns) != 0 {
		t.Errorf("code %d spawns %q", code, h.spawns)
	}
}

func TestRun_DryRun_FirstClickShowsBootstrapAndReview(t *testing.T) {
	h := newHost(t, "", true, false)
	if code := run([]string{dryRunFlag, target}, h.d); code != 0 {
		t.Fatalf("code %d stderr %q", code, h.stderr.String())
	}
	lines := strings.SplitN(h.stdout.String(), "\n", 2)
	if lines[0] != shellLine(cloneOf, BootstrapArgs(testPR)) {
		t.Errorf("first line: %q", lines[0])
	}
	if !strings.HasPrefix(lines[1], "cd '"+cloneOf+"' && 'claude' '--resume' '"+testPR.SessionID()+"'") {
		t.Errorf("second line: %q", lines[1])
	}
	if len(h.execs)+len(h.outputs)+len(h.spawns)+len(h.scripts) != 0 {
		t.Error("dry run must not execute anything")
	}
}

func TestRun_DryRun_LaterClickAndRunning(t *testing.T) {
	h := newHost(t, "", true, true)
	run([]string{dryRunFlag, target}, h.d)
	if out := h.stdout.String(); strings.Contains(out, "'-p'") || !strings.HasPrefix(out, "cd '"+cloneOf+"' && 'claude' '--resume'") || !strings.Contains(out, "New review round") {
		t.Errorf("later click: %q", out)
	}
	h = newHost(t, "", true, true)
	h.running = true
	run([]string{dryRunFlag, target}, h.d)
	if out := h.stdout.String(); out != shellLine(cloneOf, AttachArgs(testPR))+"\n" {
		t.Errorf("running: %q", out)
	}
}

func TestRun_FirstClick_OpensTerminalThatBootstrapsThenReviews(t *testing.T) {
	h := newHost(t, "", true, false)
	if code := run([]string{target}, h.d); code != 0 {
		t.Fatalf("code %d stderr %q", code, h.stderr.String())
	}
	if len(h.outputs) != 0 {
		t.Fatalf("the window opens before the bootstrap, not after it: %q", h.outputs)
	}
	want := launcherScript(cloneOf, bootstrapNotice, BootstrapArgs(testPR), ReviewArgs(h.cfg(t), testPR, upstream, false, false))
	if h.script(t) != want {
		t.Errorf("script:\n%s\nwant\n%s", h.script(t), want)
	}
	if !strings.Contains(h.script(t), "'\\''-p'\\'' '\\''--session-id'\\''") {
		t.Errorf("script lacks the bootstrap:\n%s", h.script(t))
	}
	sameArgv(t, "terminal", h.spawns[0], []string{ghostty, "--working-directory=" + cloneOf, "-e", "/scratch/claude-review-" + testPR.SessionID() + ".sh"})
	if len(h.execs) != 0 {
		t.Error("nothing should run attached")
	}
}

func TestRun_Detached_FirstClick_BootstrapFailureIsReported(t *testing.T) {
	h := newHost(t, detachedCfg, true, false)
	h.outErr = errors.New("exit status 1")
	h.outText = "banner\nWorkspace not trusted. Run `claude` in /x once and accept the trust prompt, then retry.\n"
	if code := run([]string{target}, h.d); code != 1 {
		t.Fatalf("code %d", code)
	}
	if len(h.outputs) != 1 {
		t.Errorf("the background session must not start after a failed bootstrap: %q", h.outputs)
	}
	if !strings.Contains(h.stderr.String(), "creating session for acme/widgets#42: Workspace not trusted. Run `claude` in /x once") {
		t.Errorf("stderr: %q", h.stderr.String())
	}
	if len(h.spawns) != 1 || !strings.Contains(h.spawns[0][2], "Workspace not trusted") || len(h.scripts) != 0 {
		t.Errorf("should only notify: spawns %q scripts %q", h.spawns, h.scripts)
	}
}

func TestRun_LaterClick_NoBootstrap(t *testing.T) {
	h := newHost(t, "", true, true)
	if code := run([]string{target}, h.d); code != 0 {
		t.Fatalf("code %d stderr %q", code, h.stderr.String())
	}
	if len(h.outputs) != 0 {
		t.Errorf("no bootstrap on a later click: %q", h.outputs)
	}
	if s := h.script(t); !strings.Contains(s, "'--resume'") || !strings.Contains(s, "New review round") {
		t.Errorf("script should resume with the re-review prompt:\n%s", s)
	}
}

func TestRun_LaterClick_RunningSession_Attaches(t *testing.T) {
	h := newHost(t, "", true, true)
	h.running = true
	if code := run([]string{target}, h.d); code != 0 {
		t.Fatalf("code %d stderr %q", code, h.stderr.String())
	}
	if want := launcherScript(cloneOf, AttachArgs(testPR)); h.script(t) != want {
		t.Errorf("script:\n%s", h.script(t))
	}
	if !strings.Contains(h.stderr.String(), "still running; attaching instead of resuming") {
		t.Errorf("stderr: %q", h.stderr.String())
	}
}

func TestRun_Terminal_Darwin_CustomTemplate(t *testing.T) {
	h := newHost(t, `{"terminal": "open -a iTerm {script}"}`, true, true)
	h.d.GOOS = "darwin"
	if code := run([]string{target}, h.d); code != 0 {
		t.Fatalf("code %d stderr %q", code, h.stderr.String())
	}
	sameArgv(t, "spawn", h.spawns[0], []string{"open", "-a", "iTerm", "/scratch/claude-review-" + testPR.SessionID() + ".sh"})
}

func TestRun_Terminal_DarwinDefault(t *testing.T) {
	h := newHost(t, "", true, true)
	h.d.GOOS = "darwin"
	run([]string{target}, h.d)
	if h.spawns[0][0] != "open" || h.spawns[0][2] != "Terminal" {
		t.Errorf("spawn: %q", h.spawns)
	}
}

func TestRun_Terminal_Failures(t *testing.T) {
	h := newHost(t, "", true, true)
	h.scriptErr = errors.New("disk full")
	if code := run([]string{target}, h.d); code != 1 || !strings.Contains(h.stderr.String(), "writing launcher: disk full") {
		t.Errorf("script error: code %d stderr %q", code, h.stderr.String())
	}

	h = newHost(t, `{"terminal": "open -a \"Term"}`, true, true)
	if code := run([]string{target}, h.d); code != 1 || !strings.Contains(h.stderr.String(), "unbalanced quote") {
		t.Errorf("template error: code %d stderr %q", code, h.stderr.String())
	}

	h = newHost(t, "", true, true)
	h.spawnErr = errors.New("no such file")
	if code := run([]string{target}, h.d); code != 1 || !strings.Contains(h.stderr.String(), `opening terminal with "ghostty": no such file`) {
		t.Errorf("spawn error: code %d stderr %q", code, h.stderr.String())
	}
}

func TestRun_TTY_RunsClaudeInline(t *testing.T) {
	h := newHost(t, "", true, true)
	h.d.IsTTY = true
	if code := run([]string{target}, h.d); code != 0 {
		t.Fatalf("code %d stderr %q", code, h.stderr.String())
	}
	if len(h.execs) != 1 || h.execDir[0] != cloneOf {
		t.Fatalf("exec: %q in %q", h.execs, h.execDir)
	}
	sameArgv(t, "inline", h.execs[0], ReviewArgs(h.cfg(t), testPR, upstream, true, false))
	if len(h.spawns)+len(h.scripts)+len(h.outputs) != 0 {
		t.Error("inline run must not spawn, write scripts or bootstrap")
	}
}

func TestRun_TTY_FirstClickBootstrapsThenRunsInline(t *testing.T) {
	h := newHost(t, "", true, false)
	h.d.IsTTY = true
	if code := run([]string{target}, h.d); code != 0 || len(h.outputs) != 1 || len(h.execs) != 1 {
		t.Errorf("code %d outputs %d execs %d", code, len(h.outputs), len(h.execs))
	}
}

func TestRun_TTY_ClaudeExitError(t *testing.T) {
	h := newHost(t, "", true, true)
	h.d.IsTTY = true
	h.execErr = errors.New("exit status 1")
	if code := run([]string{target}, h.d); code != 1 || !strings.Contains(h.stderr.String(), "claude exited: exit status 1") {
		t.Errorf("code %d stderr %q", code, h.stderr.String())
	}
}

func TestRun_Detached_FirstClick(t *testing.T) {
	h := newHost(t, `{"terminal": false, "post_without_asking": true}`, true, false)
	h.outText = bgOut
	if code := run([]string{target}, h.d); code != 0 {
		t.Fatalf("code %d stderr %q", code, h.stderr.String())
	}
	if len(h.outputs) != 2 {
		t.Fatalf("bootstrap then --bg, got %q", h.outputs)
	}
	sameArgv(t, "bootstrap", h.outputs[0], bootstrapLine())
	sameArgv(t, "bg", h.outputs[1], viaInteractiveShell(shellLine(cloneOf, ReviewArgs(h.cfg(t), testPR, upstream, false, true))))
	if !strings.Contains(h.outputs[1][2], "--bg") || !strings.Contains(h.outputs[1][2], "Bash(claude-review post https://github.com/acme/widgets/pull/42 *)") {
		t.Errorf("bg line: %s", h.outputs[1][2])
	}
	id := testPR.SessionID()
	if !strings.Contains(h.stderr.String(), "running in the background as "+id+"; `claude attach "+id+"`") || !strings.Contains(h.stderr.String(), "Started background session") {
		t.Errorf("stderr: %q", h.stderr.String())
	}
	if len(h.spawns)+len(h.scripts)+len(h.execs) != 0 {
		t.Error("detached must not open a terminal or run attached")
	}
}

func TestRun_Detached_LaterClick(t *testing.T) {
	h := newHost(t, detachedCfg, true, true)
	h.outText = bgOut
	if code := run([]string{target}, h.d); code != 0 || len(h.outputs) != 1 {
		t.Fatalf("code %d outputs %q stderr %q", code, h.outputs, h.stderr.String())
	}
	sameArgv(t, "bg resume", h.outputs[0], viaInteractiveShell(shellLine(cloneOf, ReviewArgs(h.cfg(t), testPR, upstream, true, true))))
}

func TestRun_Detached_StillRunning_DoesNotForkACopy(t *testing.T) {
	h := newHost(t, detachedCfg, true, true)
	h.running = true
	if code := run([]string{target}, h.d); code != 1 {
		t.Fatalf("code %d", code)
	}
	if len(h.outputs)+len(h.execs) != 0 {
		t.Errorf("must not start anything: %q %q", h.outputs, h.execs)
	}
	id := testPR.SessionID()
	if !strings.Contains(h.stderr.String(), "still running as "+id+"; `claude attach "+id+"`") {
		t.Errorf("stderr: %q", h.stderr.String())
	}
	if len(h.spawns) != 1 || !strings.Contains(h.spawns[0][2], "still running") {
		t.Errorf("notification: %q", h.spawns)
	}
}

func TestRun_Detached_Failure(t *testing.T) {
	h := newHost(t, detachedCfg, true, true)
	h.outErr = errors.New("exit status 1")
	h.outText = "some banner\nWorkspace not trusted. Run `claude` in /x once and accept the trust prompt, then retry.\n"
	if code := run([]string{target}, h.d); code != 1 || !strings.Contains(h.stderr.String(), "starting background session: Workspace not trusted. Run `claude` in /x once") {
		t.Errorf("code %d stderr %q", code, h.stderr.String())
	}
	if len(h.spawns) != 1 || h.spawns[0][0] != "notify-send" || !strings.Contains(h.spawns[0][2], "Workspace not trusted") {
		t.Errorf("should notify without a tty, with claude's last line: %q", h.spawns)
	}
}

func TestRun_Detached_FailureWithoutOutputShowsTheError(t *testing.T) {
	h := newHost(t, detachedCfg, true, true)
	h.outErr = errors.New("exit status 127")
	if code := run([]string{target}, h.d); code != 1 || !strings.Contains(h.stderr.String(), "starting background session: exit status 127") {
		t.Errorf("code %d stderr %q", code, h.stderr.String())
	}
}

// A click has no terminal, so the notification is the only place the user
// reads the error: it carries every line, such as the clone command.
func TestFail_NotificationCarriesEveryLine(t *testing.T) {
	h := newHost(t, "", true, false)
	fail(h.d, errors.New("claude-review: line one\nline two"))
	if strings.Join(h.spawns[0], "|") != "notify-send|claude-review|line one\nline two" {
		t.Errorf("%q", h.spawns)
	}
	if h.stderr.String() != "claude-review: line one\nline two\n" {
		t.Errorf("stderr should carry everything: %q", h.stderr.String())
	}
}

func TestLastLineOr(t *testing.T) {
	if got := lastLineOr("a\nb\n\n", "f"); got != "b" {
		t.Errorf("got %q", got)
	}
	if got := lastLineOr("  \n", "f"); got != "f" {
		t.Errorf("got %q", got)
	}
}

var widgetsNotFound = &notFoundError{pr: widgetsPR, target: "/srv/t/code/widgets"}

func TestFail_NotFound_MiddleClickCopiesTheCommand(t *testing.T) {
	h := newHost(t, "", true, false)
	h.captured = []string{"default\n"}
	fail(h.d, widgetsNotFound)
	if len(h.captures) != 2 {
		t.Fatalf("captures: %q", h.captures)
	}
	if n := strings.Join(h.captures[0], "|"); !strings.HasPrefix(n, "notify-send|--action=default=Copy clone command|claude-review|") {
		t.Errorf("notification: %q", n)
	}
	if c := h.captures[1]; c[0] != "sh" || !strings.Contains(c[2], "xclip -selection clipboard") || c[len(c)-1] != widgetsNotFound.Command() {
		t.Errorf("copy: %q", c)
	}
}

func TestFail_NotFound_DismissedCopiesNothing(t *testing.T) {
	h := newHost(t, "", true, false)
	fail(h.d, widgetsNotFound)
	if len(h.captures) != 1 || len(h.spawns) != 0 {
		t.Errorf("captures %q spawns %q", h.captures, h.spawns)
	}
}

func TestFail_NotFound_MacOSPutsTheCommandInTheBody(t *testing.T) {
	h := newHost(t, "", true, false)
	h.d.GOOS = darwin
	fail(h.d, widgetsNotFound)
	if len(h.captures) != 0 || len(h.spawns) != 1 || !strings.Contains(strings.Join(h.spawns[0], " "), "gh repo clone") {
		t.Errorf("captures %q spawns %q", h.captures, h.spawns)
	}
}

// A review already open in a terminal window is in no roster and cannot be
// attached to: every mode must start nothing and say where it is.
func TestRun_OpenInATerminal_StartsNothing(t *testing.T) {
	for _, mode := range []struct {
		name, cfg string
		tty       bool
	}{{"window", "", false}, {"tty", "", true}, {"detached", detachedCfg, false}} {
		for _, exists := range []bool{true, false} { // false: another click's bootstrap is mid-turn
			h := newHost(t, mode.cfg, true, exists)
			h.d.IsTTY = mode.tty
			h.live = []int{4242}
			if code := run([]string{target}, h.d); code != 1 {
				t.Errorf("%s exists=%v: code %d", mode.name, exists, code)
			}
			if len(h.scripts)+len(h.execs)+len(h.outputs) != 0 {
				t.Errorf("%s exists=%v: started something: scripts %q execs %q outputs %q", mode.name, exists, h.scripts, h.execs, h.outputs)
			}
			if !strings.Contains(h.stderr.String(), "acme/widgets#42 is already open (pid 4242)") {
				t.Errorf("%s exists=%v: stderr %q", mode.name, exists, h.stderr.String())
			}
			if notified := len(h.spawns) == 1 && h.spawns[0][0] == "notify-send"; notified == mode.tty {
				t.Errorf("%s exists=%v: notification only without a tty: %q", mode.name, exists, h.spawns)
			}
		}
	}
}

func TestRun_DryRun_OpenInATerminalSaysSo(t *testing.T) {
	h := newHost(t, "", true, true)
	h.live = []int{4242}
	if code := run([]string{dryRunFlag, target}, h.d); code != 1 || h.stdout.Len() != 0 || !strings.Contains(h.stderr.String(), "already open") {
		t.Errorf("code %d stdout %q stderr %q", code, h.stdout.String(), h.stderr.String())
	}
}

func TestRun_AnotherClickHoldsTheLock_StartsNothing(t *testing.T) {
	for _, cfg := range []string{"", detachedCfg} {
		h := newHost(t, cfg, true, false)
		h.locked = true
		if code := run([]string{target}, h.d); code != 1 {
			t.Errorf("%q: code %d", cfg, code)
		}
		if len(h.scripts)+len(h.execs)+len(h.outputs) != 0 {
			t.Errorf("%q: started something: %q %q %q", cfg, h.scripts, h.execs, h.outputs)
		}
		if !strings.Contains(h.stderr.String(), "another click is already opening acme/widgets#42") {
			t.Errorf("%q: stderr %q", cfg, h.stderr.String())
		}
		if len(h.events) == 0 || h.events[0] != "lock" {
			t.Errorf("%q: the lock comes before reading the session's state: %q", cfg, h.events)
		}
	}
}

func TestRun_LockErrorIsReported(t *testing.T) {
	h := newHost(t, "", true, true)
	h.d.Lock = func(string) (func(), error) { return nil, errors.New("read-only file system") }
	if code := run([]string{target}, h.d); code != 1 || len(h.scripts) != 0 || !strings.Contains(h.stderr.String(), "locking acme/widgets#42: read-only file system") {
		t.Errorf("code %d scripts %q stderr %q", code, h.scripts, h.stderr.String())
	}
}

func TestRun_EveryModeHoldsTheLockForTheWholeClick(t *testing.T) {
	for _, cfg := range []string{"", detachedCfg} {
		h := newHost(t, cfg, true, true)
		h.d.IsTTY = cfg == ""
		h.d.Exec = func(string, []string) error {
			h.events = append(h.events, "exec")
			return nil
		}
		h.d.Output = func(string, []string) (string, error) {
			h.events = append(h.events, "output")
			return "", nil
		}
		if code := run([]string{target}, h.d); code != 0 {
			t.Fatalf("%q: code %d stderr %q", cfg, code, h.stderr.String())
		}
		if got := strings.Join(h.events, ","); !strings.HasPrefix(got, "lock,running,") || !strings.HasSuffix(got, ",unlock") || strings.Count(got, "exec")+strings.Count(got, "output") != 1 {
			t.Errorf("%q: events %s", cfg, got)
		}
	}
}

// The window's claude starts after the click would return; the lock is
// held until it is live, or a click arriving meanwhile opens a second one.
// On a first click the bootstrap's `claude -p` holds the id first and exits.
func TestRun_Window_HoldsTheLockUntilTheWindowsSessionIsLive(t *testing.T) {
	h := newHost(t, "", true, false)
	h.live = []int{0, 0, 0, 500, 500, 0, 0, 600, 600}
	if code := run([]string{target}, h.d); code != 0 {
		t.Fatalf("code %d stderr %q", code, h.stderr.String())
	}
	if got := strings.Join(h.events, ","); got != "lock,running,spawn,running,running,running,running,running,running,running,unlock" {
		t.Errorf("events %s", got)
	}
	if h.sleeps != 6 || len(h.live) != 1 {
		t.Errorf("should stop at the first pid after the bootstrap's: sleeps %d left %v", h.sleeps, h.live)
	}

	h = newHost(t, "", true, true)
	h.live = []int{0, 0, 700, 800}
	run([]string{target}, h.d)
	if h.sleeps != 1 || len(h.live) != 1 || !h.unlocked {
		t.Errorf("no bootstrap: the first live pid is the window's: sleeps %d left %v", h.sleeps, h.live)
	}
}

func TestRun_Window_GivesUpWaitingSilently(t *testing.T) {
	h := newHost(t, "", true, false)
	h.live = []int{0, 500} // only the bootstrap ever shows up
	if code := run([]string{target}, h.d); code != 0 || h.sleeps != awaitPolls || !h.unlocked {
		t.Errorf("code %d sleeps %d unlocked %v stderr %q", code, h.sleeps, h.unlocked, h.stderr.String())
	}
}

func TestRun_Window_AttachDoesNotWait(t *testing.T) {
	h := newHost(t, "", true, true)
	h.running = true
	run([]string{target}, h.d)
	if h.sleeps != 0 {
		t.Errorf("an attach window adds no session to wait for: sleeps %d", h.sleeps)
	}
}
