package main

import (
	"fmt"
	"net/url"
	"regexp"
)

// verbLink is a claude-review:// link with a verb, as tick writes them into
// elly. The plain link without a verb stays the click it always was.
var verbLink = regexp.MustCompile(`^claude-review://([A-Za-z0-9._-]+)/([A-Za-z0-9._-]+)/([1-9][0-9]*)/(approve|attach|ignore)(?:\?([^#]*))?$`)

// ParseVerb reads a verb link.
func ParseVerb(s string) (PR, string, url.Values, bool) {
	m := verbLink.FindStringSubmatch(s)
	if m == nil {
		return PR{}, "", nil, false
	}
	pr, err := ParseTarget(m[1] + "/" + m[2] + "/" + m[3])
	if err != nil {
		return PR{}, "", nil, false
	}
	q, err := url.ParseQuery(m[5])
	if err != nil {
		return PR{}, "", nil, false
	}
	return pr, m[4], q, true
}

// runVerb handles a verb link. A link can come from anywhere a click can, so
// approve acts only on a token tick minted for this PR and commit.
func runVerb(d Deps, cfg Config, pr PR, verb string, q url.Values) int {
	switch verb {
	case "approve":
		return approveWithToken(d, pr, q.Get("at"), q.Get("token"))
	case "ignore":
		l, err := OpenLedger(LedgerPath(d.Home))
		if err != nil {
			return fail(d, err)
		}
		defer func() { _ = l.Close() }()
		if err := l.Ignore(pr); err != nil {
			return fail(d, err)
		}
		if err := l.ClearAnnotation(pr); err != nil {
			return fail(d, err)
		}
		_ = Elly{Base: cfg.EllyURL, TokenFile: ellyTokenFile()}.Delete(pr.URL())
		tell(d, fmt.Sprintf("%s ignored: no more unattended rounds until you click claude review on it again", pr.Slug()))
		return 0
	default: // attach
		return attach(d, cfg, pr)
	}
}

func approveWithToken(d Deps, pr PR, at, token string) int {
	l, err := OpenLedger(LedgerPath(d.Home))
	if err != nil {
		return fail(d, err)
	}
	ok, err := l.UseToken(pr, at, token)
	_ = l.Close()
	if err != nil {
		return fail(d, err)
	}
	if !ok {
		return fail(d, fmt.Errorf("claude-review: that Approve link for %s is not one this machine issued, or it was used already", pr.Slug()))
	}
	if code := runApprove([]string{pr.URL(), "--at", at}, d.Gh, d.Stdout, d.Stderr); code != 0 {
		return fail(d, fmt.Errorf("claude-review: approving %s failed; see the session", pr.Slug()))
	}
	tell(d, fmt.Sprintf("%s approved at %.9s", pr.Slug(), at))
	return 0
}

// attach opens the PR's session in a terminal without sending a prompt: a
// background worker is attached to, any other session resumed.
func attach(d Deps, cfg Config, pr PR) int {
	id := pr.SessionID()
	live := d.SessionRunning(d.ClaudeDir, id)
	if live.PID > 0 && !live.Background {
		return fail(d, fmt.Errorf("claude-review: %s is already open (pid %d)", pr.Slug(), live.PID))
	}
	argv := []string{claudeCmd, "--resume", id}
	if live.PID > 0 {
		argv = AttachArgs(pr)
	}
	dir, _, err := d.FindRepo(cfg.GitRepoRoots, cfg.ClonePath, pr)
	if err != nil {
		return fail(d, err)
	}
	if err := openTerminal(d, cfg, dir, [][]string{argv}, pr); err != nil {
		return fail(d, err)
	}
	return 0
}

// tell reports a verb's result: a link click has no terminal to print to.
func tell(d Deps, msg string) {
	fmt.Fprintln(d.Stderr, "claude-review:", msg)
	if !d.IsTTY {
		_, _ = d.Output("", notifyArgv(d.GOOS, "claude-review", msg))
	}
}
