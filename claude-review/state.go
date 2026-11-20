package main

import (
	"slices"
	"time"

	"github.com/chelmertz/dotfiles/claude-review/ids"
)

// A PR's state, stored in the ledger and the only source of the row elly
// shows: render writes the annotation from it, and nothing reads an
// annotation back. transition is the one place a state changes, and it only
// interprets rules, so `claude-review states` can draw every transition the
// code has. Tick, the click and the verbs observe events and run the effects.

// State is one of the st* names.
type State string

const (
	stIdle     State = "idle"
	stStarting State = "starting"
	stRunning  State = "running"
	stAwaiting State = "awaiting"
	stHeld     State = "held"
	stStuck    State = "stuck"
	stHandled  State = "handled"
	// stGone: the PR closed; its state and elly row are deleted.
	stGone State = "gone"
)

// Kinds refine a state.
const (
	kindTick     = "tick"
	kindClick    = "click"
	kindRetry    = "retry"
	kindQuestion = "question"
	kindApprove  = "approve"
	kindDrafts   = "drafts"
	kindDismiss  = "dismissed"
	kindFresh    = "fresh_look"
	kindWindow   = "window"
	kindLaunch   = "launch_failed"
	kindRefused  = "post_refused"
	kindFailed   = "failed_twice"
	kindPosted   = "posted"
	kindNothing  = "nothing"
	kindApproved = "approval_stands"
)

type stateKey struct {
	State State
	Kind  string
}

// allStates is every state the model has; render and the diagram cover each.
var allStates = []stateKey{
	{stIdle, ""},
	{stStarting, kindClick},
	{stRunning, kindTick}, {stRunning, kindClick}, {stRunning, kindRetry},
	{stAwaiting, kindQuestion}, {stAwaiting, kindApprove}, {stAwaiting, kindDrafts}, {stAwaiting, kindDismiss},
	{stHeld, kindFresh}, {stHeld, kindWindow}, {stHeld, kindLaunch},
	{stStuck, kindRefused}, {stStuck, kindFailed},
	{stHandled, kindPosted}, {stHandled, kindNothing}, {stHandled, kindApproved},
	{stGone, ""},
}

// decidable is what decide may choose for an ended round. idle is a round
// read just before a new one replaces it; held{launch_failed} a retry that
// could not start.
var decidable = []stateKey{
	{stIdle, ""}, {stHeld, kindLaunch},
	{stRunning, kindRetry},
	{stAwaiting, kindQuestion}, {stAwaiting, kindApprove}, {stAwaiting, kindDrafts}, {stAwaiting, kindDismiss},
	{stStuck, kindRefused}, {stStuck, kindFailed},
	{stHandled, kindPosted}, {stHandled, kindNothing}, {stHandled, kindApproved},
}

// PRState is a PR's stored state. Prev is what a transient state (starting,
// a blocked worker, a window) returns to.
type PRState struct {
	State  State
	Kind   string
	Detail string // the question, a launch failure, a count of drafts, who clicked
	Head   string // the PR head the state is about
	Review string // the round's summary review, linked from the row
	Since  time.Time
	Prev   *PRState
}

// Event is something tick, a click or a verb observed.
type Event struct {
	Kind   EventKind
	Head   string
	Detail string
	// Next is the state decide chose for an ended round.
	Next *PRState
	// Trigger is why an owed round is owed; a request lifts stuck.
	Trigger Trigger
	// FreshLook: the PR changed under its first look (force-push, new base).
	FreshLook bool
}

// EventKind is one of the ev* names.
type EventKind string

const (
	evClosed       EventKind = "closed"
	evUserApproved EventKind = "user_approved"
	evClick        EventKind = "click"
	evLaunched     EventKind = "launched"
	evNotStarted   EventKind = "not_started"
	evOwed         EventKind = "owed"
	evPush         EventKind = "push"
	evBlocked      EventKind = "blocked"
	evResumed      EventKind = "resumed"
	evEnded        EventKind = "ended"
	evWindowOpened EventKind = "window_opened"
	evWindowClosed EventKind = "window_closed"
	// evLaunchCleared: tick found no worker and no unread round for a launch
	// whose worker may have been alive.
	evLaunchCleared EventKind = "launch_cleared"
)

// Effect is work a transition asks the caller to do, in order, before it
// stores the new state.
type Effect string

const (
	efStopWorkers  Effect = "stop_workers"
	efRevokeTokens Effect = "revoke_tokens"
	efRoundsRead   Effect = "rounds_read"
)

// match picks the states a rule applies in. An empty kinds matches any kind;
// not inverts the whole match.
type match struct {
	states []State
	kinds  []string
	not    bool
}

