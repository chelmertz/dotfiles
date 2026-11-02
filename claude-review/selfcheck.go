package main

import (
	"crypto/rand"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// selfChecker checks, in a scratch repo, the Claude Code behaviour every
// round rests on, which changed three times by 2.1.314: a background
// session is listed in the roster under its id, its job reaches done, `claude
// stop` ends its process, and a background resume of a stopped session
// without flags continues the same id under the options the first launch
// saved, instead of forking it. Tick runs it once per new
// `claude --version`, before any round.
type selfChecker struct {
	sess  Sessions
	run   func(dir string, argv []string) (string, error)
	trust func(dir string) error
	newID func() string
	dir   string
	sleep func(time.Duration)
}

// selfCheckPrompt keeps the check to one cheap turn with no tools.
const selfCheckPrompt = "Reply with one word. Use no tools."

// donePolls bounds the wait for a one-word turn: two minutes.
const donePolls = 480

// selfCheckFlags have the round policy's shape: dontAsk, no settings file,
// no MCP, then an allow and a deny list, which Claude Code must save.
var selfCheckFlags = []string{"--permission-mode", "dontAsk", "--setting-sources", "", "--strict-mcp-config",
	"--allowedTools", "Agent", "--disallowedTools", "WebFetch"}

func (c selfChecker) check() (err error) {
	if err := os.MkdirAll(c.dir, 0o700); err != nil {
		return err
	}
	if _, statErr := os.Stat(filepath.Join(c.dir, ".git")); statErr != nil {
		if err := c.cmd("git", "init", "-q"); err != nil {
			return err
		}
	}
	if err := c.trust(c.dir); err != nil {
		return fmt.Errorf("trusting the scratch repo: %w", err)
	}
	id := c.newID()
	defer func() { err = errors.Join(err, c.cleanup(id)) }()
	boot := append([]string{claudeCmd, "-p", flagSessionID, id, flagMaxTurns, "1"}, selfCheckFlags[:policyHead]...)
	if err := c.cmd(append(boot, selfCheckPrompt)...); err != nil {
		return fmt.Errorf("creating the session: %w", err)
	}
	// The same launch a round makes: the flags on the first background
	// launch, none after it, and only when the saved options match.
	first := append(append([]string{claudeCmd, flagBg}, selfCheckFlags[:policyHead]...), "--resume", id, selfCheckPrompt)
	first = append(first, selfCheckFlags[policyHead:]...)
	later := []string{claudeCmd, flagBg, "--resume", id, selfCheckPrompt}
	for _, what := range []string{"a first background round", "a resume after stop"} {
		argv, err := c.sess.LaunchArgv(id, selfCheckFlags, first, later)
		if err != nil {
			return fmt.Errorf("%s: %w", what, err)
		}
		res, err := c.sess.Relaunch(id, nil, func() error { return c.cmd(argv...) })
		if err != nil {
			return fmt.Errorf("%s: %w", what, err)
		}
		if res.Kind != relaunched {
			return fmt.Errorf("%s: %s", what, relaunchWords(res))
		}
		if err := c.waitDone(id); err != nil {
			return fmt.Errorf("%s: %w", what, err)
		}
	}
	return nil
}

// relaunchWords says what a Relaunch outcome other than relaunched means.
// Since 2.1.314 a flagged resume forks; the check pins that a flagless one
// does not.
func relaunchWords(res RelaunchResult) string {
	what := map[RelaunchKind]string{relaunchBusy: "the worker was not done", relaunchUnknown: "the roster could not be read",
		relaunchWindow: "another session holds the id", relaunchDuplicate: "it forked", relaunchNotListed: "the worker was not listed"}[res.Kind]
	return what + " (" + res.Detail + ")"
}

func (c selfChecker) waitDone(id string) error {
	for range donePolls {
		if c.sess.JobState(id) == jobDone {
			return nil
		}
		c.sleep(lifecyclePoll)
	}
	return fmt.Errorf("its job never reached done (last %q)", c.sess.JobState(id))
}

// cleanup stops every worker the check left for id, a fork included.
func (c selfChecker) cleanup(id string) error {
	all, err := c.sess.Roster()
	if err != nil {
		return err
	}
	var errs []error
	for _, w := range c.sess.workersFor(id, all) {
		errs = append(errs, c.sess.stopAndWait(w))
	}
	return errors.Join(errs...)
}

func (c selfChecker) cmd(argv ...string) error {
	if out, err := c.run(c.dir, viaInteractiveShell(shellLine(c.dir, argv))); err != nil {
		return fmt.Errorf("%s: %s", argv[0], lastLineOr(out, err.Error()))
	}
	return nil
}

// randomUUID is a version 4 UUID for the self-check's throwaway session.
func randomUUID() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}
