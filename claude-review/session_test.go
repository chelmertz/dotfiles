package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const sid = "d2cc10f2-04e0-52a1-950a-85c38f166e8c"

func TestSessionExists(t *testing.T) {
	dir := t.TempDir()
	if SessionExists(dir, sid) {
		t.Fatal("empty dir should have no session")
	}
	proj := filepath.Join(dir, "projects", "-home-x-code-widgets")
	if err := os.MkdirAll(proj, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(proj, sid+".jsonl"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if !SessionExists(dir, sid) {
		t.Fatal("should find the transcript in any project folder")
	}
	if SessionExists(dir, "other") {
		t.Fatal("other id must not match")
	}
}

func writeRoster(t *testing.T, dir, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(dir, "daemon"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "daemon", "roster.json"), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestSessionRunning(t *testing.T) {
	dir := t.TempDir()
	if SessionRunning(dir, sid).PID != 0 {
		t.Fatal("no roster: not running")
	}
	writeRoster(t, dir, "{not json")
	if SessionRunning(dir, sid).PID != 0 {
		t.Fatal("bad roster: not running")
	}

	old := processAlive
	defer func() { processAlive = old }()
	alive := map[int]bool{100: true, 200: false, 0: true} // 0 "alive": kill(0,0) hits the whole process group, so the guard matters
	processAlive = func(pid int) bool { return alive[pid] }

	writeRoster(t, dir, fmt.Sprintf(`{"proto":1,"workers":{
	  "aa": {"sessionId": %q, "pid": 200},
	  "bb": {"sessionId": "other", "pid": 100}}}`, sid))
	if SessionRunning(dir, sid).PID != 0 {
		t.Fatal("dead pid: not running")
	}
	writeRoster(t, dir, fmt.Sprintf(`{"workers":{"aa": {"sessionId": %q, "pid": 100}}}`, sid))
	if got := SessionRunning(dir, sid); got != (Live{PID: 100, Background: true}) {
		t.Fatalf("live worker for this id: running in the background, got %+v", got)
	}
	writeRoster(t, dir, fmt.Sprintf(`{"workers":{"aa": {"sessionId": %q, "pid": 0}}}`, sid))
	if SessionRunning(dir, sid).PID != 0 {
		t.Fatal("pid 0 is not a worker")
	}
}

func TestProcessAlive_RealPids(t *testing.T) {
	if !processAlive(os.Getpid()) {
		t.Error("own pid must be alive")
	}
	if processAlive(1<<22 - 1) {
		t.Error("absurd pid should not be alive")
	}
}

func writeLive(t *testing.T, dir string, pid int, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(dir, "sessions"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "sessions", fmt.Sprintf("%d.json", pid)), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

// A terminal window started with `claude --resume <id>` is in sessions/, not
// in the roster: observed on 2.1.310, where the window's file carries the
// resumed id, not a fresh one.
func TestSessionRunning_TerminalSession(t *testing.T) {
	oldAlive, oldStart := processAlive, procStart
	defer func() { processAlive, procStart = oldAlive, oldStart }()
	processAlive = func(pid int) bool { return pid == 100 || pid == 300 }
	procStart = func(pid int) string { return map[int]string{100: "95310080", 300: "777"}[pid] }

	dir := t.TempDir()
	writeLive(t, dir, 1, "{not json")
	if err := os.Mkdir(filepath.Join(dir, "sessions", "2.json"), 0o755); err != nil { // unreadable as a file
		t.Fatal(err)
	}
	writeLive(t, dir, 200, fmt.Sprintf(`{"pid":200,"sessionId":%q,"procStart":"1","kind":"interactive"}`, sid))
	writeLive(t, dir, 300, `{"pid":300,"sessionId":"other","procStart":"777"}`)
	if got := SessionRunning(dir, sid); got.PID != 0 {
		t.Fatalf("dead pid, other id, bad json: not running, got %+v", got)
	}
	writeLive(t, dir, 100, fmt.Sprintf(`{"pid":100,"sessionId":%q,"procStart":"95310080","kind":"interactive"}`, sid))
	if got := SessionRunning(dir, sid); got != (Live{PID: 100}) {
		t.Fatalf("live terminal session: running, not in the background, got %+v", got)
	}
	writeLive(t, dir, 100, fmt.Sprintf(`{"pid":100,"sessionId":%q,"procStart":"4"}`, sid))
	if got := SessionRunning(dir, sid); got.PID != 0 {
		t.Fatalf("pid recycled since the file was written (start time differs): not running, got %+v", got)
	}
	writeLive(t, dir, 100, fmt.Sprintf(`{"pid":100,"sessionId":%q}`, sid))
	if got := SessionRunning(dir, sid); got.PID != 100 {
		t.Fatalf("no procStart recorded: the live pid is trusted, as Claude Code does, got %+v", got)
	}
	writeLive(t, dir, 0, fmt.Sprintf(`{"pid":0,"sessionId":%q}`, sid))
	if err := os.Remove(filepath.Join(dir, "sessions", "100.json")); err != nil {
		t.Fatal(err)
	}
	if got := SessionRunning(dir, sid); got.PID != 0 {
		t.Fatalf("pid 0 is not a session, got %+v", got)
	}
}

func TestSessionRunning_WorkerWinsOverItsTerminalEntry(t *testing.T) {
	old := processAlive
	defer func() { processAlive = old }()
	processAlive = func(int) bool { return true }
	dir := t.TempDir()
	writeRoster(t, dir, fmt.Sprintf(`{"workers":{"aa": {"sessionId": %q, "pid": 100}}}`, sid))
	writeLive(t, dir, 100, fmt.Sprintf(`{"pid":100,"sessionId":%q}`, sid))
	if got := SessionRunning(dir, sid); got != (Live{PID: 100, Background: true}) {
		t.Fatalf("a worker is attached to, got %+v", got)
	}
}

func TestReadProcStart(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "7"), 0o755); err != nil {
		t.Fatal(err)
	}
	// comm with a space and a ')' must not shift the fields.
	stat := "7 (a b) c) S 1 7 7 0 -1 4194560 100 0 0 0 1 2 0 0 20 0 1 0 96939642 1000 50"
	if err := os.WriteFile(filepath.Join(root, "7", "stat"), []byte(stat), 0o600); err != nil {
		t.Fatal(err)
	}
	noPS := func(int) (string, error) { t.Fatal("ps must not run when /proc has the pid"); return "", nil }
	if got := readProcStart(root, 7, noPS); got != "96939642" {
		t.Errorf("proc: got %q", got)
	}
	if err := os.WriteFile(filepath.Join(root, "7", "stat"), []byte("7 (x) S 1"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := readProcStart(root, 7, noPS); got != "" {
		t.Errorf("short stat: got %q", got)
	}
	ps := func(pid int) (string, error) { return "Wed Oct 29 08:01:02 2026\n", nil }
	if got := readProcStart(root, 8, ps); got != "Wed Oct 29 08:01:02 2026" {
		t.Errorf("ps fallback: got %q", got)
	}
	failing := func(int) (string, error) { return "", fmt.Errorf("exit status 1") }
	if got := readProcStart(root, 8, failing); got != "" {
		t.Errorf("ps failure: got %q", got)
	}
}

func TestProcStart_RealPid(t *testing.T) {
	if procStart(os.Getpid()) == "" {
		t.Error("own start time must be readable")
	}
	if out, err := psLstart(os.Getpid()); err != nil || strings.TrimSpace(out) == "" {
		t.Errorf("ps lstart: %q %v", out, err)
	}
}

func TestLockSession(t *testing.T) {
	dir := t.TempDir()
	unlock, err := lockSession(dir, sid)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := lockSession(dir, sid); !errors.Is(err, errLocked) {
		t.Fatalf("second lock while held: want errLocked, got %v", err)
	}
	other, err := lockSession(dir, "other")
	if err != nil {
		t.Fatalf("another PR's lock is independent: %v", err)
	}
	other()
	unlock()
	again, err := lockSession(dir, sid)
	if err != nil {
		t.Fatalf("after unlock: %v", err)
	}
	again()
	if _, err := lockSession(filepath.Join(dir, "missing"), sid); err == nil || errors.Is(err, errLocked) {
		t.Errorf("unopenable lock file: want a plain error, got %v", err)
	}
}

func TestLockUnderCache(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	t.Setenv("HOME", t.TempDir()) // macOS's UserCacheDir
	unlock, err := lockUnderCache(sid)
	if err != nil {
		t.Fatal(err)
	}
	defer unlock()
	if _, err := lockUnderCache(sid); !errors.Is(err, errLocked) {
		t.Fatalf("second lock while held: want errLocked, got %v", err)
	}
	t.Setenv("XDG_CACHE_HOME", "relative")
	if _, err := lockUnderCache(sid); err == nil {
		t.Error("no usable cache dir: want an error")
	}
}
