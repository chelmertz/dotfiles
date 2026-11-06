package main

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"

	"golang.org/x/term"
)

func goos() string { return runtime.GOOS }

func stdinIsTerminal() bool { return isTerminal(os.Stdin) }

// isTerminal is a real isatty. A mode check on ModeCharDevice is not enough:
// /dev/null is a character device too, and it is what a desktop launcher
// hands a URL handler as stdin.
func isTerminal(f *os.File) bool {
	// A closed file's Fd() is ^uintptr(0), which converts to -1: not a
	// terminal, no panic, so no range guard is needed.
	return term.IsTerminal(int(f.Fd())) //nolint:gosec // see above
}

// sessionMarkers are variables a running Claude Code session sets for its own
// children. Inherited by a review session they make it think it is a child
// (transcript saving turns off), so they are stripped. Run from a browser
// click none are set; run from inside a Claude session they all are.
var sessionMarkers = map[string]bool{
	"CLAUDECODE": true, "CLAUDE_PID": true,
	"CLAUDE_CODE_CHILD_SESSION": true, "CLAUDE_CODE_SESSION_ID": true,
	"CLAUDE_CODE_SESSION_ATTENDED": true, "CLAUDE_CODE_ENTRYPOINT": true,
	"CLAUDE_CODE_EXECPATH":         true,
	"CLAUDE_CODE_MESSAGING_SOCKET": true, "CLAUDE_CODE_MESSAGING_TOKEN": true,
}

// cleanEnv is environ without sessionMarkers.
func cleanEnv(environ []string) []string {
	out := make([]string, 0, len(environ))
	for _, kv := range environ {
		k, _, _ := strings.Cut(kv, "=")
		if !sessionMarkers[k] {
			out = append(out, kv)
		}
	}
	return out
}

// osHost runs children with the environment realDeps read, and keeps its
// files under the user cache directory realDeps resolved.
type osHost struct {
	environ   []string
	cacheBase string
}

func (h osHost) execAttached(dir string, argv []string) error {
	cmd := exec.Command(argv[0], argv[1:]...) //nolint:gosec // argv is built by this program
	cmd.Dir = dir
	cmd.Env = cleanEnv(h.environ)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	return cmd.Run()
}

func (h osHost) execOutput(dir string, argv []string) (string, error) {
	cmd := exec.Command(argv[0], argv[1:]...) //nolint:gosec // argv is built by this program
	cmd.Dir = dir
	cmd.Env = cleanEnv(h.environ)
	out, err := cmd.CombinedOutput()
	return string(out), err
}

// execStdout is execOutput with stdout alone: a warning claude writes to
// stderr must not pass for its reply, so stderr goes into the error only.
func (h osHost) execStdout(dir string, argv []string) (string, error) {
	cmd := exec.Command(argv[0], argv[1:]...) //nolint:gosec // argv is built by this program
	cmd.Dir = dir
	cmd.Env = cleanEnv(h.environ)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		return string(out), fmt.Errorf("%w: %s", err, strings.TrimSpace(stderr.String()))
	}
	return string(out), nil
}

func (h osHost) spawnDetached(dir string, argv []string) error {
	cmd := exec.Command(argv[0], argv[1:]...) //nolint:gosec // argv is built by this program
	cmd.Dir = dir
	cmd.Env = cleanEnv(h.environ)
	return cmd.Start()
}

// writeLauncherScript writes the launcher under the user's cache directory
// (not the shared temp dir, where another local user could pre-create the
// predictable name), with a fixed name per session so repeated clicks
// overwrite instead of littering.
func (h osHost) writeLauncherScript(name, content string) (string, error) {
	dir, err := cacheDir(h.cacheBase)
	if err != nil {
		return "", err
	}
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte(content), 0o700); err != nil { //nolint:gosec // must be executable
		return "", err
	}
	return p, nil
}

// cacheDir is this tool's directory under base, the user's cache
// directory, created private on first use. An empty or relative base (no
// cache directory could be determined) is refused, as os.UserCacheDir does.
func cacheDir(base string) (string, error) {
	if !filepath.IsAbs(base) {
		return "", fmt.Errorf("no usable cache directory: %q", base)
	}
	dir := filepath.Join(base, "claude-review")
	return dir, os.MkdirAll(dir, 0o700)
}

// errLocked is Lock's answer when another click holds the PR's lock.
var errLocked = errors.New("locked by another click")

// lockUnderCache is lockSession in cacheDir.
func (h osHost) lockUnderCache(name string) (func(), error) {
	dir, err := cacheDir(h.cacheBase)
	if err != nil {
		return nil, err
	}
	return lockSession(dir, name)
}

// lockSession takes an exclusive flock on dir/<name>.lock without waiting. The
// kernel drops it when the process exits, however it exits, so a crashed
// click never leaves a PR locked. Go opens files close-on-exec, so the
// terminal and claude started under the lock do not inherit it.
func lockSession(dir, name string) (func(), error) {
	f, err := os.OpenFile(filepath.Join(dir, name+".lock"), os.O_CREATE|os.O_RDWR, 0o600) //nolint:gosec // name is a session id or a fixed word, never input
	if err != nil {
		return nil, err
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil { //nolint:gosec // an open file's fd fits an int
		_ = f.Close()
		if errors.Is(err, syscall.EWOULDBLOCK) {
			err = errLocked
		}
		return nil, err
	}
	return func() { _ = f.Close() }, nil
}

// ghRun runs gh and returns its stdout. Stderr (a warning on success, the
// reason on failure) must not leak into values such as a commit sha, so it
// is attached to the error only.
func ghRun(args ...string) (string, error) {
	if err := graphqlVarsMismatch(args); err != nil {
		return "", err
	}
	cmd := exec.Command("gh", args...) //nolint:gosec // args are built by this program
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		return string(out), fmt.Errorf("%w: %s", err, strings.TrimSpace(stderr.String()))
	}
	return string(out), nil
}
