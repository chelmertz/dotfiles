package main

import (
	"errors"
	"fmt"
	"io"
	"slices"
	"strings"
	"time"

	"github.com/chelmertz/dotfiles/claude-review/ids"
)

// annotationTTL is how long elly shows an annotation tick wrote. Every tick
// re-sends the live ones, so a runner that stops shows its PRs as needing
// the user again within this time.
const annotationTTL = 30 * time.Minute

// roundLifetime is how long after its launch elly shows a round as
// running. Settling the round replaces the row long before this; it only
// matters when nothing settles it, a tick that stopped running. The
// longest recorded round (testdata/rounds/008) posted last 82 minutes after
// its start. running drops the PR's review-owed reasons in elly, as handled
// does, so a row that outlives a dead round hides them until it expires.
const roundLifetime = 2 * time.Hour

// tickElly is what tick needs from elly.
type tickElly interface {
	PRs() ([]EllyPR, error)
	// Username is the GitHub login elly polls as, empty without a token.
	Username() (string, error)
	Put(prURL string, a Annotation, expires time.Time) error
	PutSource(source, prURL string, a Annotation, expires time.Time) error
	Delete(prURL string) error
}

// tickGitHub is what tick needs from GitHub.
type tickGitHub interface {
	Gate
	RoundFacts(pr PR, r Round) (RoundFacts, error)
	Standing(pr PR) (state, head string, user *Review, err error)
	Dismiss(pr PR, id ids.ReviewID, message string) error
}

// tickHost is one tick's world. Every write goes through do, which only
// prints in a dry run.
type tickHost struct {
	cfg         Config
	tcfg        TickConfig
	ledger      *Ledger
	elly        tickElly
	gh          tickGitHub
	running     func(sessionID ids.SessionID) bool
	blocked     func(sessionID ids.SessionID) bool
	lastMessage func(sessionID ids.SessionID) (string, error)
	findRepo    func(pr PR) (dir, remote string, err error)
	start       func(dir string, argv []string) error
	now         func() time.Time
	out         io.Writer
	dry         bool
	// lock takes the PR's lock, the one a click holds, keyed by the PR's
	// SessionID whichever session it guards; errLocked means a click (or
	// the attach verb) has the PR, and tick leaves it this time.
	lock func(name string) (unlock func(), err error)
	// sess keeps one background worker per PR; every round starts there.
	sess Sessions
	// exists reports a transcript for a session id.
	exists func(id ids.SessionID) bool
	// trust marks a clone trusted for Claude Code before a launch in it.
	trust func(dir string) error
	// version is `claude --version`; selfCheck checks a new one, and notify
	// tells the user a check failed.
	version   func() (string, error)
	selfCheck func() error
	notify    func(msg string)
	// sessionIndex maps each PR to the session that last acted on it.
	sessionIndex func() (map[string]SessionHit, error)
}

// windowNote is the needs_you note for a PR whose session is open in a
// legacy interactive window: tick cannot start a round beside it.
const windowNote = "review window open; close it to let rounds run"

func (h tickHost) do(what string, fn func() error) error {
	if h.dry {
		fmt.Fprintln(h.out, "would", what)
		return nil
	}
	// The journal is the only record of what an unattended tick did. A
	// click's host has no out: it reports through its notification.
	if h.out != nil {
		fmt.Fprintln(h.out, what)
	}
	return fn()
}

// tick is one pass: read elly, stop rounds on closed PRs, settle finished
// rounds, start owed ones, refresh annotations. Without elly it does nothing at all; the annotations
// already written expire on their own.
func (h tickHost) tick() error {
	prs, err := h.elly.PRs()
	if err != nil {
		return err
	}
	if err := h.checkEllyLogin(); err != nil {
		return err
	}
	if err := h.checkClaude(); err != nil {
		return err
	}
	if err := h.sweep(); err != nil {
		return err
	}
	running, err := h.settle()
	if err != nil {
		return err
	}
	if err := h.startOwed(prs, running); err != nil {
		return err
	}
	if err := h.refresh(); err != nil {
		return err
	}
	h.linkSessions(prs)
	// A failed run is what the timer's journal shows.
	if reason, err := h.ledger.Tripped(); err != nil || reason != "" {
		return errors.Join(err, breakerError(reason))
	}
	return nil
}

