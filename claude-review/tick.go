package main

import (
	"errors"
	"fmt"
	"io"
	"time"
)

// annotationTTL is how long elly shows an annotation tick wrote. Every tick
// re-sends the live ones, so a runner that stops shows its PRs as needing
// the user again within this time.
const annotationTTL = 30 * time.Minute

// tickElly is what tick needs from elly.
type tickElly interface {
	PRs() ([]EllyPR, error)
	Put(prURL string, a Annotation, expires time.Time) error
	Delete(prURL string) error
}

// tickGitHub is what tick needs from GitHub.
type tickGitHub interface {
	Gate
	RoundFacts(pr PR, r Round) (RoundFacts, error)
	Standing(pr PR) (state, head string, user *Review, err error)
	Dismiss(pr PR, id int64, message string) error
}

// tickHost is one tick's world. Every write goes through do, which only
// prints in a dry run.
type tickHost struct {
	cfg         Config
	tcfg        TickConfig
	ledger      *Ledger
	elly        tickElly
	gh          tickGitHub
	running     func(sessionID string) bool
	blocked     func(sessionID string) bool
	lastMessage func(sessionID string) (string, error)
	findRepo    func(pr PR) (dir, remote string, err error)
	start       func(dir string, argv []string) error
	now         func() time.Time
	out         io.Writer
	dry         bool
	// lock takes the PR's lock, the one a click holds, keyed by the PR's
	// SessionID whichever session it guards; errLocked means a click (or
	// the attach verb) has the PR, and tick leaves it this time.
	lock func(id string) (unlock func(), err error)
	// sess keeps one background worker per PR; every round starts there.
	sess Sessions
	// exists reports a transcript for a session id.
	exists func(id string) bool
	// trust marks a clone trusted for Claude Code before a launch in it.
	trust func(dir string) error
	// version is `claude --version`; selfCheck checks a new one, and notify
	// tells the user a check failed.
	version   func() (string, error)
	selfCheck func() error
	notify    func(msg string)
}

// windowNote is the needs_you note for a PR whose session is open in a
// legacy interactive window: tick cannot start a round beside it.
const windowNote = "review window open; close it to let rounds run"

func (h tickHost) do(what string, fn func() error) error {
	if h.dry {
		fmt.Fprintln(h.out, "would", what)
		return nil
	}
	return fn()
}

