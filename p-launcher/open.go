package main

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

type action int

const (
	actFocus action = iota
	actLaunch
)

func (a action) String() string { return [...]string{"focus", "launch"}[a] }

// decide picks focus (with target con) or launch for a tag given the live tree.
func decide(treeJSON []byte, tag string) (action, int64, error) {
	wins, err := FindTagged(treeJSON, tag)
	if err != nil {
		return 0, 0, err
	}
	if con, ok := PickFocus(wins); ok {
		return actFocus, con, nil
	}
	return actLaunch, 0, nil
}

// projectDir resolves path ("m/dependabot") to its directory under root and
// the Found record UpsertProjects needs to register it. Pure (no store, no
// i3) so it's cheap to test directly.
func projectDir(root, path string) (dir string, f Found, err error) {
	// Exactly <namespace>/<name>, no empty or dot segments: the path is the
	// project's identity in the DB, so junk must not get registered.
	seg := strings.Split(path, "/")
	if len(seg) != 2 || !cleanSegment(seg[0]) || !cleanSegment(seg[1]) {
		return "", Found{}, fmt.Errorf("project path must be <namespace>/<name>, got %q", path)
	}
	dir = filepath.Join(root, path)
	info, statErr := os.Stat(dir)
	if statErr != nil {
		return "", Found{}, fmt.Errorf("no project directory %s: %v", dir, statErr)
	}
	if !info.IsDir() {
		return "", Found{}, fmt.Errorf("no project directory %s: not a directory", dir)
	}
	return dir, Found{Namespace: seg[0], Name: seg[1], Path: path}, nil
}

func cleanSegment(s string) bool { return s != "" && s != "." && s != ".." }

// Open focuses the project's terminal if one exists, otherwise launches one,
// and records the activity. root is ~/p.
func Open(s *Store, root, path string) error {
	dir, f, err := projectDir(root, path)
	if err != nil {
		return err
	}
	// Registers the project before doing anything with side effects, so a
	// directory `list`/`menu` has never seen (never upserted into the
	// project table) still gets a working `open`, not a launched ghostty
	// followed by a RecordActivity failure below.
	if err := s.UpsertProjects([]Found{f}); err != nil {
		return err
	}
	// Opening an archived project is how it comes back; one more event.
	if _, err := reopenIfArchived(s, path); err != nil {
		return err
	}
	tag := tagFor(path)
	tree, err := getTree()
	if err != nil {
		return err
	}
	act, con, err := decide(tree, tag)
	if err != nil {
		return err
	}
	var exited chan error
	switch act {
	case actFocus:
		if err := focusCon(con); err != nil {
			return err
		}
	case actLaunch:
		cmd, err := launch(dir, tag, "")
		if err != nil {
			return err
		}
		// Waited on in the background: a launch may outlive p-launcher, and
		// we must not block the MRU record or the tagged-window poll on it.
		exited = make(chan error, 1)
		go func() { exited <- cmd.Wait() }()
	}
	// Record before waiting so a slow ghostty never loses the MRU update.
	if err := s.RecordActivity(path, act.String()); err != nil {
		return err
	}
	if act == actLaunch {
		return waitForTagged(tag, 3*time.Second, exited)
	}
	return nil
}

// launch starts a detached ghostty tagged with the project. Its stdio is
// inherited so ghostty's own output lands in the same journal stream. The
// caller is responsible for waiting on the returned *exec.Cmd.
func launch(dir, tag, prompt string) (*exec.Cmd, error) {
	argv, env := launchArgs(dir, tag, prompt)
	cmd := exec.Command(argv[0], argv[1:]...)
	cmd.Stdout = os.Stderr
	cmd.Stderr = os.Stderr
	cmd.Env = env
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("start ghostty for %s: %w", dir, err)
	}
	return cmd, nil
}

// launchArgs builds the ghostty command line and environment. A non-empty
// prompt becomes Claude's first message; it travels in an environment
// variable so no shell quoting of user-visible text is needed.
func launchArgs(dir, tag, prompt string) (argv, env []string) {
	shell := "claude; exec zsh"
	env = scrubEnv(os.Environ())
	if prompt != "" {
		shell = `claude "$P_LAUNCHER_PROMPT"; exec zsh`
		env = append(env, "P_LAUNCHER_PROMPT="+prompt)
	}
	argv = []string{"ghostty", "--x11-instance-name=" + tag, "--working-directory=" + dir, "-e", "zsh", "-ic", shell}
	return argv, env
}

// scrubEnv strips every env var whose key starts with "CLAUDE". Without
// this, a ghostty launched from inside a Claude Code session hands its
// nested `claude` invocation CLAUDE_CODE_CHILD_SESSION (among others);
// observed 2026-09-07, that nested claude reported "Transcript saving is
// off — inherited CLAUDE_CODE_CHILD_SESSION marker".
func scrubEnv(env []string) []string {
	out := make([]string, 0, len(env))
	for _, kv := range env {
		if strings.HasPrefix(kv, "CLAUDE") {
			continue
		}
		out = append(out, kv)
	}
	return out
}

// waitForTagged polls the tree until a window with tag appears. A launch
// that produces no tagged window is a fault that must be visible, not a
// silently untracked terminal — as is ghostty exiting before it does so.
func waitForTagged(tag string, timeout time.Duration, exited <-chan error) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		tree, err := getTree()
		if err != nil {
			return err
		}
		wins, err := FindTagged(tree, tag)
		if err != nil {
			return err
		}
		if len(wins) > 0 {
			// Found: ghostty keeps running: init reaps it after p-launcher exits.
			return nil
		}
		select {
		case exitErr := <-exited:
			return ghostExitedErr(exitErr, tag)
		default:
		}
		time.Sleep(100 * time.Millisecond)
	}
	// The deadline can fire in the same instant ghostty exits during the
	// last sleep; drain exited once more so that's reported by exit code,
	// not folded into the generic timeout below.
	select {
	case exitErr := <-exited:
		return ghostExitedErr(exitErr, tag)
	default:
	}
	return &hintError{
		msg:  fmt.Sprintf("ghostty started but no window tagged %q appeared within %s", tag, timeout),
		hint: "check `journalctl --user -t p-launcher` for ghostty output; see the single-instance fallback in the design doc",
	}
}

// ghostExitedErr renders the failure for a ghostty process that exited
// before its tagged window appeared. Isolated from the polling loop for
// testing.
func ghostExitedErr(exitErr error, tag string) error {
	return &hintError{
		msg:  fmt.Sprintf("ghostty exited %s before a window tagged %q appeared", exitDesc(exitErr), tag),
		hint: "its output is in journalctl --user -t p-launcher",
	}
}

// exitDesc renders a cmd.Wait() outcome: nil (successful wait) is exit 0.
func exitDesc(err error) string {
	if err == nil {
		return "0"
	}
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		return fmt.Sprintf("%d", ee.ExitCode())
	}
	return err.Error()
}