// checkEllyLogin refuses a tick when elly polls as another GitHub login
// than the one rounds post as: elly's turn reasons are that login's, so
// rounds would start for PRs owed to someone else.
func (h tickHost) checkEllyLogin() error {
	login, err := h.elly.Username()
	if err != nil {
		return err
	}
	if login == "" {
		return fmt.Errorf("elly has no GitHub token; set one for @%s on its page", h.tcfg.User)
	}
	if !strings.EqualFold(login, h.tcfg.User) {
		return fmt.Errorf("elly polls as @%s and rounds post as @%s; set elly's token for @%s, or github_user", login, h.tcfg.User, h.tcfg.User)
	}
	return nil
}

// checkClaude runs the self-check when `claude --version` is not the one
// the last passing check recorded, before anything else this tick does.
// Rounds rest on roster, job-state and stop semantics that changed three
// times; a failed check trips the breaker, so nothing starts until the
// user has looked and reset it, and the next tick then checks again. While
// the breaker is tripped no check runs: nothing would start anyway.
func (h tickHost) checkClaude() error {
	if reason, err := h.ledger.Tripped(); err != nil || reason != "" {
		return err
	}
	v, err := h.version()
	if err != nil {
		return fmt.Errorf("claude --version: %w", err)
	}
	known, err := h.ledger.Meta(metaClaudeVersion)
	if err != nil || v == known {
		return err
	}
	if h.dry {
		fmt.Fprintf(h.out, "would run the self-check for %s\n", v)
		return nil
	}
	return h.runSelfCheck(v)
}

// runSelfCheck checks Claude Code v and records it, or trips the breaker.
func (h tickHost) runSelfCheck(v string) error {
	if err := h.selfCheck(); err != nil {
		reason := fmt.Sprintf("the self-check on %s failed: %v", v, err)
		h.notify(reason)
		return errors.Join(h.ledger.Trip(reason), breakerError(reason))
	}
	return h.ledger.SetMeta(metaClaudeVersion, v)
}

func breakerError(reason string) error {
	if reason == "" {
		return nil
	}
	return fmt.Errorf("no round starts: %s; `claude-review tick --reset` lets them start again", reason)
}

// sweep stops every background worker whose PR GitHub reports merged or
// closed, and settles that PR's unread rounds as closed. It is what makes
// tick converge from whatever state it finds, whoever left it: a click's
// round, a merge mid-round, a worker blocked on a question, a crash between
// any two steps. It keeps no state of its own and repeats safely. GitHub is
// asked only about PRs with a live worker or an unread round. An unreadable
// roster, a PR GitHub does not answer for and a PR a click holds are left
// for a later tick.
func (h tickHost) sweep() error {
	all, err := h.sess.Roster()
	if err != nil {
		fmt.Fprintf(h.out, "claude-review tick: no worker on a closed PR stopped: %v\n", err)
		return nil
	}
	urls, err := h.ledger.PRs()
	if err != nil {
		return err
	}
	rounds, err := h.ledger.UnreadRounds()
	if err != nil {
		return err
	}
	unread := map[string][]Round{}
	for _, r := range rounds {
		unread[r.PR] = append(unread[r.PR], r)
	}
	for _, u := range urls {
		pr, err := ParseTarget(u)
		if err != nil || (len(unread[u]) == 0 && len(h.workersOf(pr, all)) == 0) {
			continue
		}
		_, open, err := h.gh.Base(pr)
		if err == nil && !open {
			err = h.closeOut(pr, unread[u])
		}
		if err != nil {
			fmt.Fprintf(h.out, "claude-review tick: %s: %v\n", pr.URL(), err)
		}
	}
	return nil
}

// workersOf are pr's live workers under any id it ever ran under, forks
// included.
func (h tickHost) workersOf(pr PR, all []Worker) []Worker {
	var out []Worker
	for _, id := range pr.workerIDs() {
		for _, w := range h.sess.workersFor(id, all) {
			if !slices.ContainsFunc(out, func(o Worker) bool { return o.Short == w.Short }) {
				out = append(out, w)
			}
		}
	}
	return out
}