func (m match) matches(s State, kind string) bool {
	in := (len(m.states) == 0 || slices.Contains(m.states, s)) && (len(m.kinds) == 0 || slices.Contains(m.kinds, kind))
	return in != m.not && s != stGone
}

var anyState = match{}

func in(states ...State) match { return match{states: states} }
func inKind(s State, kinds ...string) match {
	return match{states: []State{s}, kinds: kinds}
}
func notIn(m match) match { m.not = !m.not; return m }

// guard narrows a rule by the event or the stored state; name labels the
// diagram's arrow.
type guard struct {
	name string
	ok   func(PRState, Event) bool
}

// ruleTarget: a fixed state, the stored Prev, or decide's choice.
type ruleTarget struct {
	to      stateKey
	prev    bool
	decided bool
}

func to(s State, kind string) ruleTarget { return ruleTarget{to: stateKey{s, kind}} }

var (
	toPrev    = ruleTarget{prev: true}
	toDecided = ruleTarget{decided: true}
)

type rule struct {
	from    match
	event   EventKind
	when    guard
	to      ruleTarget
	effects []Effect
	// remember keeps the state left in Prev, to return to.
	remember bool
}

var (
	withReason   = guard{"with a reason", func(_ PRState, e Event) bool { return e.Detail != "" }}
	rerequested  = guard{"a request", func(_ PRState, e Event) bool { _, ok := e.Trigger.(Requested); return ok }}
	freshLook    = guard{"under its first look", func(_ PRState, e Event) bool { return e.FreshLook }}
	workerBack   = guard{"the worker works again", func(s PRState, _ Event) bool { return s.Prev != nil && s.Prev.State == stRunning }}
	decidedRetry = guard{"decide: retry", func(_ PRState, e Event) bool {
		return e.Next != nil && e.Next.State == stRunning && e.Next.Kind == kindRetry
	}}
	decided = guard{"decide", func(_ PRState, e Event) bool { return e.Next != nil }}
	// A launch that may have left a worker alive starts nothing until the
	// roster says otherwise.
	notMaybeAlive = guard{"no worker may be alive", func(s PRState, _ Event) bool { return !maybeAlive(s) }}
	deadLaunch    = guard{"nothing started", func(s PRState, _ Event) bool { return !maybeAlive(s) }}
	aliveCleared  = guard{"a worker may have been alive", func(s PRState, _ Event) bool { return maybeAlive(s) }}
	aliveDecided  = guard{"a worker may be alive; decide", func(s PRState, e Event) bool { return maybeAlive(s) && e.Next != nil }}
)

// maybeAlive: a held launch whose worker may be running: launched but not
// listed, or the roster unreadable.
func maybeAlive(s PRState) bool {
	return s.State == stHeld && s.Kind == kindLaunch && (s.Detail == launchUnlisted || s.Detail == launchUnknown)
}

// rules is the whole state machine; the first rule that applies wins.
var rules = []rule{
	{from: anyState, event: evClosed, to: to(stGone, ""), effects: []Effect{efStopWorkers, efRoundsRead}},
	// The user's approval at the head leaves nothing to show.
	{from: notIn(in(stRunning, stStarting)), event: evUserApproved, to: to(stIdle, ""), effects: []Effect{efRevokeTokens}},
	{from: notIn(in(stRunning, stStarting)), event: evClick, when: notMaybeAlive, to: to(stStarting, kindClick), remember: true},
	{from: in(stStarting), event: evLaunched, to: to(stRunning, kindClick)},
	{from: notIn(in(stRunning)), event: evNotStarted, when: withReason, to: to(stHeld, kindLaunch)},
	{from: in(stStarting), event: evNotStarted, to: toPrev},
	{from: in(stStuck), event: evOwed, when: rerequested, to: to(stRunning, kindTick)},
	{from: in(stIdle, stHandled, stAwaiting), event: evOwed, when: freshLook, to: to(stHeld, kindFresh)},
	{from: in(stIdle, stHandled, stAwaiting), event: evOwed, to: to(stRunning, kindTick)},
	{from: inKind(stHeld, kindLaunch), event: evOwed, when: deadLaunch, to: to(stRunning, kindTick)},
	{from: inKind(stHeld, kindLaunch), event: evEnded, when: aliveDecided, to: toDecided, effects: []Effect{efRoundsRead}},
	{from: inKind(stHeld, kindLaunch), event: evLaunchCleared, when: aliveCleared, to: to(stHeld, kindLaunch)},
	{from: in(stAwaiting), event: evPush, to: to(stIdle, ""), effects: []Effect{efStopWorkers, efRevokeTokens}},
	{from: in(stRunning), event: evBlocked, to: to(stAwaiting, kindQuestion), remember: true},
	// A round recorded without its move (a crash between the two writes).
	{from: in(stIdle), event: evBlocked, to: to(stAwaiting, kindQuestion)},
	{from: inKind(stAwaiting, kindQuestion), event: evResumed, when: workerBack, to: toPrev},
	{from: in(stRunning), event: evEnded, when: decidedRetry, to: toDecided},
	{from: in(stRunning), event: evEnded, when: decided, to: toDecided, effects: []Effect{efRoundsRead}},
	{from: inKind(stAwaiting, kindQuestion), event: evEnded, when: decided, to: toDecided, effects: []Effect{efRoundsRead}},
	// A round recorded without its move (a crash between the two writes).
	{from: in(stIdle), event: evEnded, when: decided, to: toDecided, effects: []Effect{efRoundsRead}},
	// The fork may read prod data: no Approve token stays live beside it; the
	// restore on close mints a fresh one.
	{from: notIn(inKind(stHeld, kindWindow)), event: evWindowOpened, to: to(stHeld, kindWindow), effects: []Effect{efRevokeTokens}, remember: true},
	{from: inKind(stHeld, kindWindow), event: evWindowClosed, to: toPrev},
}

