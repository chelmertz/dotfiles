package main

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestDecide(t *testing.T) {
	tree := []byte(treeFixture)
	act, con, err := decide(tree, "p:m/dependabot")
	if err != nil || act != actFocus || con != 5 {
		t.Fatalf("got %v %d %v (focused con wraps to the first, con 5)", act, con, err)
	}
	act, _, err = decide(tree, "p:m/nope")
	if err != nil || act != actLaunch {
		t.Fatalf("got %v %v", act, err)
	}
	if _, _, err := decide([]byte("{"), "x"); err == nil {
		t.Fatal("want error")
	}
}

func TestScrubEnv(t *testing.T) {
	in := []string{"CLAUDECODE=1", "CLAUDE_CODE_SESSION_ID=x", "PATH=/bin", "HOME=/h"}
	want := []string{"PATH=/bin", "HOME=/h"}
	if got := scrubEnv(in); !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v want %v", got, want)
	}
}

func TestProjectDir(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "m", "dependabot"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "m", "afile"), nil, 0o644); err != nil {
		t.Fatal(err)
	}

	dir, f, err := projectDir(root, "m/dependabot")
	if err != nil {
		t.Fatal(err)
	}
	if dir != filepath.Join(root, "m", "dependabot") {
		t.Fatalf("dir = %q", dir)
	}
	if f != (Found{Namespace: "m", Name: "dependabot", Path: "m/dependabot"}) {
		t.Fatalf("got %+v", f)
	}

	if _, _, err := projectDir(root, "m/nope"); err == nil {
		t.Fatal("want error for missing directory")
	}
	if _, _, err := projectDir(root, "m/afile"); err == nil {
		t.Fatal("want error for a file, not a directory")
	}
	if _, _, err := projectDir(root, "dependabot"); err == nil {
		t.Fatal("want error for a path with no namespace/name slash")
	}
	for _, bad := range []string{"m/dependabot/x", "../m", "m/..", "m/", "/dependabot", "./m"} {
		if _, _, err := projectDir(root, bad); err == nil {
			t.Fatalf("want error for %q", bad)
		}
	}
}

func TestGhostExitedErr(t *testing.T) {
	if err := ghostExitedErr(nil, "p:m/x"); !strings.Contains(err.Error(), "exited 0") {
		t.Fatalf("got %q", err.Error())
	}
	runErr := exec.Command("false").Run()
	if err := ghostExitedErr(runErr, "p:m/x"); !strings.Contains(err.Error(), "exited 1") {
		t.Fatalf("got %q", err.Error())
	}
}

func TestExitDesc(t *testing.T) {
	if got := exitDesc(nil); got != "0" {
		t.Fatalf("nil: got %q", got)
	}
	if got := exitDesc(errors.New("boom")); got != "boom" {
		t.Fatalf("non-exit error: got %q", got)
	}
	err := exec.Command("false").Run()
	if got := exitDesc(err); got != "1" {
		t.Fatalf("exit error: got %q", got)
	}
}
