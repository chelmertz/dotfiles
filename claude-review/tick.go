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
	// RepliedInRoundThread: the user replied after settled in a review
	// thread holding a signed comment from on or after started.
	RepliedInRoundThread(pr PR, started, settled time.Time) (bool, error)
	Dismiss(pr PR, id ids.ReviewID, message string) error
	DismissalStatus(pr PR, id ids.ReviewID) (DismissalStatus, error)
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
	blocked     func(sessionID ids.SessionID) (why string, ok bool)
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
	// say reports a click's non-fatal failures (warn); tick's go to out.
	say func(error)
	// sessionIndex maps each PR to the session that last acted on it.
	sessionIndex func() (map[string]SessionHit, error)
}

// windowNote is the needs_you note for a PR whose session is open in a
// legacy interactive window: tick cannot start a round beside it.
const windowNote = "Claude window open; close it to let reviews run"

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

// quiet is do for a write every tick repeats (a row refresh, a session
// link): a dry run still says it, a real run does not log it, since one
// line per PR every few minutes is most of the journal and its CPU.
func (h tickHost) quiet(what string, fn func() error) error {
	if h.dry {
		return h.do(what, fn)
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
	for _, step := range []func() error{h.sweep, h.settleDismissals, h.healBreaker} {
		if err := step(); err != nil {
			return err
		}
	}
	if n := h.ledger.Unparsed; n > 0 && h.out != nil {
		fmt.Fprintf(h.out, "claude-review tick: %d recorded rounds have a trigger no parser reads; they count toward the caps\n", n)
	}
	running, err := h.settle()
	if err != nil {
		return err
	}
	for _, step := range []func() error{h.recoverStarting, h.recheckLaunches} {
		if err := step(); err != nil {
			return err
		}
	}
	held, err := h.startOwed(prs, running)
	if err != nil {
		return err
	}
	if err := h.refresh(held, prs); err != nil {
		return err
	}
	h.offerStarts(prs)
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
		return errors.Join(h.ledger.Trip(breakerSelfCheck+reason), breakerError(reason))
	}
	return h.ledger.SetMeta(metaClaudeVersion, v)
}