// closeOut stops closed pr's workers and settles its unread rounds the way
// settle does a closed PR's, under the PR's lock and from a roster read
// under it. A stop that fails leaves the rounds unread for the next tick.
func (h tickHost) closeOut(pr PR, rounds []Round) error {
	unlock, err := h.lock(pr.SessionID().String())
	if errors.Is(err, errLocked) {
		return nil
	}
	if err != nil {
		return err
	}
	defer unlock()
	all, err := h.sess.Roster()
	if err != nil {
		return err
	}
	for _, w := range h.workersOf(pr, all) {
		if err := h.do(fmt.Sprintf("stop worker %s on %s: the PR is merged or closed", w.Short, pr.URL()), func() error { return h.sess.stopAndWait(w) }); err != nil {
			return err
		}
	}
	for _, r := range rounds {
		if _, err := h.apply(pr, r, Decision{Clear: true}, false); err != nil {
			return err
		}
	}
	return nil
}

// settle reads every round the ledger started and has not read, and returns
// how many are still running. Each PR is read under its lock; a PR a click
// holds counts as running and is read next tick.
func (h tickHost) settle() (int, error) {
	rounds, err := h.ledger.UnreadRounds()
	if err != nil {
		return 0, err
	}
	running := 0
	for _, r := range rounds {
		pr, err := ParseTarget(r.PR)
		if err != nil {
			continue
		}
		unlock, err := h.lock(pr.SessionID().String())
		if err != nil {
			if !errors.Is(err, errLocked) {
				fmt.Fprintf(h.out, "claude-review tick: %s: %v\n", pr.URL(), err)
			}
			running++
			continue
		}
		busy, err := h.settleRound(pr, r, true)
		unlock()
		if err != nil {
			fmt.Fprintf(h.out, "claude-review tick: %s: %v\n", pr.URL(), err)
		}
		if busy {
			running++
		}
	}
	return running, nil
}

// settleRound reads one round of pr under its lock. busy is true while the
// round still runs or a retry started. With retry false a round that would
// be retried is marked read instead: the caller is about to start a new one.
func (h tickHost) settleRound(pr PR, r Round, retry bool) (busy bool, err error) {
	if h.running(pr.RoundSessionID()) {
		return true, nil
	}
	if h.blocked(pr.RoundSessionID()) {
		// No retry: it would resume on top of the live, waiting worker.
		d := Decision{Annotation: &Annotation{State: stateNeedsYou,
			Note: "the round is waiting for a permission it was not given", Links: []Link{attachLink(pr), ignoreLink(pr)}}}
		_, err := h.apply(pr, r, d, retry)
		return false, err
	}
	msg, err := h.lastMessage(pr.RoundSessionID())
	if err != nil {
		return false, fmt.Errorf("reading the round: %w", err)
	}
	facts, err := h.gh.RoundFacts(pr, r)
	if err != nil {
		return false, err // GitHub unreachable: read it next tick
	}
	return h.apply(pr, r, decide(r, ReadOutcome(msg), facts, pr), retry)
}

// settleUnread reads pr's unread rounds before its idle worker is stopped
// for a new round; any it cannot read stops the new round.
func (h tickHost) settleUnread(pr PR) error {
	rounds, err := h.ledger.UnreadRounds()
	if err != nil {
		return err
	}
	for _, r := range rounds {
		if r.PR != pr.URL() {
			continue
		}
		if busy, err := h.settleRound(pr, r, false); err != nil || busy {
			return errors.Join(err, errors.New("its last round still runs"))
		}
	}
	return nil
}

// apply carries out one decision; it reports whether a retry started.
// Without retry, a retry decision only marks the round read.
func (h tickHost) apply(pr PR, r Round, d Decision, retry bool) (bool, error) {
	if d.Retry && retry {
		started := false
		err := h.do("retry "+pr.URL(), func() error {
			var err error
			started, err = h.launch(pr, nil, func(at time.Time) error {
				if err := h.ledger.MarkRetried(r.ID); err != nil {
					return err
				}
				h.markRunningOrSay(pr, "tick (retry)", at)
				return nil
			})
			return err
		})
		return started || h.dry, err
	}
	if err := h.sideEffects(pr, d); err != nil {
		return false, err
	}
	if a := d.Annotation; a != nil {
		if d.MintFor != "" && !h.dry {
			tok, err := h.ledger.MintToken(pr, d.MintFor)
			if err != nil {
				return false, err
			}
			a.Links = append([]Link{{Label: "Approve", URL: approveURL(pr, d.MintFor, tok)}}, a.Links...)
		}
		if err := h.mark(pr, *a); err != nil {
			return false, err
		}
	}
	return false, h.do("mark round "+pr.URL()+" read", func() error { return h.ledger.MarkRead(r.ID) })
}

