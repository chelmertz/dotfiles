package main

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

func TestRunCmdCapturesStdout(t *testing.T) {
	out, err := runCmd(context.Background(), "sh", "-c", "echo hi")
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(string(out)) != "hi" {
		t.Fatalf("got %q", out)
	}
}

func TestRunCmdReportsExitCodeAndStderr(t *testing.T) {
	_, err := runCmd(context.Background(), "sh", "-c", "echo boom >&2; exit 3")
	var ce *CmdError
	if !errors.As(err, &ce) {
		t.Fatalf("want CmdError, got %T %v", err, err)
	}
	if ce.ExitCode != 3 || !strings.Contains(ce.Stderr, "boom") || ce.Timeout {
		t.Fatalf("got %+v", ce)
	}
	if !strings.Contains(ce.Error(), "sh -c") || !strings.Contains(ce.Error(), "exit 3") || !strings.Contains(ce.Error(), "boom") {
		t.Fatalf("message lacks context: %s", ce.Error())
	}
}

func TestRunCmdReportsTimeout(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	_, err := runCmd(ctx, "sh", "-c", "sleep 5")
	var ce *CmdError
	if !errors.As(err, &ce) || !ce.Timeout {
		t.Fatalf("want timeout CmdError, got %v", err)
	}
	if !strings.Contains(ce.Error(), "timed out") {
		t.Fatalf("message: %s", ce.Error())
	}
}

func TestRunCmdMissingBinary(t *testing.T) {
	_, err := runCmd(context.Background(), "definitely-not-a-binary-xyz")
	var ce *CmdError
	if !errors.As(err, &ce) || ce.ExitCode != -1 {
		t.Fatalf("want CmdError exit -1, got %v", err)
	}
}
