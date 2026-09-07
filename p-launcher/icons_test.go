package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestIconSVG(t *testing.T) {
	s := string(iconSVG("you", "#d95926", 13))
	for _, want := range []string{`<svg`, `viewBox="0 0 24 24"`, `stroke="#d95926"`, `width="13"`, `<path`} {
		if !strings.Contains(s, want) {
			t.Fatalf("missing %q in %s", want, s)
		}
	}
	if iconSVG("nope", "#000", 13) != "" {
		t.Fatal("unknown icon must render nothing")
	}
}

func TestWriteIcons(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "icons")
	paths, err := writeIcons(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"you", "claude", "idle", "review", "blank"} {
		p, ok := paths[name]
		if !ok {
			t.Fatalf("no path for %s", name)
		}
		b, err := os.ReadFile(p)
		if err != nil || !strings.HasPrefix(string(b), `<svg xmlns=`) {
			t.Fatalf("%s: %v %q", name, err, b)
		}
	}
	// second call is a no-op for unchanged content: mtime unchanged
	st1, _ := os.Stat(paths["you"])
	time.Sleep(10 * time.Millisecond)
	if _, err := writeIcons(dir); err != nil {
		t.Fatal(err)
	}
	st2, _ := os.Stat(paths["you"])
	if !st1.ModTime().Equal(st2.ModTime()) {
		t.Fatal("rewrote unchanged icon")
	}
}
