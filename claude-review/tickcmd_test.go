package main

import (
	"errors"
	"net/http"
	"net/http/httptest"
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

func TestRunTickAsksGitHubWhoTheUserIs(t *testing.T) {
	down := httptest.NewServer(http.NotFoundHandler())
	down.Close()
	for _, c := range []struct {
		cfg    string
		ghErr  error
		want   int
		asksGh bool
	}{
		{`{"review_repos":["acme/*"],"elly_url":"` + down.URL + `"}`, nil, 1, true},
		{`{"review_repos":["acme/*"],"elly_url":"` + down.URL + `","github_user":"me"}`, nil, 1, false},
		{`{"review_repos":["acme/*"],"elly_url":"` + down.URL + `"}`, errors.New("401"), 1, true},
	} {
		h := newHost(t, c.cfg, true, true)
		h.d.Home = t.TempDir()
		t.Setenv("XDG_STATE_HOME", "") // the ledger goes under Home, never the real state dir
		asked := false
		h.d.Gh = func(args ...string) (string, error) {
			if strings.Join(args, " ") == "api user --jq .login" {
				asked = true
			}
			return "me\n", c.ghErr
		}
		if code := run([]string{"tick"}, h.d); code != c.want || asked != c.asksGh {
			t.Errorf("%s (gh err %v): exit %d, asked gh %v; stderr %q", c.cfg, c.ghErr, code, asked, h.stderr)
		}
		if c.ghErr == nil && !strings.Contains(h.stderr.String(), "elly") {
			t.Errorf("elly being down is not what failed: %q", h.stderr)
		}
	}
}

// `tick --self-check` runs the check by hand, whatever version the ledger
// holds; a failure trips the breaker like the timer's would.
func TestTickSelfCheckFlagRunsTheCheck(t *testing.T) {
	h, fs := clickHost(t, true)
	h.captured = []string{"rc noise\n2.1.315 (Claude Code)\n"}
	fs.unreadable = true
	if code := run([]string{"tick", "--self-check"}, h.d); code != 1 {
		t.Errorf("exit %d: %s", code, h.stderr)
	}
	reason, _ := h.ledger(t).Tripped()
	if !strings.Contains(reason, "the self-check on 2.1.315 (Claude Code) failed") || !strings.Contains(notified(h), "self-check") {
		t.Errorf("breaker %q, notified %q", reason, notified(h))
	}
	if len(h.ghCalls) != 0 {
		t.Errorf("a self-check asked GitHub: %q", h.ghCalls)
	}
}

// A click hands the PR over, and a PR the user was only asked about in a
// DM is invisible to elly until the user is a reviewer: the click requests
// the user's own review, unless the user wrote the PR, is already asked, or
// has reviewed it.
func TestClickRequestsTheUsersOwnReview(t *testing.T) {
	for _, c := range []struct {
		name, pull, reviews string
		want                bool
	}{
		{"asked in a DM only", `{"state":"open","user":{"login":"bob"},"requested_reviewers":[],"head":{"sha":"h7"},"base":{"ref":"main"}}`, `[]`, true},
		{"the user's own PR", `{"state":"open","user":{"login":"Me"},"requested_reviewers":[],"head":{"sha":"h7"},"base":{"ref":"main"}}`, `[]`, false},
		{"already asked", `{"state":"open","user":{"login":"bob"},"requested_reviewers":[{"login":"me"}],"head":{"sha":"h7"},"base":{"ref":"main"}}`, `[]`, false},
		{"already reviewed", `{"state":"open","user":{"login":"bob"},"requested_reviewers":[],"head":{"sha":"h7"},"base":{"ref":"main"}}`, `[{"user":{"login":"me"},"state":"APPROVED"}]`, false},
	} {
		t.Run(c.name, func(t *testing.T) {
			h := newHost(t, `{"terminal": false, "review_repos":["acme/*"], "github_user":"me"}`, true, true)
			h.d.Home = t.TempDir()
			t.Setenv("XDG_STATE_HOME", "") // the ledger goes under Home, never the real state dir
			var requested []string
			h.d.Gh = func(args ...string) (string, error) {
				h.ghCalls = append(h.ghCalls, args)
				joined := strings.Join(args, " ")
				switch {
				case strings.Contains(joined, "requested_reviewers"):
					requested = append(requested, joined)
					return `{}`, nil
				case strings.HasSuffix(joined, "pulls/42/reviews"):
					return c.reviews, nil
				}
				return c.pull, nil
			}
			if code := run([]string{target}, h.d); code != 0 {
				t.Fatalf("click exit %d: %s", code, h.stderr)
			}
			want := "api -X POST repos/acme/widgets/pulls/42/requested_reviewers -f reviewers[]=me"
			if got := len(requested) == 1 && requested[0] == want; got != c.want {
				t.Errorf("requested %q, want a request: %v", requested, c.want)
			}
		})
	}
}
