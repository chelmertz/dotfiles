package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"time"
)

// CmdError describes a failed external process with enough context to act on
// from a journal line alone.
type CmdError struct {
	Cmd      string // `sh "-c" "echo"`
	ExitCode int    // -1 when the process could not be started, was killed, or its pipes were abandoned after WaitDelay
	Stderr   string // trimmed, at most 2000 bytes
	Timeout  bool
}

func (e *CmdError) Error() string {
	if e.Timeout {
		return fmt.Sprintf("%s: timed out; stderr: %q", e.Cmd, e.Stderr)
	}
	return fmt.Sprintf("%s: exit %d; stderr: %q", e.Cmd, e.ExitCode, e.Stderr)
}

// runCmd runs name with args, returning stdout. Stderr is captured into the
// error. Callers pass a ctx with a timeout for anything that must not hang.
func runCmd(ctx context.Context, name string, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	// exec.CommandContext's SIGKILL on ctx cancellation only reaches the
	// direct child; without a WaitDelay, Run blocks until every grandchild
	// (e.g. a process a shell spawned) closes stdout/stderr on its own.
	cmd.WaitDelay = time.Second
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	if err == nil {
		return stdout.Bytes(), nil
	}
	parts := make([]string, 0, 1+len(args))
	parts = append(parts, name)
	for _, a := range args {
		parts = append(parts, fmt.Sprintf("%q", a))
	}
	ce := &CmdError{
		Cmd:      strings.Join(parts, " "),
		ExitCode: -1,
		Stderr:   clip(stderr.String()),
	}
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		ce.ExitCode = ee.ExitCode()
	} else if ce.Stderr == "" {
		// The process never ran (binary missing, not executable, ...): the
		// only trace is the start error itself.
		ce.Stderr = err.Error()
	}
	// A process that exits with code N exactly as the deadline fires is
	// reported as exit N, not as a timeout; only an unattributed exit
	// (killed, or exec.ErrWaitDelay after pipes were abandoned) counts.
	ce.Timeout = ce.ExitCode == -1 && errors.Is(ctx.Err(), context.DeadlineExceeded)
	return stdout.Bytes(), ce
}

func clip(s string) string {
	s = strings.TrimSpace(s)
	if len(s) > 2000 {
		return strings.ToValidUTF8(s[:2000], "") + "…"
	}
	return s
}
