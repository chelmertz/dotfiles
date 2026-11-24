package main

import (
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/chelmertz/dotfiles/claude-review/ids"
	"github.com/chelmertz/dotfiles/claude-review/prlock"
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
	lastMessage func(sessionID ids.SessionID, from int64) (string, error)
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
	// transcriptSize is a session transcript's byte size, measured just
	// before each launch as where the round's own lines start: lastMessage
	// reads from there.
	transcriptSize func(id ids.SessionID) int64
	// trust marks a clone trusted for Claude Code before a launch in it.
	trust func(dir string) error
	// version is `claude --version`; selfCheck checks a new one, and notify
	// tells the user a check failed.
	version   func() (string, error)
	selfCheck func() error
	notify    func(msg string)
	// say reports a click's non-fatal failures (warn); tick's go to out.
	say func(error)
	// origin is what the decision log names as deciding this host's steps:
	// originTick when empty.
	origin string
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
	// Before the sweep, so a closed PR the migration gives a state is
	// closed again in this pass.
	if err := h.migrateStates(); err != nil {
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
	if err := h.plan(prs, running); err != nil {
		return err
	}
	h.linkSessions(prs)
	// A failed run is what the timer's journal shows.
	if reason, err := h.ledger.Tripped(); err != nil || reason != "" {
		return errors.Join(err, breakerError(reason))
	}
	return nil
}

// migrateStates runs the pr_state migrations until a pass completes them
// (Ledger.MigrateStatesOnce): tick is their one caller, so no click, verb
// or round's post re-creates a state behind tick's back. A dry run skips
// them, as it skips every write.
func (h tickHost) migrateStates() error {
	if h.dry {
		return nil
	}
	skipped, err := h.ledger.MigrateStatesOnce(h.lock)
	if err != nil {
		return fmt.Errorf("the pr_state migration: %w", err)
	}
	if skipped > 0 && h.out != nil {
		fmt.Fprintf(h.out, "claude-review tick: %d PRs held by a click; the pr_state migration runs again next pass\n", skipped)
	}
	return nil
}

// recordMentions stores each review-repo PR's mention gate, under the PR's
// lock, before anything mints or renders an Approve this pass. A PR a click
// holds is returned with its gate instead: refresh stores it under its own
// lock before it renders that PR's row, so no row this pass reads a gate
// older than elly's.
func (h tickHost) recordMentions(prs []EllyPR) (unstored map[string]MentionGate, err error) {
	unstored = map[string]MentionGate{}
	for _, p := range prs {
		// The user's own PRs too: their row shows the mention like any.
		pr, err := ParseTarget(p.Url)
		if err != nil || !repoAllowed(p.RepoOwner, p.RepoName, h.tcfg.ReviewRepos) {
			continue
		}
		lk, err := lockPR(pr, h.lock)
		if errors.Is(err, errLocked) {
			unstored[pr.URL()] = mentionGateOf(p)
			continue
		}
		if err != nil {
			return nil, err
		}
		err = h.ledger.SetMentionGate(lk, pr, mentionGateOf(p))
		lk.Unlock()
		if err != nil {
			return nil, err
		}
	}
	return unstored, nil
}

// plan starts the owed rounds, refreshes every row and offers Start where
// a click is what is missing; without elly's Asked it only refreshes.
func (h tickHost) plan(prs []EllyPR, running int) error {
	prs = withoutStaleMentions(prs, h.now())
	gates, err := h.recordMentions(prs)
	if err != nil {
		return err
	}
	readable, err := h.askedReadable(prs)
	if err != nil {
		return err
	}
	held := map[string]Hold{}
	if readable {
		if held, err = h.startOwed(prs, running); err != nil {
			return err
		}
	}
	if err := h.refresh(held, gates, prs); err != nil {
		return err
	}
	if readable {
		h.offerStarts(prs)
	}
	return nil
}

// askedReadable: elly says who is asked on every row. When it does not (an
// older or newer elly), tick plans and offers nothing this pass, rather
// than read every request as none; it says so each pass and notifies once
// until elly sends the field again.
func (h tickHost) askedReadable(prs []EllyPR) (bool, error) {
	if askedReadable(prs) {
		return true, h.ledger.DeleteMeta(metaAskedUnreadable)
	}
	if h.out != nil {
		fmt.Fprintln(h.out, "claude-review tick: elly sent no Asked this build reads; no review is planned this pass")
	}
	seen, err := h.ledger.Meta(metaAskedUnreadable)
	if err != nil || seen != "" {
		return false, err
	}
	if h.notify != nil {
		h.notify("claude-review plans no reviews: elly does not say who is asked; update elly and claude-review together")
	}
	return false, h.ledger.SetMeta(metaAskedUnreadable, "1")
}

// metaAskedUnreadable marks that the user was told elly sends no Asked.
const metaAskedUnreadable = "asked-unreadable-notified"

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
	if causeOf(reason) == causeTotalCap {
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
		if err != nil {
			continue
		}
		mine, err := h.workersOf(pr, all)
		if err != nil {
			fmt.Fprintf(h.out, "claude-review tick: %s: no worker stopped: %v\n", pr.URL(), err)
			continue
		}
		if len(unread[u]) == 0 && len(mine) == 0 {
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
// included. An error is unknown for pr: a roster entry naming it did not
// parse.
func (h tickHost) workersOf(pr PR, all Roster) ([]Worker, error) {
	return h.sess.workersFor(all, pr.workerIDs()...)
}

// closeOut stops closed pr's workers and settles its unread rounds the way
// settle does a closed PR's, under the PR's lock and from a roster read
// under it. A stop that fails leaves the rounds unread for the next tick.
func (h tickHost) closeOut(pr PR, rounds []Round) error {
	lk, err := lockPR(pr, h.lock)
	if errors.Is(err, errLocked) {
		return nil
	}
	if err != nil {
		return err
	}
	defer lk.Unlock()
	all, err := h.sess.Roster()
	if err != nil {
		return err
	}
	mine, err := h.workersOf(pr, all)
	if err != nil {
		return err
	}
	for _, w := range mine {
		if err := h.do(fmt.Sprintf("stop worker %s on %s: the PR is merged or closed", w.Short, pr.URL()), func() error { return h.sess.stopAndWait(w) }); err != nil {
			return err
		}
	}
	_, err = h.move(lk, pr, Event{Kind: evClosed})
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
		lk, err := lockPR(pr, h.lock)
		if err != nil {
			if !errors.Is(err, errLocked) {
				fmt.Fprintf(h.out, "claude-review tick: %s: %v\n", pr.URL(), err)
			}
			running++
			continue
		}
		busy, err := h.settleRound(lk, pr, r, true)
		lk.Unlock()
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
func (h tickHost) settleRound(lk prlock.Locked, pr PR, r Round, retry bool) (busy bool, err error) {
	sid := r.SessionFor(pr)
	if h.running(sid) {
		_, err := h.move(lk, pr, Event{Kind: evResumed, Round: r.ID})
		return true, err
	}
	if why, ok := h.blocked(sid); ok {
		return false, h.settleBlocked(lk, pr, r, why, retry)
	}
	msg, err := h.lastMessage(sid, r.TranscriptFrom)
	if err != nil {
		return h.unreadable(lk, pr, r, retry, err)
	}
	facts, err := h.gh.RoundFacts(pr, r)
	if err != nil {
		return false, err // GitHub unreachable: read it next tick
	}
	if facts.DismissalPending, err = h.ledger.DismissalPending(pr); err != nil {
		return false, err
	}
	return h.apply(lk, pr, r, decide(r, ReadOutcome(msg), facts, pr), retry)
}

// maxUnreadable is how many passes running a round's transcript may fail to
// read before the round is read as stuck: a passing I/O error heals, a
// rewritten transcript does not.
const maxUnreadable = 3

// unreadable counts a pass whose read of r's transcript failed with why.
// Below maxUnreadable the round stays unread and is read again next pass;
// at it the round is read and the PR goes stuck{failed}, a needs-you row.
// A click's read counts too: a round it cannot read stops its new round.
func (h tickHost) unreadable(lk prlock.Locked, pr PR, r Round, retry bool, why error) (bool, error) {
	if r.Unreadable+1 < maxUnreadable {
		return false, errors.Join(fmt.Errorf("reading the round: %w", why),
			h.do("count an unreadable read of "+pr.URL(), func() error { return h.ledger.MarkUnreadable(r.ID) }))
	}
	fmt.Fprintf(h.out, "claude-review tick: %s: reading the round: %v; stuck\n", pr.URL(), why)
	stuck := Next{State: PRState{State: stStuck, Kind: kindFailed, Detail: noteUnreadable, Head: r.Head}}
	return h.apply(lk, pr, r, stuck, retry)
}

// settleBlocked reads a round whose worker waits for the user: the question
// row, answered in the session or in a fork with prod data. The round stays
// unread, so the worker's resuming and ending are seen. A push since the
// round started overtakes the question: the worker is stopped and the round
// read, so the new head gets a round of its own, which asks again if the
// question still holds.
func (h tickHost) settleBlocked(lk prlock.Locked, pr PR, r Round, why string, retry bool) error {
	_, head, _, err := h.gh.Standing(pr)
	if err != nil {
		return err // GitHub unreachable: read it next tick
	}
	if _, err := h.move(lk, pr, Event{Kind: evBlocked, Question: capRunes(why, maxQuestionNote), Round: r.ID}); err != nil {
		return err
	}
	switch {
	case head != "" && head != r.Head:
		_, err = h.move(lk, pr, Event{Kind: evPush, Head: head})
	case !retry:
		_, err = h.move(lk, pr, Event{Kind: evEnded, Next: &PRState{State: stIdle}, Round: r.ID})
	default:
		return nil
	}
	if err != nil {
		return err
	}
	return h.do("mark round "+pr.URL()+" read", func() error { return h.ledger.MarkRead(r.ID) })
}

// settleUnread reads pr's unread rounds before its idle worker is stopped
// for a new round; any it cannot read stops the new round. The caller holds
// lk.
func (h tickHost) settleUnread(lk prlock.Locked, pr PR) error {
	rounds, err := h.ledger.UnreadRounds()
	if err != nil {
		return err
	}
	for _, r := range rounds {
		if r.PR != pr.URL() {
			continue
		}
		if busy, err := h.settleRound(lk, pr, r, false); err != nil || busy {
			return errors.Join(err, errors.New("its last round still runs"))
		}
	}
	return nil
}

// apply carries out one decision; it reports whether a retry started.
// Without retry, a retry decision only reads the round.
func (h tickHost) apply(lk prlock.Locked, pr PR, r Round, d Decision, retry bool) (bool, error) {
	s, err := h.stateOf(lk, pr)
	if err != nil {
		return false, err
	}
	var next PRState
	switch d := d.(type) {
	case Closed:
		if _, err := h.move(lk, pr, Event{Kind: evClosed}); err != nil {
			return false, err
		}
		return false, h.do("mark round "+pr.URL()+" read", func() error { return h.ledger.MarkRead(r.ID) })
	case Retry:
		// No retry resumes into a session the user has open in a window.
		if retry && !windowInChain(s) {
			return h.retry(lk, pr, r)
		}
		next = PRState{State: stIdle}
	case Dismiss:
		// The record first: a crash or a refusal from here on leaves it for
		// the next tick, which settles it only on reading the dismissal back.
		if err := h.do(fmt.Sprintf("record the dismissal of review %s on %s", d.Review, pr.URL()), func() error {
			return errors.Join(h.ledger.AddDismissal(pr, d.Review, h.now()), h.ledger.RevokeTokens(pr))
		}); err != nil {
			return false, err
		}
		h.sendDismissal(Dismissal{PR: pr.URL(), Review: d.Review})
		next = d.State
	case Next:
		next = d.State
	}
	if namesRound(s, r.ID) {
		_, ok, err := h.moved(lk, pr, Event{Kind: evEnded, Next: &next, Round: r.ID})
		if err != nil || !ok {
			// No rule for it where it sits: the round stays unread and is
			// read again next pass, when the chain may have moved.
			return false, err
		}
	}
	// Read, and dropped when no element names it: it is not this state's.
	return false, h.do("mark round "+pr.URL()+" read", func() error { return h.ledger.MarkRead(r.ID) })
}

// windowInChain: a window is open somewhere in s's chain.
func windowInChain(s PRState) bool {
	for _, e := range chain(s) {
		if isWindow(e.PRState) {
			return true
		}
	}
	return false
}

// retry resumes r's session for a round cut off before its summary, and
// reports whether it started.
func (h tickHost) retry(lk prlock.Locked, pr PR, r Round) (bool, error) {
	var not skip
	failed := func(detail string) error {
		_, err := h.move(lk, pr, Event{Kind: evEnded, Next: &PRState{State: stHeld, Kind: kindLaunch, Detail: detail}, Round: r.ID})
		return err
	}
	err := h.do("retry "+pr.URL(), func() error {
		var err error
		not, err = h.launch(lk, pr, r.Trigger, nil, func(_ time.Time, from int64) error {
			if err := h.ledger.MarkRetried(r.ID, from); err != nil {
				return err
			}
			_, err := h.move(lk, pr, Event{Kind: evEnded, Next: &PRState{State: stRunning, Kind: kindRetry, Head: r.Head}, Round: r.ID})
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
	return p.requested() && repoAllowed(pr.Owner, pr.Repo, cfg.ReviewRepos)
}

// startOffer is the info annotation that is Start on elly's row. It lives
// in elly only: the ledger's rows are round states, and the first one a
// click writes ("starting") replaces it.
func startOffer(pr PR) Annotation {
	return Annotation{State: stateInfo, Links: []Link{{Label: "Start", URL: "claude-review://" + pr.Owner + "/" + pr.Repo + "/" + itoaPR(pr) + "?from=elly", Primary: true, Action: actionStart}}}
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
		lk, err := lockPR(pr, h.lock)
		if err != nil {
			continue // a click has it, and writes its own row
		}
		if err := h.elly.Put(pr.URL(), startOffer(pr), h.now().Add(offerTTL)); err != nil {
			fmt.Fprintf(h.out, "claude-review tick: %s: offering Start: %v\n", pr.URL(), err)
		}
		lk.Unlock()
	}
}

// clearOrSay clears pr's annotation, reporting a failure without stopping.
func (h tickHost) clearOrSay(lk prlock.Locked, pr PR) {
	if err := h.do("clear "+pr.URL(), func() error { return h.clear(lk, pr) }); err != nil {
		fmt.Fprintf(h.out, "claude-review tick: %s: %v\n", pr.URL(), err)
	}
}

// clear deletes pr's row in elly, then its receipt: a DELETE that fails
// keeps the receipt, so the stale-row walk sends it again next tick. elly
// answers a row it does not hold with success.
func (h tickHost) clear(lk prlock.Locked, pr PR) error {
	if err := lk.Check(prKey(pr)); err != nil {
		return err
	}
	if h.elly != nil {
		if err := h.elly.Delete(pr.URL()); err != nil {
			return err
		}
	}
	return h.ledger.ClearAnnotation(pr)
}

// startOwed starts the rounds planTick picks, at most MaxRunning at once,
// and says in the journal why it leaves each other PR. held maps each PR
// whose owed round the daily cap or the breaker holds back to the note its
// row shows this tick; that is never stored.
func (h tickHost) startOwed(prs []EllyPR, running int) (held map[string]Hold, err error) {
	plans, err := planTick(prs, h.tcfg, h.ledger, h.gh, h.now())
	if err != nil {
		return nil, err
	}
	held = map[string]Hold{}
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
// the daily cap or the breaker says so, and reports whether it did. The
// first-look move runs under the PR's lock; a PR a click holds is left this
// pass, held.
func (h tickHost) hold(p Planned, held map[string]Hold) (bool, error) {
	hold, err := h.holdOf(p)
	if err != nil {
		return false, err
	}
	note := hold.note()
	if p.Gate == gateFreshLook {
		lk, err := lockPR(p.PR, h.lock)
		if errors.Is(err, errLocked) {
			h.leave(p.PR, skipClickHolds)
			return true, nil
		}
		if err != nil {
			return false, err
		}
		note = "the PR changed under its first look"
		_, err = h.move(lk, p.PR, Event{Kind: evChangedUnderFirstLook, Head: p.Head})
		lk.Unlock()
		if err != nil {
			return false, err
		}
	} else if note != "" {
		held[p.PR.URL()] = hold
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
	lk, err := lockPR(p.PR, h.lock)
	if errors.Is(err, errLocked) {
		return skipClickHolds, nil
	}
	if err != nil {
		return "", err
	}
	defer lk.Unlock()
	switch windowOf(h.sess, h.ledger, p.PR) {
	case winOpen:
		_, err := h.move(lk, p.PR, Event{Kind: evWindowOpened})
		return skipWindowOpen, err
	case winUnknown:
		return skipWindowUnknown, nil
	case winClosed:
	}
	if busy, err := h.roundRuns(p.PR); err != nil || busy {
		return skipRunning, err
	}
	s, err := h.stateOf(lk, p.PR)
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
		not, err = h.launch(lk, p.PR, p.Trigger, func() error { return h.settleUnread(lk, p.PR) }, func(at time.Time, from int64) error {
			id, err := h.ledger.StartRoundFrom(p.PR, p.Head, p.Trigger, at, from)
			if err != nil {
				return err
			}
			e := owedEvent(p)
			e.Round = id
			_, err = h.move(lk, p.PR, e)
			return err
		}, func(detail string) error {
			_, err := h.move(lk, p.PR, Event{Kind: evFailedToStart, Class: detail})
			return err
		})
		return err
	})
	return not, err
}

// roundRuns: a round of pr may still run, in today's session or in the one
// an unread round recorded, which a policy version bump leaves behind.
func (h tickHost) roundRuns(pr PR) (bool, error) {
	if h.running(pr.RoundSessionID()) {
		return true, nil
	}
	rounds, err := h.ledger.UnreadRounds()
	if err != nil {
		return false, err
	}
	for _, r := range rounds {
		if r.PR == pr.URL() && h.running(r.SessionFor(pr)) {
			return true, nil
		}
	}
	return false, nil
}

// startingLifetime bounds a "starting" row a crashed click never cleared.
const startingLifetime = 2 * time.Minute

// setRow stores pr's row and tells the desktop when it newly needs the
// user: a state elly shows only to someone already looking is no signal.
// An unchanged state and note stay quiet, so a row seen every tick
// notifies once; an Approve link's fresh token does not count as a change.
func (h tickHost) setRow(lk prlock.Locked, pr PR, a Annotation, identity string) error {
	if err := lk.Check(prKey(pr)); err != nil {
		return err
	}
	was, had, err := h.ledger.Annotation(pr)
	if err != nil {
		return err
	}
	if err := h.ledger.SetAnnotation(pr, a); err != nil {
		return err
	}
	key := metaNotifiedPrefix + pr.URL()
	if !needsUser(a.State) {
		// Leaving a needs-the-user state: the same one again notifies.
		return h.ledger.DeleteMeta(key)
	}
	last, err := h.ledger.Meta(key)
	if err != nil || last == identity {
		return err
	}
	// A row up before identities were stored is seeded, not notified.
	seeded := last == "" && had && was.State == a.State && was.Note == a.Note
	// A click builds its host without one: it tells the desktop itself.
	if h.notify != nil && !seeded {
		h.notify(pr.URL() + " needs you: " + a.Note)
	}
	return h.ledger.SetMeta(key, identity)
}

// metaNotifiedPrefix keys the identity of the last needs-the-user row the
// user was told about, apart from the row's copy, so neither the copy's
// expiry nor an elly restart notifies again.
const metaNotifiedPrefix = "notified "

// holdOf is p's hold this pass: its daily cap, or the breaker, which wins.
func (h tickHost) holdOf(p Planned) (Hold, error) {
	reason, err := h.ledger.Tripped()
	if err != nil {
		return Hold{}, err
	}
	return holdFor(p.Gate == gatePaused, reason), nil
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
	if err != nil || causeOf(reason) != causeTotalCap {
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

// launch starts a round on pr in its clone and then records it with
// record, given the time taken before the launch and the session
// transcript's size just before it. It is the only way tick
// starts a round, so both failsafes live here. Every caller runs it through
// do; the dry-run check makes a caller that forgets fail loudly instead of
// posting. The breaker trips at TotalDailyCap starts in 24 hours; healBreaker
// clears a cap trip once, and a second one latches, so a loop that keeps starting rounds
// stops at the cap even after the window moves on. settle reads the PR's
// last round before its idle worker is stopped. The caller holds lk.
func (h tickHost) launch(lk prlock.Locked, pr PR, trigger Trigger, settle func() error, record func(at time.Time, from int64) error, failed func(detail string) error) (notStarted skip, err error) {
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
	cfg := h.cfg
	cfg.MentionsOnly = mentionsOnly(trigger)
	// The re-review prompt diffs from the last round; on a PR no round and
	// no click has seen (auto_start accepts at the current head) that diff
	// is empty, and the round would post nothing without reading the PR.
	_, _, settled, err := h.ledger.LastSettled(pr)
	if err != nil {
		return "", err
	}
	rereview := settled || h.exists(pr.SessionID())
	// from: where the round's own lines start in the transcript it appends
	// to, so its read never reaches an earlier round's words.
	var from int64
	st := h.starter()
	st.beforeLaunch = func() { from = h.transcriptSize(pr.RoundSessionID()) }
	res, err := st.start(cfg, pr, dir, remote, rereview, settle)
	if err != nil && res.Kind != relaunchDuplicate {
		return "", err
	}
	return h.afterRelaunch(lk, pr, res, err, func() error { return record(at, from) }, failed)
}

// starter starts rounds the way a click does, through h.start.
func (h tickHost) starter() roundStarter {
	return roundStarter{sess: h.sess, exists: h.exists, run: h.start, wrapper: h.tcfg.Wrapper, promptFromCaller: true}
}

// afterRelaunch is the one table from a launch's result to the PR's state,
// for the click and tick alike: the round is recorded only once its worker
// is listed and alone; failed records why none started. notStarted names
// why no round started; "" with no error is a start. Every result has its
// row, and the exhaustive linter fails a switch that misses one. Results
// where nothing was touched (busy, roster unreadable before any launch)
// move nothing: the PR is tried again next pass.
func (h tickHost) afterRelaunch(lk prlock.Locked, pr PR, res RelaunchResult, stopErr error, record func() error, failed func(detail string) error) (notStarted skip, err error) {
	switch res.Kind {
	case relaunched:
		return "", record()
	case relaunchWindow:
		_, err := h.move(lk, pr, Event{Kind: evWindowOpened})
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
	case relaunchBusy:
		return skipBusy, nil
	}
	return "", fmt.Errorf("launch ended with no outcome (kind %d)", res.Kind)
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
	s, err := h.peekState(p.PR) // planning only: startOne reads it again under the lock
	if err != nil || s.State != stAwaiting || s.Kind != kindQuestion {
		return p, err
	}
	answered, err := h.answered(p.PR, s)
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

// answered: the user replied in a thread of the round that asked q, from
// its start until it settled, or until now while its worker waits. A
// question stored before rounds were named looks in the last settled
// round, as before.
func (h tickHost) answered(pr PR, q PRState) (bool, error) {
	if !q.Round.IsZero() {
		started, settled, read, err := h.ledger.RoundWindow(q.Round)
		if err != nil {
			return false, err
		}
		if !read {
			settled = h.now()
		}
		return h.gh.RepliedInRoundThread(pr, started, settled)
	}
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
// missing from elly is not evidence. held is startOwed's paused rows;
// gates are the mention gates recordMentions could not store.
//
// Each PR's state is read under its lock, never before it: a click that
// held the lock in between wrote its own state and row, and a state read
// earlier would send the row it replaced.
func (h tickHost) refresh(held map[string]Hold, gates map[string]MentionGate, prs []EllyPR) error {
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
	// Which PRs to visit; what each holds is read under its lock.
	urls := map[string]bool{}
	for url := range states {
		urls[url] = true
	}
	for url := range rows {
		urls[url] = true
	}
	for url := range held {
		urls[url] = true
	}
	for url := range urls {
		pr, err := ParseTarget(url)
		if err != nil {
			continue
		}
		// A click holding the lock is about to write its own state, and the
		// row is refreshed next pass.
		lk, err := lockPR(pr, h.lock)
		if errors.Is(err, errLocked) {
			continue
		}
		if err != nil {
			fmt.Fprintf(h.out, "claude-review tick: %s: %v\n", pr.URL(), err)
			continue
		}
		h.refreshLocked(lk, pr, held, gates, updated[url])
		lk.Unlock()
	}
	return nil
}

// refreshLocked is refresh's step for one PR, under lk.
func (h tickHost) refreshLocked(lk prlock.Locked, pr PR, held map[string]Hold, gates map[string]MentionGate, updated time.Time) {
	if g, ok := gates[pr.URL()]; ok {
		if err := h.ledger.SetMentionGate(lk, pr, g); err != nil {
			fmt.Fprintf(h.out, "claude-review tick: %s: %v\n", pr.URL(), err)
			return
		}
	}
	s, ok, err := h.ledger.PRState(pr)
	if err != nil {
		fmt.Fprintf(h.out, "claude-review tick: %s: %v\n", pr.URL(), err)
		return
	}
	hold, isHeld := held[pr.URL()]
	switch {
	case ok:
	case isHeld:
		s = PRState{State: stIdle} // accepted before any round
	default:
		_, had, err := h.ledger.Annotation(pr)
		h.warn(pr, err)
		if had {
			h.clearOrSay(lk, pr) // a row no state stands behind
		}
		return
	}
	h.refreshOne(lk, pr, s, hold, updated)
}

// refreshOne moves pr's state s, read under lk, on evidence and sends its
// row.
func (h tickHost) refreshOne(lk prlock.Locked, pr PR, s PRState, hold Hold, updated time.Time) {
	s, err := h.evidence(lk, pr, s, updated)
	if err != nil {
		fmt.Fprintf(h.out, "claude-review tick: %s: %v\n", pr.URL(), err)
		return
	}
	if deleted(s) {
		return
	}
	if err := h.quiet("send "+pr.URL()+" to elly", func() error { return h.show(lk, pr, s, hold) }); err != nil {
		fmt.Fprintf(h.out, "claude-review tick: %s: %v\n", pr.URL(), err)
	}
}

// evidence moves s on what GitHub and the desktop say, and returns the
// state after. An idle PR is not asked about: it has no row.
// updated is the PR's last update as elly saw it, zero when elly does not
// list it.
func (h tickHost) evidence(lk prlock.Locked, pr PR, s PRState, updated time.Time) (PRState, error) {
	if (s.State == stHeld && s.Kind == kindWindow || windowBelow(s)) && h.windowClosed(pr) {
		// The head, so the restore drops what was judged at an older one;
		// unreadable, the restore stands as before.
		_, head, _, _ := h.gh.Standing(pr)
		next, err := h.move(lk, pr, Event{Kind: evWindowClosed, Head: head})
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
	return h.move(lk, pr, *e)
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