// sideEffects are the decision's writes besides the annotation: dismiss
// the user's approval, clear the PR, hold it back from rounds.
func (h tickHost) sideEffects(pr PR, d Decision) error {
	if !d.DismissReviewID.IsZero() {
		msg := "A later review round found new problems at the current head, so this approval no longer covers it."
		if err := h.do(fmt.Sprintf("dismiss review %s on %s", d.DismissReviewID, pr.URL()), func() error { return h.gh.Dismiss(pr, d.DismissReviewID, msg) }); err != nil {
			return err
		}
	}
	if d.Clear {
		if err := h.do("clear "+pr.URL(), func() error { return h.clear(pr) }); err != nil {
			return err
		}
	}
	if d.Ignore {
		return h.do("ignore "+pr.URL(), func() error { return h.ledger.Ignore(pr) })
	}
	return nil
}

// clearOrSay clears pr's annotation, reporting a failure without stopping.
func (h tickHost) clearOrSay(pr PR) {
	if err := h.do("clear "+pr.URL(), func() error { return h.clear(pr) }); err != nil {
		fmt.Fprintf(h.out, "claude-review tick: %s: %v\n", pr.URL(), err)
	}
}

func (h tickHost) clear(pr PR) error {
	if err := h.ledger.ClearAnnotation(pr); err != nil {
		return err
	}
	return h.elly.Delete(pr.URL())
}

// startOwed starts the rounds planTick picks, at most MaxRunning at once.
func (h tickHost) startOwed(prs []EllyPR, running int) error {
	plans, err := planTick(prs, h.tcfg, h.ledger, h.gh, h.now())
	if err != nil {
		return err
	}
	for _, p := range plans {
		gate, err := h.gateNote(p)
		if err != nil {
			return err
		}
		if gate != nil {
			if err := h.mark(p.PR, *gate); err != nil {
				return err
			}
			continue
		}
		started, err := h.startOne(p, running)
		if err != nil {
			fmt.Fprintf(h.out, "claude-review tick: %s: %v\n", p.PR.URL(), err)
		}
		if started {
			running++
		}
	}
	return nil
}

// startOne starts p's round under the PR's lock, unless a click holds it,
// a legacy window holds the session, a round runs, or no slot is free.
func (h tickHost) startOne(p Planned, running int) (bool, error) {
	unlock, err := h.lock(p.PR.SessionID().String())
	if errors.Is(err, errLocked) {
		if h.dry {
			fmt.Fprintf(h.out, "would leave %s: a click holds it\n", p.PR.URL())
		}
		return false, nil
	}
	if err != nil {
		return false, err
	}
	defer unlock()
	if h.sess.WindowOpen(p.PR.RoundSessionID()) {
		return false, h.markWindow(p.PR)
	}
	if h.running(p.PR.RoundSessionID()) {
		if h.dry {
			fmt.Fprintf(h.out, "would leave %s: its session is open\n", p.PR.URL())
		}
		return false, nil
	}
	if running >= h.tcfg.MaxRunning {
		return false, nil
	}
	started := false
	err = h.do(fmt.Sprintf("start a round on %s at %.9s (%s)", p.PR.URL(), p.Head, p.Trigger), func() error {
		var err error
		started, err = h.launch(p.PR, func() error { return h.settleUnread(p.PR) }, func(at time.Time) error {
			if _, err := h.ledger.StartRound(p.PR, p.Head, p.Trigger, at); err != nil {
				return err
			}
			h.markRunningOrSay(p.PR, "tick", at)
			return nil
		})
		return err
	})
	return started || h.dry, err
}

