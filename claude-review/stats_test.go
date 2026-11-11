package main

import (
	"testing"
	"time"
)

// stats prints the sources of recent rounds, most first, ties by name.
func TestStatsPrintsRoundsPerSource(t *testing.T) {
	h, _ := clickHost(t, true)
	l := h.ledger(t)
	now := time.Now()
	for _, tr := range []string{clickTrigger(now, "thunderbird"), clickTrigger(now, "thunderbird"), clickTrigger(now, "elly"), "new_head"} {
		if _, err := l.StartRound(testPR, "h", tr, now); err != nil {
			t.Fatal(err)
		}
	}
	if code := run([]string{"stats", "-days", "7"}, h.d); code != 0 {
		t.Fatalf("exit %d: %s", code, h.stderr)
	}
	want := "thunderbird  2\nelly         1\ntick         1\n"
	if got := h.stdout.String(); got != want {
		t.Errorf("got\n%s\nwant\n%s", got, want)
	}
	for _, bad := range [][]string{{"stats", "x"}, {"stats", "-days", "0"}} {
		if code := run(bad, h.d); code != 2 {
			t.Errorf("%q: exit %d, want 2", bad, code)
		}
	}
}