func breakerError(reason string) error {
	if reason == "" {
		return nil
	}
	if healable(reason) {
		return fmt.Errorf("no round starts: %s; it clears itself once fewer than the limit started in the last 24 hours", reason)
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
	_, err = h.move(pr, Event{Kind: evClosed})
	if err != nil {
		return err
	}
	for _, r := range rounds {
		if err := h.do("mark round "+pr.URL()+" read", func() error { return h.ledger.MarkRead(r.ID) }); err != nil {
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
// be retried, or waits on the user, is read instead: the caller is about to
// start a new one.
func (h tickHost) settleRound(pr PR, r Round, retry bool) (busy bool, err error) {
	sid := r.SessionFor(pr)
	if h.running(sid) {
		_, err := h.move(pr, Event{Kind: evResumed})
		return true, err
	}
	if why, ok := h.blocked(sid); ok {
		return false, h.settleBlocked(pr, r, why, retry)
	}
	msg, err := h.lastMessage(sid)
	if err != nil {
		return false, fmt.Errorf("reading the round: %w", err)
	}
	facts, err := h.gh.RoundFacts(pr, r)
	if err != nil {
		return false, err // GitHub unreachable: read it next tick
	}
	if facts.DismissalPending, err = h.ledger.DismissalPending(pr); err != nil {
		return false, err
	}
	return h.apply(pr, r, decide(r, ReadOutcome(msg), facts, pr), retry)
}

// settleBlocked reads a round whose worker waits for the user: the question
// row, answered in the session or in a fork with prod data. The round stays
// unread, so the worker's resuming and ending are seen. A push since the
// round started overtakes the question: the worker is stopped and the round
// read, so the new head gets a round of its own, which asks again if the
// question still holds.
func (h tickHost) settleBlocked(pr PR, r Round, why string, retry bool) error {
	_, head, _, err := h.gh.Standing(pr)
	if err != nil {
		return err // GitHub unreachable: read it next tick
	}
	if _, err := h.move(pr, Event{Kind: evBlocked, Detail: capRunes(why, maxQuestionNote)}); err != nil {
		return err
	}
	switch {
	case head != "" && head != r.Head:
		_, err = h.move(pr, Event{Kind: evPush, Head: head})
	case !retry:
		_, err = h.move(pr, Event{Kind: evEnded, Next: &PRState{State: stIdle}})
	default:
		return nil
	}
	if err != nil {
		return err
	}
	return h.do("mark round "+pr.URL()+" read", func() error { return h.ledger.MarkRead(r.ID) })
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
// Without retry, a retry decision only reads the round.
func (h tickHost) apply(pr PR, r Round, d Decision, retry bool) (bool, error) {
	if d.Clear {
		if _, err := h.move(pr, Event{Kind: evClosed}); err != nil {
			return false, err
		}
		return false, h.do("mark round "+pr.URL()+" read", func() error { return h.ledger.MarkRead(r.ID) })
	}
	if d.Retry && retry {
		return h.retry(pr, r, d)
	}
	if d.Retry {
		d.Next = &PRState{State: stIdle}
	}
	if !d.DismissReviewID.IsZero() {
		// The record first: a crash or a refusal from here on leaves it for
		// the next tick, which settles it only on reading the dismissal back.
		if err := h.do(fmt.Sprintf("record the dismissal of review %s on %s", d.DismissReviewID, pr.URL()), func() error {
			return errors.Join(h.ledger.AddDismissal(pr, d.DismissReviewID, h.now()), h.ledger.RevokeTokens(pr))
		}); err != nil {
			return false, err
		}
		h.sendDismissal(Dismissal{PR: pr.URL(), Review: d.DismissReviewID})
	}
	if _, err := h.move(pr, Event{Kind: evEnded, Next: d.Next}); err != nil {
		return false, err
	}
	return false, h.do("mark round "+pr.URL()+" read", func() error { return h.ledger.MarkRead(r.ID) })
}

// retry resumes r's session for a round cut off before its summary, and
// reports whether it started.
func (h tickHost) retry(pr PR, r Round, d Decision) (bool, error) {
	var not skip
	failed := func(detail string) error {
		_, err := h.move(pr, Event{Kind: evEnded, Next: &PRState{State: stHeld, Kind: kindLaunch, Detail: detail}})
		return err
	}
	err := h.do("retry "+pr.URL(), func() error {
		var err error
		not, err = h.launch(pr, nil, func(time.Time) error {
			if err := h.ledger.MarkRetried(r.ID); err != nil {
				return err
			}
			_, err := h.move(pr, Event{Kind: evEnded, Next: d.Next})
			return err
		}, failed)
		return err
	})
	if err != nil && !h.dry {
		// the round it retried is over and none replaced it
		class := launchNotStarted
		if errors.Is(err, errRosterUnknown) {
			class = launchUnknown
		}
		err = errors.Join(err, failed(class))
	}
	if not != "" {
		h.leave(pr, not)
	}
	return (err == nil && not == "") || h.dry, err
}

// offerTTL outlives one 5-minute tick, so a standing offer never blinks,
// and lets one that stopped holding lapse within two.
const offerTTL = 11 * time.Minute

// offersStart is whether elly's row for p gets a Start: a review asked of
// the user by name in review_repos that only a click would start (no
// acceptance, not the user's own), and nothing claude-review
// already shows there. A click is the user's act; the offer only puts the
// link on the page, beside the mail and the GitHub button.
func offersStart(p EllyPR, cfg TickConfig, accepted, annotated bool) bool {
	pr, err := ParseTarget(p.Url)
	if err != nil || accepted || annotated || p.Buried || strings.EqualFold(p.Author, cfg.User) {
		return false
	}
	asked := p.ReviewRequestedOfUser || slices.ContainsFunc(append(append([]string{}, p.ReviewRequestedFromUsers...), p.RereviewFrom...), func(u string) bool { return strings.EqualFold(u, cfg.User) })
	return asked && repoAllowed(pr.Owner, pr.Repo, cfg.ReviewRepos)
}

// startOffer is the info annotation that is Start on elly's row. It lives
// in elly only: the ledger's rows are round states, and the first one a
// click writes ("starting") replaces it.
func startOffer(pr PR) Annotation {
	return Annotation{State: stateInfo, Links: []Link{{Label: "Start", URL: "claude-review://" + pr.Owner + "/" + pr.Repo + "/" + itoaPR(pr) + "?from=elly", Primary: true}}}
}

// offerStarts sends Start where offersStart holds, each under the PR's
// lock so it never lands on a click's "starting". Failures are said, never
// the tick's.
func (h tickHost) offerStarts(prs []EllyPR) {
	for _, p := range prs {
		pr, err := ParseTarget(p.Url)
		if err != nil {
			continue
		}
		_, accepted, err1 := h.ledger.Acceptance(pr)
		_, annotated, err2 := h.ledger.PRState(pr)
		if err := errors.Join(err1, err2); err != nil {
			fmt.Fprintf(h.out, "claude-review tick: %s: %v\n", pr.URL(), err)
			continue
		}
		if !offersStart(p, h.tcfg, accepted, annotated) {
			continue
		}
		if h.dry {
			fmt.Fprintf(h.out, "would offer Start on %s\n", pr.URL())
			continue
		}
		unlock, err := h.lock(pr.SessionID().String())
		if err != nil {
			continue // a click has it, and writes its own row
		}
		if err := h.elly.Put(pr.URL(), startOffer(pr), h.now().Add(offerTTL)); err != nil {
			fmt.Fprintf(h.out, "claude-review tick: %s: offering Start: %v\n", pr.URL(), err)
		}
		unlock()
	}
}

// clearOrSay clears pr's annotation, reporting a failure without stopping.
func (h tickHost) clearOrSay(pr PR) {
	if err := h.do("clear "+pr.URL(), func() error { return h.clear(pr) }); err != nil {
		fmt.Fprintf(h.out, "claude-review tick: %s: %v\n", pr.URL(), err)
	}
}

func (h tickHost) clear(pr PR) error {
	if err := h.ledger.ClearAnnotation(pr); err != nil || h.elly == nil {
		return err
	}
	return h.elly.Delete(pr.URL())
}

// startOwed starts the rounds planTick picks, at most MaxRunning at once,
// and says in the journal why it leaves each other PR. held maps each PR
// whose owed round the daily cap or the breaker holds back to the note its
// row shows this tick; that is never stored.
func (h tickHost) startOwed(prs []EllyPR, running int) (held map[string]string, err error) {
	plans, err := planTick(prs, h.tcfg, h.ledger, h.gh, h.now())
	if err != nil {
		return nil, err
	}
	held = map[string]string{}
	for _, p := range plans {
		if p, err = h.answerOwes(p); err != nil {
			fmt.Fprintf(h.out, "claude-review tick: %s: %v\n", p.PR.URL(), err)
			continue
		}
		if p.Skip != "" {
			h.leave(p.PR, p.Skip)
			continue
		}
		holds, err := h.hold(p, held)
		if err != nil {
			return nil, err
		}
		if holds {
			continue
		}
		s, err := h.startOne(p, running)
		if err != nil {
			fmt.Fprintf(h.out, "claude-review tick: %s: %v\n", p.PR.URL(), err)
		}
		if s != "" {
			h.leave(p.PR, s)
		} else if err == nil {
			running++
		}
	}
	return held, nil
}

// leave writes why tick leaves pr alone, "would leave" in a dry run.
func (h tickHost) leave(pr PR, s skip) {
	// The two answers nearly every PR gets every tick are left out of a
	// real run's journal; a dry run still lists them.
	if h.out == nil || (!h.dry && (s == skipNotAccepted || s == skipNothingOwed)) {
		return
	}
	verb := "leave"
	if h.dry {
		verb = "would leave"
	}
	fmt.Fprintf(h.out, "%s %s: %s\n", verb, pr.URL(), s)
}

// hold keeps p's owed round back when the PR changed under its first look,
// the daily cap or the breaker says so, and reports whether it did.
func (h tickHost) hold(p Planned, held map[string]string) (bool, error) {
	note, err := h.pausedNote(p)
	if err != nil {
		return false, err
	}
	if p.Gate == gateFreshLook {
		note = "the PR changed under its first look"
		if _, err := h.move(p.PR, Event{Kind: evOwed, Head: p.Head, FreshLook: true}); err != nil {
			return false, err
		}
	} else if note != "" {
		held[p.PR.URL()] = note
	}
	if note != "" && h.out != nil {
		fmt.Fprintf(h.out, "hold %s: %s\n", p.PR.URL(), note)
	}
	return note != "", nil
}

// owedEvent is the event p's round reports; a request lifts stuck.
func owedEvent(p Planned) Event {
	return Event{Kind: evOwed, Head: p.Head, Trigger: p.Trigger}
}

// startOne starts p's round under the PR's lock, or names what holds it
// back: a click holding it, a legacy window on the session, a running
// round, a state no owed round moves, or no free slot. "" with no error is
// a start.
func (h tickHost) startOne(p Planned, running int) (skip, error) {
	unlock, err := h.lock(p.PR.SessionID().String())
	if errors.Is(err, errLocked) {
		return skipClickHolds, nil
	}
	if err != nil {
		return "", err
	}
	defer unlock()
	if windowOpen(h.sess, h.ledger, p.PR) {
		_, err := h.move(p.PR, Event{Kind: evWindowOpened})
		return skipWindowOpen, err
	}
	if h.running(p.PR.RoundSessionID()) {
		return skipRunning, nil
	}
	s, err := h.stateOf(p.PR)
	if err != nil {
		return "", err
	}
	if _, _, ok := transition(s, owedEvent(p), h.now()); !ok {
		return skip("its state is " + describe(s) + ", which no owed round moves"), nil
	}
	if running >= h.tcfg.MaxRunning {
		return skipNoSlot, nil
	}
	var not skip
	err = h.do(fmt.Sprintf("start a round on %s at %.9s (%s)", p.PR.URL(), p.Head, p.Trigger.Text()), func() error {
		var err error
		not, err = h.launch(p.PR, func() error { return h.settleUnread(p.PR) }, func(at time.Time) error {
			if _, err := h.ledger.StartRound(p.PR, p.Head, p.Trigger, at); err != nil {
				return err
			}
			_, err := h.move(p.PR, owedEvent(p))
			return err
		}, func(detail string) error {
			_, err := h.move(p.PR, Event{Kind: evNotStarted, Detail: detail})
			return err
		})
		return err
	})
	return not, err
}

// startingLifetime bounds a "starting" row a crashed click never cleared.
const startingLifetime = 2 * time.Minute

// setRow stores pr's row and tells the desktop when it newly needs the
// user: a state elly shows only to someone already looking is no signal.
// An unchanged state and note stay quiet, so a row seen every tick
// notifies once; an Approve link's fresh token does not count as a change.
func (h tickHost) setRow(pr PR, a Annotation) error {
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
}

// pausedNote is the row of a PR whose owed round the daily cap or the
// breaker holds back, "" when neither does.
func (h tickHost) pausedNote(p Planned) (string, error) {
	if p.Gate == gatePaused {
		return "paused: the daily review limit for this PR is reached", nil
	}
	reason, err := h.ledger.Tripped()
	if err != nil || reason == "" {
		return "", err
	}
	if healable(reason) {
		return "paused: " + breakerWords(reason) + "; claude-review starts again once fewer than the limit started in the last 24 hours", nil
	}
	return "paused: " + breakerWords(reason) + "; claude-review starts no round until it is reset", nil
}

// errRosterUnknown marks a launch whose session list could not be read.
var errRosterUnknown = errors.New("the session list could not be read")

// Breaker reasons start with their kind, so a note can name the kind in
// fixed words and never carry the stored text (session ids, a self-check's
// output). The full reason stays in the ledger and in tick's own output.
const (
	breakerCap       = "cap: "
	breakerDuplicate = "duplicate: "
	breakerSelfCheck = "self-check: "
	breakerLoop      = "loop: "
)

// healable: a cap trip, which clears itself (healBreaker). A duplicate, a
// failed self-check or a cap tripped twice in a week waits for a reset.
func healable(reason string) bool {
	return strings.HasPrefix(reason, breakerCap) || strings.HasSuffix(reason, "the limit across all PRs") && !strings.HasPrefix(reason, breakerLoop)
}

// metaBreakerHealed is when healBreaker last cleared a cap trip.
const metaBreakerHealed = "breaker-healed-at"

// healLatchWindow: a cap trip this soon after a self-clear latches.
const healLatchWindow = 7 * 24 * time.Hour

// healBreaker clears a cap trip once fewer than the cap's rounds started in
// the last 24 hours. The cap still bounds a loop to TotalDailyCap rounds a
// day; a second trip within healLatchWindow of a clear latches it, said
// once, so a loop does not spend the cap every day unseen.
func (h tickHost) healBreaker() error {
	reason, err := h.ledger.Tripped()
	if err != nil || !healable(reason) {
		return err
	}
	n, err := h.ledger.Launches(h.now().Add(-24 * time.Hour))
	if err != nil || n >= h.tcfg.TotalDailyCap {
		return err
	}
	last, err := h.ledger.Meta(metaBreakerHealed)
	if err != nil {
		return err
	}
	if at, perr := time.Parse(time.RFC3339, last); perr == nil && h.now().Sub(at) < healLatchWindow {
		return h.do("latch the breaker: the cap tripped twice in a week", func() error {
			if h.notify != nil {
				h.notify("claude-review: the round limit across all PRs tripped twice in a week; rounds stay paused until `claude-review tick --reset`")
			}
			return h.ledger.Trip(breakerLoop + strings.TrimPrefix(reason, breakerCap))
		})
	}
	return h.do("clear the breaker: fewer than the limit started in the last 24 hours", func() error {
		return errors.Join(h.ledger.Reset(h.now()), h.ledger.SetMeta(metaBreakerHealed, h.now().UTC().Format(time.RFC3339)))
	})
}

// A reason stored before the kinds existed is matched by its old cap text.
func breakerWords(reason string) string {
	switch {
	case strings.HasPrefix(reason, breakerCap), strings.HasSuffix(reason, "the limit across all PRs"):
		return "too many rounds started in 24 hours, the limit across all PRs"
	case strings.HasPrefix(reason, breakerDuplicate):
		return "two workers held one session"
	case strings.HasPrefix(reason, breakerLoop):
		return "the limit across all PRs was reached twice in a week"
	case strings.HasPrefix(reason, breakerSelfCheck):
		return "the self-check on a new Claude Code failed"
	}
	return "the breaker tripped"
}

// launch starts a round on pr in its clone and then records it with
// record, given the time taken before the launch. It is the only way tick
// starts a round, so both failsafes live here. Every caller runs it through
// do; the dry-run check makes a caller that forgets fail loudly instead of
// posting. The breaker trips at TotalDailyCap starts in 24 hours; healBreaker
// clears a cap trip once, and a second one latches, so a loop that keeps starting rounds
// stops at the cap even after the window moves on. settle reads the PR's
// last round before its idle worker is stopped. The caller holds the lock.
func (h tickHost) launch(pr PR, settle func() error, record func(at time.Time) error, failed func(detail string) error) (notStarted skip, err error) {
	if h.dry {
		return "", errors.New("refusing to start a round in a dry run")
	}
	reason, err := h.ledger.Tripped()
	if err != nil || reason != "" {
		return "", errors.Join(err, breakerError(reason))
	}
	n, err := h.ledger.Launches(h.now().Add(-24 * time.Hour))
	if err != nil {
		return "", err
	}
	if n >= h.tcfg.TotalDailyCap {
		reason = fmt.Sprintf("%d rounds started in 24 hours, the limit across all PRs", n)
		return "", errors.Join(h.ledger.Trip(breakerCap+reason), breakerError(reason))
	}
	dir, remote, err := h.findRepo(pr)
	if err != nil {
		return "", err
	}
	if err := h.trust(dir); err != nil {
		return "", err
	}
	at := h.now()
	res, err := h.starter().start(h.cfg, pr, dir, remote, true, settle)
	if err != nil && res.Kind != relaunchDuplicate {
		return "", err
	}
	return h.afterRelaunch(pr, res, err, func() error { return record(at) }, failed)
}

// starter starts rounds the way a click does, through h.start.
func (h tickHost) starter() roundStarter {
	return roundStarter{sess: h.sess, exists: h.exists, run: h.start, wrapper: h.tcfg.Wrapper}
}

// afterRelaunch turns Relaunch's outcome into ledger writes: the round is
// recorded only once its worker is listed and alone; failed records why
// none started. notStarted names why no round started; "" with no error is
// a start.
func (h tickHost) afterRelaunch(pr PR, res RelaunchResult, stopErr error, record func() error, failed func(detail string) error) (notStarted skip, err error) {
	switch res.Kind {
	case relaunched:
		return "", record()
	case relaunchWindow:
		_, err := h.move(pr, Event{Kind: evWindowOpened})
		return skipWindowOpen, err
	case relaunchUnknown:
		return "", fmt.Errorf("nothing started: %s: %w", res.Detail, errRosterUnknown)
	case relaunchDuplicate:
		reason := pr.URL() + ": " + res.Detail
		err := failed(launchDuplicate)
		return skip(res.Detail + "; rounds are paused for every PR"), errors.Join(stopErr, err, h.ledger.Trip(breakerDuplicate+reason), breakerError(reason))
	case relaunchNotListed:
		err := failed(launchUnlisted)
		return skip("the round was launched but " + res.Detail + "; nothing was recorded"), err
	case relaunchPolicy:
		err := failed(launchNotStarted)
		return skip("nothing started: " + res.Detail), err
	}
	return skipBusy, nil
}

// answered: a round's question is the user's until the user replies, after
// the round settled, in a review thread the round posted in. Any other post
// on the PR (a review of someone else's thread, a PR comment) leaves it.
// The signal lives on GitHub, where the author sees it too; tick only reads
// it.
// answerOwes turns a question the user answered on GitHub into an owed
// round. elly cannot report it: rounds post as the user, so a reply in the
// round's thread leaves no thread someone else answered last.
func (h tickHost) answerOwes(p Planned) (Planned, error) {
	if p.Skip != skipNothingOwed {
		return p, nil
	}
	s, err := h.stateOf(p.PR)
	if err != nil || s.State != stAwaiting || s.Kind != kindQuestion {
		return p, err
	}
	answered, err := h.answered(p.PR)
	if err != nil || !answered {
		return p, err
	}
	acc, _, err := h.ledger.Acceptance(p.PR)
	if err != nil {
		return p, err
	}
	open, gate, err := gateFor(p.PR, acc, p.Head, h.tcfg, h.ledger, h.gh, h.now())
	if err != nil {
		return p, err
	}
	p.Skip, p.Trigger, p.Gate = "", Answered{At: h.now().UTC()}, gate
	if !open {
		p.Skip = skipClosed
	}
	return p, nil
}

func (h tickHost) answered(pr PR) (bool, error) {
	started, settled, ok, err := h.ledger.LastSettled(pr)
	if err != nil || !ok {
		return false, err
	}
	return h.gh.RepliedInRoundThread(pr, started, settled)
}

// refreshedExpiry is when a re-sent a expires: rowTTL from now, or
// its own expiry, never extended, so a round nothing settles stops showing
// as running. Zero means that has passed; send is false while too little of
// it is left for elly to accept, and elly's copy ends then anyway.
func (h tickHost) refreshedExpiry(a Annotation) (expires time.Time, send bool) {
	if a.Expires.IsZero() {
		return h.now().Add(rowTTL(h.now())), true
	}
	left := a.Expires.Sub(h.now())
	if left <= 0 {
		return time.Time{}, false
	}
	return a.Expires, left >= time.Minute
}

// refresh re-renders every PR's row with a fresh expiry, and moves a state
// only on positive evidence: the PR is closed, the user's own approval
// stands at its head, a window closed. An answered question moves only
// by the round answerOwes starts, so a held launch keeps the question. A PR
// missing from elly is not evidence. held is startOwed's paused rows.
func (h tickHost) refresh(held map[string]string, prs []EllyPR) error {
	updated := map[string]time.Time{}
	for _, p := range prs {
		updated[p.Url] = p.LastUpdated
	}
	states, err := h.ledger.PRStates()
	if err != nil {
		return err
	}
	rows, err := h.ledger.Annotations()
	if err != nil {
		return err
	}
	for url := range rows {
		if _, ok := states[url]; !ok {
			if pr, err := ParseTarget(url); err == nil {
				h.clearOrSay(pr) // a row no state stands behind
			}
		}
	}
	for url := range held {
		if _, ok := states[url]; !ok {
			states[url] = PRState{State: stIdle} // accepted before any round
		}
	}
	for url, s := range states {
		pr, err := ParseTarget(url)
		if err != nil {
			continue
		}
		// Every move runs under the PR's lock; a click holding it is about
		// to write its own state, and the row is refreshed next pass.
		unlock, err := h.lock(pr.SessionID().String())
		if errors.Is(err, errLocked) {
			continue
		}
		if err != nil {
			fmt.Fprintf(h.out, "claude-review tick: %s: %v\n", pr.URL(), err)
			continue
		}
		h.refreshOne(pr, s, held[url], updated[url])
		unlock()
	}
	return nil
}

// refreshOne moves pr's state s on evidence and sends its row; the caller
// holds the PR's lock.
func (h tickHost) refreshOne(pr PR, s PRState, paused string, updated time.Time) {
	s, err := h.evidence(pr, s, updated)
	if err != nil {
		fmt.Fprintf(h.out, "claude-review tick: %s: %v\n", pr.URL(), err)
		return
	}
	if s.State == stGone {
		return
	}
	if err := h.quiet("send "+pr.URL()+" to elly", func() error { return h.show(pr, s, paused) }); err != nil {
		fmt.Fprintf(h.out, "claude-review tick: %s: %v\n", pr.URL(), err)
	}
}

// evidence moves s on what GitHub and the desktop say, and returns the
// state after. An idle PR is not asked about: it has no row.
// updated is the PR's last update as elly saw it, zero when elly does not
// list it.
func (h tickHost) evidence(pr PR, s PRState, updated time.Time) (PRState, error) {
	if (s.State == stHeld && s.Kind == kindWindow || windowBelow(s)) && !windowOpen(h.sess, h.ledger, pr) {
		// The head, so the restore drops what was judged at an older one;
		// unreadable, the restore stands as before.
		_, head, _, _ := h.gh.Standing(pr)
		next, err := h.move(pr, Event{Kind: evWindowClosed, Head: head})
		if err != nil {
			return s, err
		}
		s = next
	}
	if s.State == stIdle {
		return s, nil
	}
	// A close or an approval updates the PR, so GitHub has nothing new to
	// say about one elly saw unchanged since its last quiet answer. Each
	// ask is two gh calls, for every PR with a row, every tick.
	key, seen := metaStandingPrefix+pr.URL(), ""
	if !updated.IsZero() {
		seen = updated.UTC().Format(time.RFC3339Nano)
		if last, err := h.ledger.Meta(key); err == nil && last == seen {
			return s, nil
		}
	}
	e, err := h.onGitHub(pr)
	if err != nil {
		return s, err
	}
	if e == nil {
		if seen != "" && !h.dry {
			h.warn(pr, h.ledger.SetMeta(key, seen))
		}
		return s, nil
	}
	return h.move(pr, *e)
}

// metaStandingPrefix keys the PR update time GitHub last had nothing new
// for.
const metaStandingPrefix = "standing-seen "

// onGitHub is the event GitHub holds for pr, nil for none.
func (h tickHost) onGitHub(pr PR) (*Event, error) {
	state, head, user, err := h.gh.Standing(pr)
	if err != nil {
		return nil, err
	}
	switch {
	case state != prOpen:
		return &Event{Kind: evClosed}, nil
	case user != nil && user.State == reviewApproved && user.CommitID == head:
		if being, err := h.beingDismissed(pr, user.ID); err != nil || being {
			// Still on GitHub only because its dismissal has not landed.
			return nil, err
		}
		return &Event{Kind: evUserApproved, Head: head}, nil
	}
	return nil, nil
}
