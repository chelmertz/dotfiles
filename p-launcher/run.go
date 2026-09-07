package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"
)

// CmdError describes a failed external process with enough context to act on
// from a journal line alone.
type CmdError struct {
	Cmd      string // "sh -c echo"
	ExitCode int    // -1 when the process could not be started or was killed
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
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	if err == nil {
		return stdout.Bytes(), nil
	}
	ce := &CmdError{
		Cmd:      name + " " + strings.Join(args, " "),
		ExitCode: -1,
		Stderr:   clip(stderr.String()),
		Timeout:  errors.Is(ctx.Err(), context.DeadlineExceeded),
	}
	var ee *exec.ExitError
	if errors.As(err, &ee) && !ce.Timeout {
		ce.ExitCode = ee.ExitCode()
	}
	return stdout.Bytes(), ce
}

func clip(s string) string {
	s = strings.TrimSpace(s)
	if len(s) > 2000 {
		return s[:2000] + "…"
	}
	return s
}
