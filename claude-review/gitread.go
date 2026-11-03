package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"strings"
	"time"
)

// readCommands are the git reads a session gets instead of git itself. A
// permission pattern cannot exclude a flag, and `git show`, `git diff` and
// `git log` take --output, which writes any file, while `git grep -O` runs
// a command. Here the program builds argv: the session names the PR (pinned
// by its rule) and at most one plain operand, which never reaches git as a
// flag. The head is always the PR's head on GitHub.
var readCommands = map[string]func([]string, prReader, io.Writer, io.Writer) int{
	"show": runShow,
	"diff": runDiff,
	"grep": runGrep,
}

// prReader is what the read commands need: the PR's head on GitHub, the
// git runner, the clone's remotes and the ledger's diff base.
type prReader struct {
	head     func(pr PR) (string, error)
	git      func(args ...string) (string, error)
	remotes  func() ([]Remote, error)
	diffBase func(pr PR) (string, bool, error)
}

func newPRReader(d Deps) prReader {
	return prReader{
		head: func(pr PR) (string, error) {
			return headSHA(d.Gh, fmt.Sprintf("repos/%s/%s", pr.Owner, pr.Repo), pr.Number)
		},
		git: func(args ...string) (string, error) { return gitRead(d.Environ, args...) },
		remotes: func() ([]Remote, error) {
			// No remotes exits 1 with no output, which checkout reports.
			out, _ := gitRead(d.Environ, "config", "--get-regexp", `^remote\..*\.url$`)
			return parseRemotes(out), nil
		},
		diffBase: func(pr PR) (string, bool, error) {
			l, err := OpenLedger(LedgerPath(d.StateDir))
			if err != nil {
				return "", false, err
			}
			defer func() { _ = l.Close() }()
			return l.DiffBase(pr)
		},
	}
}

const (
	gitTimeout = time.Minute
	// gitOutputCap bounds what one read puts into a session; Claude Code
	// saves a large output to a file the session can page through.
	gitOutputCap = 4 << 20
)

// gitRead runs git in the working directory with the user's global and
// system config ignored, so settings such as log.showSignature or
// grep.patternType=perl cannot change what runs on the PR's content.
func gitRead(environ []string, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), gitTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "git", append([]string{"--no-pager"}, args...)...) //nolint:gosec // argv built by the read commands, no shell
	cmd.Env = append(append([]string(nil), environ...), "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_NOSYSTEM=1", "GIT_PAGER=cat", "GIT_TERMINAL_PROMPT=0")
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &capped{w: &out, left: gitOutputCap}, &errb
	err := cmd.Run()
	if err != nil {
		return out.String(), fmt.Errorf("git %s: %w: %s", args[0], err, strings.TrimSpace(errb.String()))
	}
	return out.String(), nil
}

// capped writes until left runs out and then drops the rest, noting it once.
type capped struct {
	w    *bytes.Buffer
	left int
	cut  bool
}

func (c *capped) Write(p []byte) (int, error) {
	if c.left <= 0 {
		if !c.cut {
			c.w.WriteString("\n[output cut]\n")
			c.cut = true
		}
		return len(p), nil
	}
	n := min(len(p), c.left)
	c.w.Write(p[:n])
	c.left -= n
	return len(p), nil
}

// checkout resolves the PR's head and checks that this clone is the PR's
// repository and holds that commit. It returns the head and the remote to
// fetch from.
func (r prReader) checkout(pr PR) (string, error) {
	remotes, err := r.remotes()
	if err != nil {
		return "", err
	}
	remote := ""
	for _, rm := range remotes {
		if strings.EqualFold(slugOf(rm.URL), pr.Owner+"/"+pr.Repo) {
			remote = rm.Name
			break
		}
	}
	if remote == "" {
		return "", fmt.Errorf("this directory is not a clone of %s/%s", pr.Owner, pr.Repo)
	}
	head, err := r.head(pr)
	if err != nil {
		return "", err
	}
	if _, err := r.git("cat-file", "-e", head+"^{commit}"); err != nil {
		return "", fmt.Errorf("the PR head %s is not in this clone; run `git fetch %s pull/%d/head` first", head, remote, pr.Number)
	}
	return head, nil
}