// markRunning shows pr's just-recorded round in elly at once, with an
// attach link, until settling it writes the outcome or roundLifetime after
// at runs out. by names who started it. mark never notifies for it.
func (h tickHost) markRunning(pr PR, by string, at time.Time) error {
	a := Annotation{State: stateRunning, Note: "started by " + by,
		Links: []Link{{Label: "Attach", URL: verbURL(pr, "attach")}}, Expires: at.Add(roundLifetime)}
	if err := h.mark(pr, a); err != nil || h.elly == nil {
		return err
	}
	return h.do("send "+pr.URL()+" to elly", func() error { return h.elly.Put(pr.URL(), a, a.Expires) })
}

// markRunningOrSay is markRunning for a round already started: a failure
// to show it is reported, never the round's failure.
func (h tickHost) markRunningOrSay(pr PR, by string, at time.Time) {
	if err := h.markRunning(pr, by, at); err != nil && h.out != nil {
		fmt.Fprintf(h.out, "claude-review tick: %s: %v\n", pr.URL(), err)
	}
}

// dropRunning clears pr's running row after a launch failed: the round it
// stood for is over, and no new one started.
func (h tickHost) dropRunning(pr PR) error {
	a, ok, err := h.ledger.Annotation(pr)
	if err != nil || !ok || a.State != stateRunning {
		return err
	}
	return h.do("clear "+pr.URL(), func() error {
		if err := h.ledger.ClearAnnotation(pr); err != nil || h.elly == nil {
			return err
		}
		return h.elly.Delete(pr.URL())
	})
}

func (h tickHost) markWindow(pr PR) error {
	return h.mark(pr, Annotation{State: stateNeedsYou, Note: windowNote, Links: []Link{ignoreLink(pr)}})
}

// mark sets pr's annotation, and tells the desktop when it newly needs the
// user: a state elly shows only to someone already looking is no signal.
// An unchanged state and note stay quiet, so a gate seen every tick
// notifies once; an Approve link's fresh token does not count as a change.
func (h tickHost) mark(pr PR, a Annotation) error {
	return h.do(fmt.Sprintf("mark %s %s: %s", pr.URL(), a.State, a.Note), func() error {
		was, had, err := h.ledger.Annotation(pr)
		if err != nil {
			return err
		}
		if err := h.ledger.SetAnnotation(pr, a); err != nil {
			return err
		}
		// A click builds its host without one: it tells the desktop itself.
		if h.notify != nil && needsUser(a.State) && (!had || was.State != a.State || was.Note != a.Note) {
			h.notify(pr.URL() + " needs you: " + a.Note)
		}
		return nil
	})
}

// gateNote is the annotation that holds p back, or nil when it may start.
func (h tickHost) gateNote(p Planned) (*Annotation, error) {
	switch p.Gate {
	case gateFreshLook:
		return &Annotation{State: stateNeedsYou, Note: "the PR changed under its first look (force-push or new base); click claude review to look again",
			Links: []Link{ignoreLink(p.PR)}}, nil
	case gatePaused:
		return &Annotation{State: stateNeedsYou, Note: "paused: the daily round limit for this PR is reached",
			Links: []Link{attachLink(p.PR), ignoreLink(p.PR)}}, nil
	}
	reason, err := h.ledger.Tripped()
	if err != nil || reason == "" {
		return nil, err
	}
	return &Annotation{State: stateNeedsYou, Note: "paused: " + reason + "; claude-review starts no round until it is reset",
		Links: []Link{attachLink(p.PR), ignoreLink(p.PR)}}, nil
}

