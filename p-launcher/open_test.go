package main

import (
	"errors"
	"os/exec"
	"reflect"
	"testing"
)

func TestDecide(t *testing.T) {
	tree := []byte(treeFixture)
	act, con, err := decide(tree, "p:m/dependabot")
	if err != nil || act != actFocus || con != 5 {
		t.Fatalf("got %v %d %v (focused is con 6, next wraps to 5)", act, con, err)
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
