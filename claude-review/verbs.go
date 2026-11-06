package main

import (
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"strings"

	"github.com/chelmertz/dotfiles/claude-review/ids"
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
		l, err := OpenLedger(LedgerPath(d.StateDir))
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
		_ = Elly{Base: cfg.EllyURL, TokenFile: ellyTokenFile(d.RuntimeDir)}.Delete(pr.URL())
		tell(d, fmt.Sprintf("%s ignored: no more unattended rounds until you click claude review on it again", pr.URL()))
		return 0
	default: // attach
		return attach(d, cfg, pr)
	}
}

func approveWithToken(d Deps, pr PR, at, token string) int {
	tok, err := ids.ParseToken(token)
	if err != nil {
		return fail(d, notIssued(pr))
	}
	l, err := OpenLedger(LedgerPath(d.StateDir))
	if err != nil {
		return fail(d, err)
	}
	ok, err := l.UseToken(pr, at, tok)
	_ = l.Close()
	if err != nil {
		return fail(d, err)
	}
	if !ok {
		return fail(d, notIssued(pr))
	}
	if code := runApprove([]string{pr.URL(), "--at", at}, d.Gh, d.Stdout, d.Stderr); code != 0 {
		return fail(d, fmt.Errorf("claude-review: approving %s failed; see the session", pr.URL()))
	}
	tell(d, fmt.Sprintf("%s approved at %.9s", pr.URL(), at))
	return 0
}

// notIssued refuses an Approve link: forged, malformed or spent alike.
func notIssued(pr PR) error {
	return fmt.Errorf("claude-review: that Approve link for %s is not one this machine issued, or it was used already", pr.URL())
}

// attach opens the PR's live background worker in a terminal, under the
// PR's lock. It never resumes a session interactively: that window would
// run under the user's own settings and hold every round back. With no
// worker there is nothing to watch, and it says so.
func attach(d Deps, cfg Config, pr PR) int {
	id := pr.RoundSessionID()
	unlock, err := d.Lock(pr.SessionID().String())
	if errors.Is(err, errLocked) {
		return fail(d, fmt.Errorf("claude-review: %s is being started; try again in a moment", pr.URL()))
	}
	if err != nil {
		return fail(d, fmt.Errorf("claude-review: locking %s: %w", pr.URL(), err))
	}
	defer unlock()
	all, err := d.Sessions.Roster()
	if err != nil {
		return fail(d, fmt.Errorf("claude-review: %s: %w", pr.URL(), err))
	}
	var key ids.WorkerKey
	for _, w := range d.Sessions.workersFor(id, all) {
		if w.SessionID == id {
			key = w.Short
		}
	}
	switch {
	case !key.IsZero():
		dir, _, err := d.FindRepo(cfg.GitRepoRoots, cfg.ClonePath, pr)
		if err != nil {
			return fail(d, err)
		}
		if err := openTerminal(d, cfg, dir, [][]string{AttachArgs(key)}, pr); err != nil {
			return fail(d, err)
		}
	case d.Sessions.WindowOpen(id):
		return fail(d, fmt.Errorf("claude-review: %s is already open in a window", pr.URL()))
	default:
		tell(d, "no round running on "+pr.URL())
	}
	return 0
}

// hostless drops "https://github.com/" from a click's notification: the
// owner/repo/pull/n that is left names the PR, and the rest is noise.
func hostless(msg string) string {
	return strings.ReplaceAll(msg, "https://github.com/", "")
}

// tell reports a verb's result: a link click has no terminal to print to.
func tell(d Deps, msg string) {
	fmt.Fprintln(d.Stderr, "claude-review:", msg)
	if !d.IsTTY {
		_, _ = d.Output("", notifyArgv(d.GOOS, "claude-review", hostless(msg)))
	}
}