// launch starts a round on pr in its clone and then records it with
// record, given the time taken before the launch. It is the only way tick
// starts a round, so both failsafes live here. Every caller runs it through
// do; the dry-run check makes a caller that forgets fail loudly instead of
// posting. The breaker trips at TotalDailyCap starts in 24 hours and stays
// tripped until the user resets it, so a loop that keeps starting rounds
// stops at the cap even after the window moves on. settle reads the PR's
// last round before its idle worker is stopped. The caller holds the lock.
func (h tickHost) launch(pr PR, settle func() error, record func(at time.Time) error) (started bool, err error) {
	if h.dry {
		return false, errors.New("refusing to start a round in a dry run")
	}
	defer func() {
		if err == nil {
			return
		}
		if dropErr := h.dropRunning(pr); dropErr != nil && h.out != nil {
			fmt.Fprintf(h.out, "claude-review tick: %s: %v\n", pr.URL(), dropErr)
		}
	}()
	reason, err := h.ledger.Tripped()
	if err != nil || reason != "" {
		return false, errors.Join(err, breakerError(reason))
	}
	n, err := h.ledger.Launches(h.now().Add(-24 * time.Hour))
	if err != nil {
		return false, err
	}
	if n >= h.tcfg.TotalDailyCap {
		reason = fmt.Sprintf("%d rounds started in 24 hours, the limit across all PRs", n)
		return false, errors.Join(h.ledger.Trip(reason), breakerError(reason))
	}
	dir, remote, err := h.findRepo(pr)
	if err != nil {
		return false, err
	}
	if err := h.trust(dir); err != nil {
		return false, err
	}
	at := h.now()
	res, err := h.starter().start(h.cfg, pr, dir, remote, true, settle)
	if err != nil && res.Kind != relaunchDuplicate {
		return false, err
	}
	return h.afterRelaunch(pr, res, err, func() error { return record(at) })
}

// starter starts rounds the way a click does, through h.start.
func (h tickHost) starter() roundStarter {
	return roundStarter{sess: h.sess, exists: h.exists, run: h.start, wrapper: h.tcfg.Wrapper}
}

// afterRelaunch turns Relaunch's outcome into ledger writes: the round is
// recorded only once its worker is listed and alone.
func (h tickHost) afterRelaunch(pr PR, res RelaunchResult, stopErr error, record func() error) (bool, error) {
	needsYou := func(note string) error {
		return h.mark(pr, Annotation{State: stateNeedsYou, Note: note, Links: []Link{ignoreLink(pr)}})
	}
	switch res.Kind {
	case relaunched:
		return true, record()
	case relaunchWindow:
		return false, h.markWindow(pr)
	case relaunchUnknown:
		return false, fmt.Errorf("nothing started: %s", res.Detail)
	case relaunchDuplicate:
		reason := pr.URL() + ": " + res.Detail
		return false, errors.Join(stopErr, needsYou(res.Detail+"; rounds are paused for every PR"), h.ledger.Trip(reason), breakerError(reason))
	case relaunchNotListed:
		return false, needsYou("the round was launched but " + res.Detail + "; nothing was recorded")
	case relaunchPolicy:
		return false, needsYou("nothing started: " + res.Detail)
	}
	return false, nil // busy: a round still runs there
}

// refreshedExpiry is when a re-sent a expires: annotationTTL from now, or
// its own expiry, never extended, so a round nothing settles stops showing
// as running. Zero means that has passed; send is false while too little of
// it is left for elly to accept, and elly's copy ends then anyway.
func (h tickHost) refreshedExpiry(a Annotation) (expires time.Time, send bool) {
	if a.Expires.IsZero() {
		return h.now().Add(annotationTTL), true
	}
	left := a.Expires.Sub(h.now())
	if left <= 0 {
		return time.Time{}, false
	}
	return a.Expires, left >= time.Minute
}

// refresh re-sends every live annotation with a fresh expiry, and drops one
// only on positive evidence: the PR is closed, or the user's own approval
// stands at its head. A PR missing from elly is not evidence.
func (h tickHost) refresh() error {
	all, err := h.ledger.Annotations()
	if err != nil {
		return err
	}
	for url, a := range all {
		pr, err := ParseTarget(url)
		if err != nil {
			continue
		}
		if a.Note == windowNote && !h.sess.WindowOpen(pr.RoundSessionID()) {
			h.clearOrSay(pr)
			continue
		}
		expires, send := h.refreshedExpiry(a)
		if expires.IsZero() {
			h.clearOrSay(pr)
			continue
		}
		if !send {
			continue
		}
		state, head, user, err := h.gh.Standing(pr)
		if err != nil {
			fmt.Fprintf(h.out, "claude-review tick: %s: %v\n", pr.URL(), err)
			continue
		}
		if state != prOpen || (user != nil && user.State == reviewApproved && user.CommitID == head) {
			h.clearOrSay(pr)
			continue
		}
		if err := h.do("send "+pr.URL()+" to elly", func() error { return h.elly.Put(url, a, expires) }); err != nil {
			fmt.Fprintf(h.out, "claude-review tick: %s: %v\n", pr.URL(), err)
		}
	}
	return nil
}