// tick is one pass: read elly, settle finished rounds, start owed ones,
// refresh annotations. Without elly it does nothing at all; the annotations
// already written expire on their own.
func (h tickHost) tick() error {
	prs, err := h.elly.PRs()
	if err != nil {
		return err
	}
	if err := h.checkClaude(); err != nil {
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
	// A failed run is what the timer's journal shows.
	if reason, err := h.ledger.Tripped(); err != nil || reason != "" {
		return errors.Join(err, breakerError(reason))
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
		unlock, err := h.lock(pr.SessionID())
		if err != nil {
			if !errors.Is(err, errLocked) {
				fmt.Fprintf(h.out, "claude-review tick: %s: %v\n", pr.Slug(), err)
			}
			running++
			continue
		}
		busy, err := h.settleRound(pr, r, true)
		unlock()
		if err != nil {
			fmt.Fprintf(h.out, "claude-review tick: %s: %v\n", pr.Slug(), err)
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
		err := h.do("retry "+pr.Slug(), func() error {
			var err error
			started, err = h.launch(pr, nil, func(time.Time) error { return h.ledger.MarkRetried(r.ID) })
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
		if err := h.do(fmt.Sprintf("mark %s %s: %s", pr.Slug(), a.State, a.Note), func() error { return h.ledger.SetAnnotation(pr, *a) }); err != nil {
			return false, err
		}
	}
	return false, h.do("mark round "+pr.Slug()+" read", func() error { return h.ledger.MarkRead(r.ID) })
}

// sideEffects are the decision's writes besides the annotation: dismiss
// the user's approval, clear the PR, hold it back from rounds.
func (h tickHost) sideEffects(pr PR, d Decision) error {
	if d.DismissReviewID != 0 {
		msg := "A later review round found new problems at the current head, so this approval no longer covers it."
		if err := h.do(fmt.Sprintf("dismiss review %d on %s", d.DismissReviewID, pr.Slug()), func() error { return h.gh.Dismiss(pr, d.DismissReviewID, msg) }); err != nil {
			return err
		}
	}
	if d.Clear {
		if err := h.do("clear "+pr.Slug(), func() error { return h.clear(pr) }); err != nil {
			return err
		}
	}
	if d.Ignore {
		return h.do("ignore "+pr.Slug(), func() error { return h.ledger.Ignore(pr) })
	}
	return nil
}

// clearOrSay clears pr's annotation, reporting a failure without stopping.
func (h tickHost) clearOrSay(pr PR) {
	if err := h.do("clear "+pr.Slug(), func() error { return h.clear(pr) }); err != nil {
		fmt.Fprintf(h.out, "claude-review tick: %s: %v\n", pr.Slug(), err)
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
			if err := h.do(fmt.Sprintf("mark %s %s: %s", p.PR.Slug(), gate.State, gate.Note), func() error { return h.ledger.SetAnnotation(p.PR, *gate) }); err != nil {
				return err
			}
			continue
		}
		started, err := h.startOne(p, running)
		if err != nil {
			fmt.Fprintf(h.out, "claude-review tick: %s: %v\n", p.PR.Slug(), err)
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
	unlock, err := h.lock(p.PR.SessionID())
	if errors.Is(err, errLocked) {
		if h.dry {
			fmt.Fprintf(h.out, "would leave %s: a click holds it\n", p.PR.Slug())
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
			fmt.Fprintf(h.out, "would leave %s: its session is open\n", p.PR.Slug())
		}
		return false, nil
	}
	if running >= h.tcfg.MaxRunning {
		return false, nil
	}
	started := false
	err = h.do(fmt.Sprintf("start a round on %s at %.9s (%s)", p.PR.Slug(), p.Head, p.Trigger), func() error {
		var err error
		started, err = h.launch(p.PR, func() error { return h.settleUnread(p.PR) }, func(at time.Time) error {
			_, err := h.ledger.StartRound(p.PR, p.Head, p.Trigger, at)
			return err
		})
		return err
	})
	return started || h.dry, err
}

func (h tickHost) markWindow(pr PR) error {
	a := Annotation{State: stateNeedsYou, Note: windowNote, Links: []Link{ignoreLink(pr)}}
	return h.do(fmt.Sprintf("mark %s %s: %s", pr.Slug(), a.State, a.Note), func() error { return h.ledger.SetAnnotation(pr, a) })
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
func (h tickHost) launch(pr PR, settle func() error, record func(at time.Time) error) (bool, error) {
	if h.dry {
		return false, errors.New("refusing to start a round in a dry run")
	}
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
		return h.ledger.SetAnnotation(pr, Annotation{State: stateNeedsYou, Note: note, Links: []Link{ignoreLink(pr)}})
	}
	switch res.Kind {
	case relaunched:
		return true, record()
	case relaunchWindow:
		return false, h.markWindow(pr)
	case relaunchUnknown:
		return false, fmt.Errorf("nothing started: %s", res.Detail)
	case relaunchDuplicate:
		reason := pr.Slug() + ": " + res.Detail
		return false, errors.Join(stopErr, needsYou(res.Detail+"; rounds are paused for every PR"), h.ledger.Trip(reason), breakerError(reason))
	case relaunchNotListed:
		return false, needsYou("the round was launched but " + res.Detail + "; nothing was recorded")
	case relaunchPolicy:
		return false, needsYou("nothing started: " + res.Detail)
	}
	return false, nil // busy: a round still runs there
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
		state, head, user, err := h.gh.Standing(pr)
		if err != nil {
			fmt.Fprintf(h.out, "claude-review tick: %s: %v\n", pr.Slug(), err)
			continue
		}
		if state != prOpen || (user != nil && user.State == reviewApproved && user.CommitID == head) {
			h.clearOrSay(pr)
			continue
		}
		if err := h.do("send "+pr.Slug()+" to elly", func() error { return h.elly.Put(url, a, h.now().Add(annotationTTL)) }); err != nil {
			fmt.Fprintf(h.out, "claude-review tick: %s: %v\n", pr.Slug(), err)
		}
	}
	return nil
}
