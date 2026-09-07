package main

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestDiscover(t *testing.T) {
	root := t.TempDir()
	for _, d := range []string{"m/dependabot", "m/reputation", "personal/health", "archive/old-thing", "m/.hidden"} {
		if err := os.MkdirAll(filepath.Join(root, d), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	// files at both levels are ignored
	if err := os.WriteFile(filepath.Join(root, "CLAUDE.md"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "m", "CLAUDE.md"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	// symlinked directories are followed
	linkTarget := t.TempDir()
	if err := os.Symlink(linkTarget, filepath.Join(root, "m", "linked")); err != nil {
		t.Fatal(err)
	}

	got, err := Discover(root)
	if err != nil {
		t.Fatal(err)
	}
	want := []Found{
		{Namespace: "m", Name: "dependabot", Path: "m/dependabot"},
		{Namespace: "m", Name: "linked", Path: "m/linked"},
		{Namespace: "m", Name: "reputation", Path: "m/reputation"},
		{Namespace: "personal", Name: "health", Path: "personal/health"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %+v\nwant %+v", got, want)
	}
}

func TestDiscoverMissingRoot(t *testing.T) {
	_, err := Discover(filepath.Join(t.TempDir(), "nope"))
	if err == nil {
		t.Fatal("want error for missing root")
	}
}
