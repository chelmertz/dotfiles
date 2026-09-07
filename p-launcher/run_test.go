package main

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
	"unicode/utf8"
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
	if !strings.Contains(ce.Error(), `sh "-c"`) || !strings.Contains(ce.Error(), "exit 3") || !strings.Contains(ce.Error(), "boom") {
		t.Fatalf("message lacks context: %s", ce.Error())
	}
}

func TestRunCmdReportsTimeout(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	start := time.Now()
	_, err := runCmd(ctx, "sh", "-c", "sleep 5")
	if elapsed := time.Since(start); elapsed >= 3*time.Second {
		t.Fatalf("runCmd took %s, want under 3s (grandchild outlived the killed shell)", elapsed)
	}
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
	if !strings.Contains(ce.Error(), "executable file not found") {
		t.Fatalf("start error must be surfaced, got %q", ce.Error())
	}
}

func TestClipRuneBoundary(t *testing.T) {
	// "€" is 3 bytes/rune; 2000 is not a multiple of 3, so a naive byte-offset
	// cut at 2000 lands mid-rune and would produce invalid UTF-8.
	s := strings.Repeat("€", 700)
	got := clip(s)
	if !utf8.ValidString(got) {
		t.Fatalf("clip produced invalid UTF-8: %q", got)
	}
	if !strings.HasSuffix(got, "…") {
		t.Fatalf("want clipped result to end with ellipsis, got %q", got[len(got)-10:])
	}
}