// readArgs parses `<pr> [operand]`, with exactly n operands and no flags.
func readArgs(args []string, n int, usage string, stderr io.Writer) (PR, string, bool) {
	if len(args) != 1+n {
		fmt.Fprintln(stderr, usage)
		return PR{}, "", false
	}
	pr, err := ParseTarget(args[0])
	if err != nil {
		fmt.Fprintln(stderr, usage)
		return PR{}, "", false
	}
	if n == 0 {
		return pr, "", true
	}
	return pr, args[1], true
}

func readFail(name string, err error, stderr io.Writer) int {
	fmt.Fprintf(stderr, "claude-review %s: %v\n", name, err)
	return 1
}

// runShow is `claude-review show <pr> <path>`: the file at the PR head.
// cat-file is plumbing, so no textconv or attribute from the PR applies.
func runShow(args []string, r prReader, stdout, stderr io.Writer) int {
	pr, path, ok := readArgs(args, 1, "usage: claude-review show <pr> <path>", stderr)
	if !ok {
		return 2
	}
	head, err := r.checkout(pr)
	if err != nil {
		return readFail("show", err, stderr)
	}
	out, err := r.git("cat-file", "blob", head+":"+strings.TrimPrefix(path, "./"))
	if err != nil {
		return readFail("show", errors.New("no such file at the PR head"), stderr)
	}
	fmt.Fprintf(stderr, "head %s\n", head)
	fmt.Fprint(stdout, out)
	return 0
}

// runDiff is `claude-review diff <pr>`: the commits and the diff since the
// last round tick read on the PR, or since the head the user accepted. The
// base comes from the ledger, never from the session, so it cannot name a
// stash or an unpushed branch in this clone.
func runDiff(args []string, r prReader, stdout, stderr io.Writer) int {
	pr, _, ok := readArgs(args, 0, "usage: claude-review diff <pr>", stderr)
	if !ok {
		return 2
	}
	base, found, err := r.diffBase(pr)
	if err != nil {
		return readFail("diff", err, stderr)
	}
	if !found || !fullSHA.MatchString(base) {
		return readFail("diff", fmt.Errorf("no earlier round on this PR; use `gh pr diff %d`", pr.Number), stderr)
	}
	head, err := r.checkout(pr)
	if err != nil {
		return readFail("diff", err, stderr)
	}
	if _, err := r.git("merge-base", "--is-ancestor", base, head); err != nil {
		return readFail("diff", fmt.Errorf("the PR was rewritten since %s, the head of the last round; use `gh pr diff %d`", base, pr.Number), stderr)
	}
	log, err := r.git("log", "--no-color", "--no-show-signature", "--format=%H %s", base+".."+head)
	if err != nil {
		return readFail("diff", err, stderr)
	}
	diff, err := r.git("diff", "--no-color", "--no-ext-diff", "--no-textconv", base, head)
	if err != nil {
		return readFail("diff", err, stderr)
	}
	fmt.Fprintf(stdout, "head %s, since %s\n\n%s\n%s", head, base, log, diff)
	return 0
}

// runGrep is `claude-review grep <pr> <text>`: fixed-string matches at the
// PR head. -e keeps text an operand even when it starts with "-".
func runGrep(args []string, r prReader, stdout, stderr io.Writer) int {
	pr, text, ok := readArgs(args, 1, "usage: claude-review grep <pr> <text>", stderr)
	if !ok {
		return 2
	}
	head, err := r.checkout(pr)
	if err != nil {
		return readFail("grep", err, stderr)
	}
	out, err := r.git("grep", "--no-color", "-n", "-I", "-F", "-e", text, head, "--")
	var exit *exec.ExitError
	if errors.As(err, &exit) && exit.ExitCode() == 1 {
		out, err = "", nil // no match
	}
	if err != nil {
		return readFail("grep", err, stderr)
	}
	fmt.Fprintf(stdout, "head %s\n%s", head, out)
	return 0
}
