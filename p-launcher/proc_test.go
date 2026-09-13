package main

import (
	"os"
	"path/filepath"
	"strconv"
	"testing"
)

func fakeProc(t *testing.T, procs map[int][3]string) string {
	t.Helper()
	root := t.TempDir()
	for pid, p := range procs { // comm, cmdline (argv NUL-joined), ppid
		d := filepath.Join(root, strconv.Itoa(pid))
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
		stat := strconv.Itoa(pid) + " (" + p[0] + ") S " + p[2] + " 1 1 0 -1"
		for f, c := range map[string]string{"comm": p[0] + "\n", "cmdline": p[1], "stat": stat} {
			if err := os.WriteFile(filepath.Join(d, f), []byte(c), 0o644); err != nil {
				t.Fatal(err)
			}
		}
	}
	return root
}

func TestClaudePID(t *testing.T) {
	proc := fakeProc(t, map[int][3]string{
		10: {"ghostty", "ghostty\x00--x11-instance-name=p:m/a", "1"},
		20: {"zsh", "zsh\x00-ic\x00claude; exec zsh", "10"},
		30: {".claude-unwrapp", "claude\x00--add-dir\x00/x", "20"},
		40: {"zsh", "/usr/bin/zsh\x00-c\x00source snap", "30"},
		50: {"p-launcher", "p-launcher\x00hook", "40"},
		// comm with spaces and parens, argv0 a full path
		60: {"we (ird) name", "/nix/store/abc/bin/claude", "1"},
		70: {"zsh", "zsh", "60"},
	})
	if got := claudePID(proc, 50); got != 30 {
		t.Fatalf("got %d want 30", got)
	}
	if got := claudePID(proc, 70); got != 60 {
		t.Fatalf("full-path argv0: got %d want 60", got)
	}
	if got := claudePID(proc, 20); got != 0 {
		t.Fatalf("no claude ancestor: got %d", got)
	}
	if got := claudePID(proc, 999); got != 0 {
		t.Fatalf("unknown pid: got %d", got)
	}
	if !isClaude(proc, 30) || isClaude(proc, 40) || isClaude(proc, 999) {
		t.Fatal("isClaude")
	}
	if parentPID(proc, 60) != 1 || parentPID(proc, 999) != 0 {
		t.Fatal("parentPID with parens in comm")
	}
}

// fakeCwd gives a fake /proc entry a cwd symlink. The target need not exist:
// os.Readlink reports the link's text, which is all liveFromProc reads.
func fakeCwd(t *testing.T, proc string, pid int, dir string) {
	t.Helper()
	if err := os.Symlink(dir, filepath.Join(proc, strconv.Itoa(pid), "cwd")); err != nil {
		t.Fatal(err)
	}
}

func TestLiveFromProc(t *testing.T) {
	root := "/home/x/p"
	proc := fakeProc(t, map[int][3]string{
		// a claude working in a project: the case session rows miss when the
		// session has not submitted a prompt yet
		11: {".claude-unwrapp", "/nix/store/abc-claude-code/bin/claude\x00", "1"},
		// a claude outside ~/p entirely
		12: {".claude-unwrapp", "/nix/store/abc-claude-code/bin/claude\x00", "1"},
		// not claude, but sitting in a project
		13: {"zsh", "zsh\x00-ic\x00claude; exec zsh", "1"},
		// a claude with no readable cwd (denied, or exited mid-scan)
		14: {".claude-unwrapp", "/nix/store/abc-claude-code/bin/claude\x00", "1"},
	})
	fakeCwd(t, proc, 11, "/home/x/p/personal/demo/dotfiles")
	fakeCwd(t, proc, 12, "/home/x/code/elsewhere")
	fakeCwd(t, proc, 13, "/home/x/p/m/other")

	live := liveFromProc(proc, root)
	if !live["personal/demo"] {
		t.Errorf("missed a claude working in a project: %v", live)
	}
	if live["m/other"] {
		t.Errorf("a non-claude process must not mark a project live: %v", live)
	}
	if len(live) != 1 {
		t.Errorf("live = %v, want only personal/demo", live)
	}
}

// A project deep inside a clone still maps to the project, since that is where
// a session working on a checkout actually sits.
func TestLiveFromProcMapsNestedCwd(t *testing.T) {
	proc := fakeProc(t, map[int][3]string{
		21: {".claude-unwrapp", "/nix/store/abc-claude-code/bin/claude\x00", "1"},
	})
	fakeCwd(t, proc, 21, "/home/x/p/m/thing/repo/src/deep")
	if live := liveFromProc(proc, "/home/x/p"); !live["m/thing"] {
		t.Errorf("nested cwd did not map to its project: %v", live)
	}
}
