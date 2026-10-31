package main

import (
	"strings"
	"testing"
)

func TestTickWithoutReviewReposRunsNothing(t *testing.T) {
	h := newHost(t, `{}`, true, true)
	h.d.Home = t.TempDir()
	t.Setenv("XDG_STATE_HOME", "") // the ledger goes under Home, never the real state dir
	if code := run([]string{"tick"}, h.d); code != 0 {
		t.Fatalf("exit %d: %s", code, h.stderr)
	}
	if !strings.Contains(h.stderr.String(), "review_repos is empty") || len(h.ghCalls) != 0 || len(h.outputs) != 0 {
		t.Errorf("stderr %q, gh %v, outputs %v", h.stderr, h.ghCalls, h.outputs)
	}
}

func TestTickSkipsWhenAnotherTickRuns(t *testing.T) {
	h := newHost(t, `{"review_repos":["acme/*"]}`, true, true)
	h.d.Home = t.TempDir()
	t.Setenv("XDG_STATE_HOME", "") // the ledger goes under Home, never the real state dir
	h.locked = true
	if code := run([]string{"tick"}, h.d); code != 0 || len(h.ghCalls) != 0 {
		t.Errorf("exit %d, gh %v", code, h.ghCalls)
	}
}

func TestClickRecordsAcceptanceOnlyForReviewRepos(t *testing.T) {
	for _, c := range []struct {
		cfg  string
		want bool
	}{{`{"terminal": false, "review_repos":["acme/*"]}`, true}, {`{"terminal": false, "review_repos":["globex/*"]}`, false}} {
		h := newHost(t, c.cfg, true, true)
		h.d.Home = t.TempDir()
		t.Setenv("XDG_STATE_HOME", "") // the ledger goes under Home, never the real state dir
		h.d.Gh = func(args ...string) (string, error) {
			h.ghCalls = append(h.ghCalls, args)
			return `{"state":"open","head":{"sha":"h7"},"base":{"ref":"main"}}`, nil
		}
		if code := run([]string{target}, h.d); code != 0 {
			t.Fatalf("click exit %d: %s", code, h.stderr)
		}
		l, err := OpenLedger(LedgerPath(h.d.Home))
		if err != nil {
			t.Fatal(err)
		}
		a, ok, _ := l.Acceptance(widgetsPR)
		_ = l.Close()
		if ok != c.want || (ok && (a.Head != "h7" || a.Base != "main")) {
			t.Errorf("%s: accepted=%v %+v", c.cfg, ok, a)
		}
	}
}