// targets are the states r can lead to from k, for the diagram and the
// reachability test. Prev is never new: it was reached before.
func (r rule) targets() []stateKey {
	switch {
	case r.to.decided:
		return decidable
	case r.to.prev:
		return nil
	}
	return []stateKey{r.to.to}
}

// transition is the PR state machine. ok is false when no rule applies: the
// caller leaves the state as it is. at is when the event was seen.
func transition(s PRState, e Event, at time.Time) (next PRState, effects []Effect, ok bool) {
	for _, r := range rules {
		if r.event != e.Kind || !r.from.matches(s.State, s.Kind) || (r.when.ok != nil && !r.when.ok(s, e)) {
			continue
		}
		switch {
		case r.to.prev && s.Prev != nil:
			next = *s.Prev
		case r.to.prev:
			next = PRState{State: stIdle, Head: s.Head, Since: at}
		case r.to.decided:
			next = *e.Next
			next.Head, next.Since, next.Prev = firstNonEmpty(next.Head, firstNonEmpty(e.Head, s.Head)), at, nil
		default:
			next = PRState{State: r.to.to.State, Kind: r.to.to.Kind, Detail: e.Detail, Head: firstNonEmpty(e.Head, s.Head), Since: at}
		}
		if r.remember {
			prev := s
			next.Prev = &prev
		}
		return next, r.effects, true
	}
	return s, nil, false
}

