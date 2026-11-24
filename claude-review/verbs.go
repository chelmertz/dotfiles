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
const (
	verbApprove = "approve"
	verbAttach  = "attach"
	verbStates  = "states"
)

// knownVerbs are the verbs verbLink accepts; runVerb refuses any other.
var knownVerbs = []string{verbApprove, verbAttach, verbStates}

var verbLink = regexp.MustCompile(`^claude-review://([A-Za-z0-9._-]+)/([A-Za-z0-9._-]+)/([1-9][0-9]*)/(` + strings.Join(knownVerbs, "|") + `)(?:\?([^#]*))?$`)

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
	case verbApprove:
		return approveWithToken(d, pr, q.Get("at"), q.Get("token"))
	case verbStates:
		return runStates([]string{pr.URL()}, d, true)
	case verbAttach:
		return attach(d, cfg, pr)
	default:
		return fail(d, fmt.Errorf("claude-review: unknown link verb %q for %s", verb, pr.URL()))
	}
}

func approveWithToken(d Deps, pr PR, at, token string) int {
	tok, err := ids.ParseToken(token)
	if err != nil {
		return fail(d, notIssued(pr))
	}
	unlock, err := approveGuard(d, pr)
	if err != nil {
		return fail(d, err)
	}
	defer unlock()
	l, err := OpenLedger(LedgerPath(d.StateDir))
	if err != nil {
		return fail(d, err)
	}
	// The window is read here, under pr's lock: a fork may hold prod data,
	// so no Approve link is honoured beside one, nor while one cannot be
	// ruled out.
	window := windowOf(d.Sessions, l, pr)
	if window != winClosed {
		_ = l.Close()
		return fail(d, approveWindowRefusal(pr, window))
	}
	ok, err := l.UseToken(pr, at, tok, window)
	_ = l.Close()
	if err != nil {
		return fail(d, fmt.Errorf("claude-review: %s: %w", pr.URL(), err))
	}
	if !ok {
		return fail(d, notIssued(pr))
	}
	// at is the head the spent token was minted for (UseToken), so the
	// log takes it as it takes a typed --at.
	code, outcome := approveAt(d.Gh, pr, at, d.Stdout, d.Stderr)
	if err := logApproval(d, pr, at, originLink, outcome); err != nil {
		return fail(d, fmt.Errorf("claude-review: %s: %s at %.9s, but logging it failed: %w", pr.URL(), outcome, at, err))
	}
	if code != 0 {
		return fail(d, fmt.Errorf("claude-review: approving %s failed; see the session", pr.URL()))
	}
	tell(d, fmt.Sprintf("%s approved at %.9s", pr.URL(), at))
	return 0
}

// approveGuard takes pr's lock and refuses while a dismissal of the user's
// approval is pending: approving then would undo it. Under the lock, tick
// cannot record a dismissal between this check and the approval.
func approveGuard(d Deps, pr PR) (unlock func(), err error) {
	lk, err := lockPR(pr, d.Lock)
	unlock = lk.Unlock
	if errors.Is(err, errLocked) {
		return nil, fmt.Errorf("claude-review: %s is busy; try again in a moment", pr.URL())
	}
	if err != nil {
		return nil, fmt.Errorf("claude-review: locking %s: %w", pr.URL(), err)
	}
	l, err := OpenLedger(LedgerPath(d.StateDir))
	if err != nil {
		unlock()
		return nil, err
	}
	pending, err := l.DismissalPending(pr)
	_ = l.Close()
	if err == nil && pending {
		err = fmt.Errorf("claude-review: a dismissal of your approval on %s is still pending; approve after it lands", pr.URL())
	}
	if err != nil {
		unlock()
		return nil, err
	}
	return unlock, nil
}

// runApproveCmd is the typed approve: the same guard as a link, no token,
// and its outcome logged as the link's is.
func runApproveCmd(args []string, d Deps) int {
	return runApproveGuarded(args, d.Gh, d.Stdout, d.Stderr, func(pr PR) (func(), error) { return approveGuard(d, pr) },
		func(pr PR, at, outcome string) error { return logApproval(d, pr, at, originVerb, outcome) })
}

// approveWindowRefusal is why an Approve link is refused under window.
func approveWindowRefusal(pr PR, window WindowState) error {
	if window == winOpen {
		return fmt.Errorf("claude-review: %s is open in a window; approve once it is closed", pr.URL())
	}
	return fmt.Errorf("claude-review: %s: %s, so its Approve link waits", pr.URL(), windowUnknownNote)
}

// notIssued refuses an Approve link: forged, malformed or spent alike.
func notIssued(pr PR) error {
	return fmt.Errorf("claude-review: that Approve link for %s is not one this machine issued, or it was used already", pr.URL())
}

// attach is Open Claude: the PR's live background worker in a terminal,
// under the PR's lock, or with none working, a locked-down fork of its last
// session (fork.go). It never resumes the session itself: that window would
// run under the user's own settings and hold every round back.
func attach(d Deps, cfg Config, pr PR) int {
	lk, err := lockPR(pr, d.Lock)
	if errors.Is(err, errLocked) {
		return fail(d, fmt.Errorf("claude-review: %s is being started; try again in a moment", pr.URL()))
	}
	if err != nil {
		return fail(d, fmt.Errorf("claude-review: locking %s: %w", pr.URL(), err))
	}
	defer lk.Unlock()
	id, err := attachSession(d, pr)
	if err != nil {
		return fail(d, err)
	}
	all, err := d.Sessions.Roster()
	if err != nil {
		return fail(d, fmt.Errorf("claude-review: %s: %w", pr.URL(), err))
	}
	mine, err := d.Sessions.workersFor(all, id)
	if err != nil {
		return fail(d, fmt.Errorf("claude-review: %s: %w", pr.URL(), err))
	}
	var key ids.WorkerKey
	for _, w := range mine {
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
	case d.Sessions.WindowOf(id) == winOpen:
		return fail(d, fmt.Errorf("claude-review: %s is already open in a window", pr.URL()))
	case d.Sessions.WindowOf(id) == winUnknown:
		return fail(d, fmt.Errorf("claude-review: %s: %s", pr.URL(), windowUnknownNote))
	default:
		return openFork(d, cfg, lk, pr)
	}
	return 0
}

// attachSession is the session Open Claude opens: the one pr's newest
// round recorded, so a worker started before a policy version bump is
// still found.
func attachSession(d Deps, pr PR) (ids.SessionID, error) {
	l, err := OpenLedger(LedgerPath(d.StateDir))
	if err != nil {
		return ids.SessionID{}, err
	}
	defer l.Close()
	return l.RoundSession(pr)
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
