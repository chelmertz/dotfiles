package main

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

// fakeBin writes an executable shell script and returns its path.
func fakeBin(t *testing.T, body string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "bin")
	if err := os.WriteFile(p, []byte("#!/bin/sh\n"+body+"\n"), 0o700); err != nil { //nolint:gosec // a test script
		t.Fatal(err)
	}
	return p
}

// within runs fn and fails the test if it has not returned after d.
func within(t *testing.T, d time.Duration, fn func() error) error {
	t.Helper()
	done := make(chan error, 1)
	go func() { done <- fn() }()
	select {
	case err := <-done:
		return err
	case <-time.After(d):
		t.Fatalf("still running after %s", d)
		return nil
	}
}

// A hung gh read (a locked keyring's prompt, a stalled network) is bounded
// and reads as GitHub not answering, so the PR is read next pass.
func TestAHungGhReadTimesOut(t *testing.T) {
	gh := ghRunWith(fakeBin(t, "sleep 30"), 300*time.Millisecond)
	err := within(t, 5*time.Second, func() error { _, err := gh("api", "user"); return err })
	if !errors.Is(err, errGhTimeout) || errors.Is(err, errGhUnknown) {
		t.Errorf("a hung read: %v, want errGhTimeout and not errGhUnknown", err)
	}
}

// A write that ran out of time may have landed: its outcome is unknown,
// never "GitHub did not answer".
func TestAHungGhWriteIsUnknown(t *testing.T) {
	gh := ghRunWith(fakeBin(t, "sleep 30"), 300*time.Millisecond)
	for _, args := range [][]string{
		{"api", "-X", "POST", "repos/acme/widgets/pulls/1/reviews", "-f", "event=APPROVE"},
		{"api", "--method", "PUT", "repos/acme/widgets/pulls/1/reviews/2/dismissals"},
		{"api", "graphql", "-f", "query=" + resolveThreadMutation, "-f", "id=x"},
	} {
		err := within(t, 5*time.Second, func() error { _, err := gh(args...); return err })
		if !errors.Is(err, errGhUnknown) {
			t.Errorf("%q: %v, want errGhUnknown", args, err)
		}
	}
	if ghWrites([]string{"api", "-X", "GET", "repos/acme/widgets/pulls/1"}) || ghWrites([]string{"api", "graphql", "-f", "query=query { viewer { login } }"}) {
		t.Error("a read counted as a write")
	}
}

// A launch runs through an interactive shell; killing only the shell left
// a child holding the output pipe, so the wait never ended. The whole
// process group is killed and the wait bounded.
func TestAHungCommandIsKilledWithItsChildren(t *testing.T) {
	pidFile := filepath.Join(t.TempDir(), "child.pid")
	// sleep by absolute path: execOutput runs with the host's environment
	// only, and a nix build sandbox has no /usr/bin or /run.
	sleep, err := exec.LookPath("sleep")
	if err != nil {
		t.Fatal(err)
	}
	bin := fakeBin(t, sleep+" 30 & echo $! > "+pidFile+"\nwait")
	h := osHost{timeout: 300 * time.Millisecond}
	err = within(t, 5*time.Second, func() error { _, err := h.execOutput("", []string{bin}); return err })
	if !errors.Is(err, errTimedOut) {
		t.Fatalf("a hung command: %v, want errTimedOut", err)
	}
	b, err := os.ReadFile(pidFile) //nolint:gosec // the test's own file
	if err != nil {
		t.Fatal(err)
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(b)))
	if err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(2 * time.Second)
	for syscall.Kill(pid, 0) == nil {
		if time.Now().After(deadline) {
			t.Fatalf("the child %d outlived the timeout", pid)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// A launch that timed out may have left its worker running: it is unknown,
// which holds the PR, never "nothing started", which a retry would launch
// beside.
func TestALaunchThatTimedOutIsUnknown(t *testing.T) {
	fs := newFakeSessions()
	res, err := fs.sessions().Relaunch(widgetsPR.SessionID(), nil, func() error {
		return errors.Join(errors.New("starting the round"), errTimedOut)
	})
	if err != nil || res.Kind != relaunchUnknown {
		t.Errorf("a timed-out launch: %+v, %v; want relaunchUnknown", res, err)
	}
}

// A pass in which every gh call timed out failed at everything it tried;
// it fails the unit, so ReviewTickFailing fires instead of a quiet journal.
func TestAPassWhereEveryGhCallTimedOutFails(t *testing.T) {
	c := &ghCounter{gh: func(...string) (string, error) { return "", errGhTimeout }}
	if c.allTimedOut() {
		t.Error("no call yet counted as all timed out")
	}
	_, _ = c.run("api", "user")
	_, _ = c.run("api", "repos/acme/widgets/pulls/1")
	if !c.allTimedOut() {
		t.Error("two timed-out calls did not count as all timed out")
	}
	c.gh = func(...string) (string, error) { return "{}", nil }
	_, _ = c.run("api", "repos/acme/widgets/pulls/2")
	if c.allTimedOut() {
		t.Error("one answered call still counted as all timed out")
	}
}

// A trailing -X with no method after it is not a write, and does not panic.
func TestGhWritesWithATrailingMethodFlag(t *testing.T) {
	if ghWrites([]string{"api", "-X"}) || ghWrites([]string{"api", "--method"}) {
		t.Error("a method flag with no method read as a write")
	}
}

// A child that left the process group and holds the output pipe does not
// hold the run past WaitDelay: the group kill cannot reach it.
func TestABoundedCommandDoesNotWaitOnAnEscapedChild(t *testing.T) {
	if _, err := exec.LookPath("setsid"); err != nil {
		t.Skip("no setsid")
	}
	cmd, cancel, _ := boundedCommand(200*time.Millisecond, "sh", "-c", "setsid sleep 8 & sleep 8")
	defer cancel()
	var out strings.Builder
	cmd.Stdout = &out
	start := time.Now()
	err := within(t, 15*time.Second, cmd.Run)
	if err == nil {
		t.Error("the bounded run succeeded")
	}
	if d := time.Since(start); d > 5*time.Second {
		t.Errorf("returned after %s, want within the 2 s WaitDelay", d)
	}
}