func firstNonEmpty(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

// View is what render needs beyond the state: the Approve link's token,
// minted for an awaiting{approve} head, and why an owed round is held back
// this tick (the daily cap, the breaker), which is never stored.
type View struct {
	Token  ids.Token
	Paused string
	// Dismissing: a dismissal of the user's approval is recorded, not seen.
	Dismissing bool
}

// render is the PR's elly row and the only writer of notes and links. Every
// state that needs the user marks exactly one link Primary, which elly shows
// as the row's button. idle and gone have no row: the zero Annotation.
// render is a PR's elly row, with a `?` link to the states page on every
// row it shows.
func render(pr PR, s PRState, v View) Annotation {
	a := renderRow(pr, s, v)
	if a.State != "" {
		a.Links = append(a.Links, statesLink(pr))
	}
	return a
}

func statesLink(pr PR) Link { return Link{Label: "?", URL: verbURL(pr, "states")} }

func renderRow(pr PR, s PRState, v View) Annotation {
	var a Annotation
	switch s.State {
	case stStarting:
		from := "a click"
		if s.Detail != "" {
			from = "a click from " + s.Detail
		}
		a = Annotation{State: stateRunning, Note: "starting (" + from + ")", Expires: s.Since.Add(startingLifetime)}
	case stRunning:
		by := map[string]string{kindTick: kindTick, kindClick: "a click", kindRetry: "tick (retry)"}[s.Kind]
		a = Annotation{State: stateRunning, Note: "started by " + by, Links: []Link{attachLink(pr)}, Expires: s.Since.Add(roundLifetime)}
	case stAwaiting, stHeld:
		a = renderNeedsYou(pr, s, v)
	case stStuck:
		a.State = stateStuck
		if s.Kind == kindRefused {
			a.Note, a.Links = "a post was refused over the limit; no more reviews on this PR until you start one again", []Link{primary(startAgainLink(pr)), attachLink(pr)}
		} else {
			a.Note, a.Links = failedTwiceNote(s.Detail), []Link{primary(attachLink(pr))}
		}
	case stHandled, stIdle:
		if v.Paused != "" {
			return Annotation{State: stateNeedsYou, Note: capRunes(v.Paused, ellyMaxNote), Links: []Link{primary(attachLink(pr))}}
		}
		if s.State == stIdle {
			return Annotation{}
		}
		a.State = stateHandled
		a.Note = map[string]string{kindPosted: noteRoundPosted, kindNothing: noteNothing, kindApproved: noteApprovalStands}[s.Kind]
		if s.Kind == kindPosted && s.Detail == detailCapped {
			a.Note = noteCapped
		}
		a.Links = []Link{attachLink(pr)}
	default:
		return Annotation{}
	}
	a.Note = capRunes(a.Note, ellyMaxNote)
	return a
}

// Row texts. elly's reader cares whether the PR needs them, not about
// rounds, so no row says "round".
const (
	noteRoundPosted = "Claude posted its review"
	noteCapped      = "Claude posted its review at the comment limit; the rest is in its summary comment"
	// detailCapped marks a handled{posted} round whose comments hit the
	// per-head limit.
	detailCapped       = "capped"
	noteNothing        = "Claude found nothing to post"
	noteApprovalStands = "Claude found nothing new; your approval stands"
	noteDismissed      = "new findings after your approval; your approval was dismissed"
	noteDismissing     = "new findings after your approval; dismissing your approval, retrying"
)

func primary(l Link) Link { l.Primary = true; return l }

// renderNeedsYou is an awaiting or held row: it waits on the user.
func renderNeedsYou(pr PR, s PRState, v View) Annotation {
	a := Annotation{State: stateNeedsYou}
	switch s.Kind {
	case kindQuestion:
		a.Note, a.Links = questionNote(Outcome{Question: s.Detail}), []Link{primary(answerLink(pr))}
	case kindApprove:
		a.Note = "LGTM at " + shortSHA(s.Head) + "; approve it if you agree"
		a.Links = []Link{{Label: "Approve", URL: approveURL(pr, s.Head, v.Token), Primary: true}, attachLink(pr)}
	case kindDrafts:
		a.Note = "Claude left " + s.Detail + " unsent comments in a draft review; submit or discard it on GitHub"
		a.Links = []Link{primary(attachLink(pr))}
	case kindDismiss:
		a.Note = noteDismissed
		if v.Dismissing {
			a.Note = noteDismissing
		}
		a.Links = []Link{primary(attachLink(pr))}
	case kindFresh:
		a.Note = "the PR changed under its first look (force-push or new base); click claude review to look again"
		a.Links = []Link{primary(Link{Label: "Look again", URL: startAgainLink(pr).URL}), attachLink(pr)}
	case kindWindow:
		a.Note, a.Links = windowNote, []Link{primary(attachLink(pr))}
	case kindLaunch:
		a.Note, a.Links = launchNote(s.Detail), []Link{primary(startAgainLink(pr))}
		if maybeAlive(s) {
			// No Start: a second worker would review beside the first.
			a.Links = []Link{primary(attachLink(pr))}
		}
	}
	return a
}

func shortSHA(sha string) string {
	if len(sha) > 9 {
		return sha[:9]
	}
	return sha
}

// Launch classes: the Detail a held{launch_failed} row stores. The note is
// read from launchNotes and never from the error that caused it, which can
// hold session ids, argv or a response body. C1's LaunchError reads the
// same table.
const (
	launchNotStarted = "not_started"
	launchUnlisted   = "unlisted"
	launchUnknown    = "unknown_roster"
	launchDuplicate  = "duplicate"
)

var launchNotes = map[string]string{
	launchNotStarted: "nothing started; start it again",
	launchUnlisted:   "the round was launched but its worker was not listed; a review may be running, check before starting again",
	launchUnknown:    "the session list could not be read; a review may be running, check before starting again",
	launchDuplicate:  "two workers held one session; rounds are paused for every PR",
}

// launchNote is a held{launch_failed} row's note. A Detail stored before
// the classes existed is old error text and gets the not-started words.
func launchNote(detail string) string {
	if n, ok := launchNotes[detail]; ok {
		return n
	}
	return launchNotes[launchNotStarted]
}

// failedTwiceNote is a stuck{failed_twice} row's note; the stored Detail is
// one of failedTwice's two texts or, from a migrated row, anything, which
// says nothing about why Claude stopped.
func failedTwiceNote(detail string) string {
	switch detail {
	case noteStoppedTwice, noteFailedTwice:
		return detail
	}
	return noteStoppedUnknown
}

const (
	noteFailedTwice    = "Claude failed twice"
	noteStoppedTwice   = "Claude stopped twice before posting its summary"
	noteStoppedUnknown = "Claude stopped; open it to see why"
)
