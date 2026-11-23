package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"time"

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

// githubTokenVars are the GitHub tokens a shell may hold, elly's GITHUB_PAT
// among them. Named one by one: a *_TOKEN pattern would also strip the
// Claude CLI's own CLAUDE_CODE_OAUTH_TOKEN.
var githubTokenVars = map[string]bool{
	"GH_TOKEN": true, "GITHUB_TOKEN": true, "GH_ENTERPRISE_TOKEN": true,
	"GITHUB_ENTERPRISE_TOKEN": true, "GITHUB_PAT": true,
}

// cleanEnv is environ without sessionMarkers or GitHub tokens: what a
// launched session gets. claude-review's own gh calls keep the full one.
func cleanEnv(environ []string) []string {
	out := make([]string, 0, len(environ))
	for _, kv := range environ {
		k, _, _ := strings.Cut(kv, "=")
		if !sessionMarkers[k] && !githubTokenVars[k] {
			out = append(out, kv)
		}
	}
	return out
}

// osHost runs children with the environment realDeps read, and keeps its
// files under the user cache directory realDeps resolved, its locks beside
// the ledger.
type osHost struct {
	environ   []string
	cacheBase string
	// stateDir is the XDG state home; the per-PR locks live under it.
	stateDir string
	// timeout bounds execOutput and execStdout; zero is execTimeout.
	timeout time.Duration
}

// execTimeout bounds a command this program waits on: a launch, a stop, a
// version or self-check turn. The tick unit's own cap is 10 minutes.
const execTimeout = 5 * time.Minute

// errTimedOut: a command outlived its bound and was killed with its
// process group. What it did before is unknown.
var errTimedOut = errors.New("timed out")

// boundedCommand is exec.Command in its own process group, killed whole
// after d: killing only an interactive shell leaves its children holding
// the output pipe, and the wait for them never ends. WaitDelay bounds that
// wait too. timedOut names a timeout in the error a run returned.
func boundedCommand(d time.Duration, name string, args ...string) (cmd *exec.Cmd, cancel func(), timedOut func(error) error) {
	ctx, cancel := context.WithTimeout(context.Background(), d)
	cmd = exec.CommandContext(ctx, name, args...) //nolint:gosec // argv is built by this program
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error { return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL) }
	cmd.WaitDelay = 2 * time.Second
	return cmd, cancel, func(err error) error {
		if err != nil && errors.Is(ctx.Err(), context.DeadlineExceeded) {
			return fmt.Errorf("%w after %s: %w", errTimedOut, d, err)
		}
		return err
	}
}

func (h osHost) bound() time.Duration {
	if h.timeout > 0 {
		return h.timeout
	}
	return execTimeout
}

func (h osHost) execAttached(dir string, argv []string) error {
	cmd := exec.Command(argv[0], argv[1:]...) //nolint:gosec // argv is built by this program
	cmd.Dir = dir
	cmd.Env = cleanEnv(h.environ)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	return cmd.Run()
}

func (h osHost) execOutput(dir string, argv []string) (string, error) {
	cmd, cancel, timedOut := boundedCommand(h.bound(), argv[0], argv[1:]...)
	defer cancel()
	cmd.Dir = dir
	cmd.Env = cleanEnv(h.environ)
	out, err := cmd.CombinedOutput()
	return string(out), timedOut(err)
}

// execStdout is execOutput with stdout alone: a warning claude writes to
// stderr must not pass for its reply, so stderr goes into the error only.
func (h osHost) execStdout(dir string, argv []string) (string, error) {
	cmd, cancel, timedOut := boundedCommand(h.bound(), argv[0], argv[1:]...)
	defer cancel()
	cmd.Dir = dir
	cmd.Env = cleanEnv(h.environ)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err = timedOut(err); err != nil {
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

// lockDir holds the locks, beside the ledger rather than in the cache
// directory, so a tick and a click agree on it whatever cache directory
// each one's environment names. Created private on first use.
func lockDir(stateDir string) (string, error) {
	if !filepath.IsAbs(stateDir) {
		return "", fmt.Errorf("no usable state directory: %q", stateDir)
	}
	dir := filepath.Join(filepath.Dir(LedgerPath(stateDir)), "locks")
	return dir, os.MkdirAll(dir, 0o700)
}

// lock takes name's lock in the cache directory, where the build before
// this one takes it, then in lockDir, and releases both. Taking both for
// one release keeps a click from the previous build and a tick from this
// one apart; the cache lock goes in the release after.
func (h osHost) lock(name string) (func(), error) {
	old, err := cacheDir(h.cacheBase)
	if err != nil {
		return nil, err
	}
	dir, err := lockDir(h.stateDir)
	if err != nil {
		return nil, err
	}
	unlockOld, err := lockSession(old, name)
	if err != nil {
		return nil, err
	}
	unlockNew, err := lockSession(dir, name)
	if err != nil {
		unlockOld()
		return nil, err
	}
	return func() { unlockNew(); unlockOld() }, nil
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

// ghTimeout bounds one gh call; the slowest seen, RoundFacts' two GraphQL
// queries, takes seconds.
const ghTimeout = 60 * time.Second

var (
	// errGhTimeout: a gh read got no answer in time; the caller reads again
	// next pass, as when GitHub is unreachable.
	errGhTimeout = errors.New("GitHub did not answer in time")
	// errGhUnknown: a gh write ran out of time and may have landed. Nothing
	// sends it again before reading GitHub back.
	errGhUnknown = errors.New("a GitHub write timed out; whether it landed is unknown")
)

// ghRun runs gh and returns its stdout. Stderr (a warning on success, the
// reason on failure) must not leak into values such as a commit sha, so it
// is attached to the error only.
var ghRun = ghRunWith("gh", ghTimeout)

func ghRunWith(bin string, timeout time.Duration) Gh {
	return func(args ...string) (string, error) {
		if err := graphqlVarsMismatch(args); err != nil {
			return "", err
		}
		cmd, cancel, timedOut := boundedCommand(timeout, bin, args...)
		defer cancel()
		var stderr bytes.Buffer
		cmd.Stderr = &stderr
		out, err := cmd.Output()
		if err == nil {
			return string(out), nil
		}
		if errors.Is(timedOut(err), errTimedOut) {
			class := errGhTimeout
			if ghWrites(args) {
				class = errGhUnknown
			}
			return string(out), fmt.Errorf("gh %s: %w", strings.Join(args[:min(2, len(args))], " "), class)
		}
		return string(out), fmt.Errorf("%w: %s", err, strings.TrimSpace(stderr.String()))
	}
}

// ghWrites: args change something on GitHub, a REST method other than GET
// or a GraphQL mutation.
func ghWrites(args []string) bool {
	for i, a := range args {
		if (a == "-X" || a == "--method") && i+1 < len(args) && !strings.EqualFold(args[i+1], "GET") {
			return true
		}
		if q, ok := strings.CutPrefix(a, "query="); ok && strings.HasPrefix(strings.TrimSpace(q), "mutation") {
			return true
		}
	}
	return false
}

// ghCounter wraps a Gh and counts its calls and timeouts, so a pass where
// every call timed out can fail the unit.
type ghCounter struct {
	gh              Gh
	calls, timeouts int
}

func (c *ghCounter) run(args ...string) (string, error) {
	out, err := c.gh(args...)
	c.calls++
	if errors.Is(err, errGhTimeout) || errors.Is(err, errGhUnknown) {
		c.timeouts++
	}
	return out, err
}

// allTimedOut: at least one call, and none answered.
func (c *ghCounter) allTimedOut() bool { return c.calls > 0 && c.timeouts == c.calls }
