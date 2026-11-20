package main

import (
	"slices"
	"strconv"
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
	// stGoneLegacy: what builds before closing became a deletion stored
	// for a closed PR, if any row still holds it; it reads as idle.
	stGoneLegacy State = "gone"
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
	return in != m.not
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
	// dropWindow: the same chain without the window below its top.
	dropWindow bool
	// deletes: no state follows; the PR's state and row are deleted.
	deletes bool
}

func to(s State, kind string) ruleTarget { return ruleTarget{to: stateKey{s, kind}} }

var (
	toPrev       = ruleTarget{prev: true}
	toDecided    = ruleTarget{decided: true}
	toDropWindow = ruleTarget{dropWindow: true}
	toDeleted    = ruleTarget{deletes: true}
)

// deleted is transition's outcome for a closed PR: the zero state, which
// SetPRState refuses, so no caller can store it.
func deleted(s PRState) bool { return s.State == "" }

// staleAt: s's chain holds a waiting or running state judged at another
// head than head. An unknown head ("") proves nothing. Held states keep
// their head across a push, as the push rule leaves them.
func staleAt(s PRState, head string) bool {
	if head == "" {
		return false
	}
	for e := &s; e != nil; e = e.Prev {
		if (e.State == stAwaiting || e.State == stRunning) && e.Head != "" && e.Head != head {
			return true
		}
	}
	return false
}

// apply is the state t leads s to on e, before remember and the head check.
func (t ruleTarget) apply(s PRState, e Event, at time.Time) PRState {
	switch {
	case t.deletes:
		return PRState{}
	case t.prev && s.Prev != nil:
		return *s.Prev
	case t.prev:
		return PRState{State: stIdle, Head: s.Head, Since: at}
	case t.dropWindow:
		return withoutWindow(s)
	case t.decided:
		next := *e.Next
		next.Head, next.Since, next.Prev = firstNonEmpty(next.Head, firstNonEmpty(e.Head, s.Head)), at, nil
		return next
	}
	return PRState{State: t.to.State, Kind: t.to.Kind, Detail: e.Detail, Head: firstNonEmpty(e.Head, s.Head), Since: at}
}

// remembered is what a remembering rule keeps under its new state. A
// starting is never kept: its click aborts on what replaced it. A window
// opening also drops an older window off the top: one window stands for the
// user's session at a time.
func remembered(s PRState, e Event) *PRState {
	top := &s
	for top != nil && (top.State == stStarting || (e.Kind == evWindowOpened && top.State == stHeld && top.Kind == kindWindow)) {
		top = top.Prev
	}
	if top == nil {
		return nil
	}
	kept := *top
	return &kept
}

// windowBelow: a window sits in s's chain under its top.
func windowBelow(s PRState) bool {
	for e := s.Prev; e != nil; e = e.Prev {
		if e.State == stHeld && e.Kind == kindWindow {
			return true
		}
	}
	return false
}

// withoutWindow is s's chain without its first window below the top.
func withoutWindow(s PRState) PRState {
	if s.Prev == nil {
		return s
	}
	if s.Prev.State == stHeld && s.Prev.Kind == kindWindow {
		s.Prev = s.Prev.Prev
		return s
	}
	rest := withoutWindow(*s.Prev)
	s.Prev = &rest
	return s
}

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
	_, ok := typed(s).V.(HeldLaunchMaybeAlive)
	return ok
}

// rules is the whole state machine; the first rule that applies wins.
var rules = []rule{
	{from: anyState, event: evClosed, to: toDeleted, effects: []Effect{efStopWorkers, efRoundsRead}},
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
	{from: anyState, event: evWindowClosed, when: guard{"a window below", func(s PRState, _ Event) bool { return windowBelow(s) }}, to: toDropWindow},
}

// targets are the states r can lead to from k, for the diagram and the
// reachability test. Prev is never new: it was reached before.
func (r rule) targets() []stateKey {
	switch {
	case r.to.decided:
		return decidable
	case r.to.prev, r.to.dropWindow, r.to.deletes:
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
		next = r.to.apply(s, e, at)
		if r.remember {
			next.Prev = remembered(s, e)
		}
		if (r.to.prev || r.to.dropWindow) && e.Kind != evResumed && staleAt(next, e.Head) {
			// Judged at an older head: dropped as a push drops it.
			return PRState{State: stIdle, Head: e.Head, Since: at}, []Effect{efStopWorkers, efRevokeTokens, efRoundsRead}, true
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
// as the row's button. idle has no row, and a closed PR none either: the zero Annotation.
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
	t := typed(s)
	if k := t.V.key(); v.Paused != "" && (k.State == stIdle || k.State == stHandled) {
		return Annotation{State: stateNeedsYou, Note: capRunes(v.Paused, ellyMaxNote), Links: []Link{primary(attachLink(pr))}}
	}
	a := t.V.row(pr, t, v)
	a.Note = capRunes(a.Note, ellyMaxNote)
	return a
}

// Each variant words its own row; Variant requires it, so a new variant
// does not compile without one.

func (Idle) row(PR, Typed, View) Annotation { return Annotation{} }

func (x Starting) row(_ PR, t Typed, _ View) Annotation {
	from := "a click"
	if x.From != "" {
		from = "a click from " + x.From
	}
	return Annotation{State: stateRunning, Note: "starting (" + from + ")", Expires: t.Since.Add(startingLifetime)}
}

func (x Running) row(pr PR, t Typed, _ View) Annotation {
	by := map[string]string{kindTick: kindTick, kindClick: "a click", kindRetry: "tick (retry)"}[x.By]
	return Annotation{State: stateRunning, Note: "started by " + by, Links: []Link{attachLink(pr)}, Expires: t.Since.Add(roundLifetime)}
}

func (x AwaitingQuestion) row(pr PR, _ Typed, _ View) Annotation {
	return needsYou(questionNote(Outcome{Question: x.Text}), primary(answerLink(pr)))
}

func (AwaitingApprove) row(pr PR, t Typed, v View) Annotation {
	return needsYou("LGTM at "+shortSHA(t.Head)+"; approve it if you agree",
		Link{Label: "Approve", URL: approveURL(pr, t.Head, v.Token), Primary: true}, attachLink(pr))
}

func (x AwaitingDrafts) row(pr PR, _ Typed, _ View) Annotation {
	return needsYou("Claude left "+strconv.Itoa(x.Count)+" unsent comments in a draft review; submit or discard it on GitHub", primary(attachLink(pr)))
}

func (AwaitingDismissed) row(pr PR, _ Typed, v View) Annotation {
	if v.Dismissing {
		return needsYou(noteDismissing, primary(attachLink(pr)))
	}
	return needsYou(noteDismissed, primary(attachLink(pr)))
}

func (HeldFresh) row(pr PR, _ Typed, _ View) Annotation {
	return needsYou("the PR changed under its first look (force-push or new base); click claude review to look again",
		primary(Link{Label: "Look again", URL: startAgainLink(pr).URL}), attachLink(pr))
}

func (HeldWindow) row(pr PR, _ Typed, _ View) Annotation {
	return needsYou(windowNote, primary(attachLink(pr)))
}

func (x HeldLaunchDead) row(pr PR, _ Typed, _ View) Annotation {
	return needsYou(launchNotes[x.Class], primary(startAgainLink(pr)))
}

// No Start: a second worker would review beside the first.
func (x HeldLaunchMaybeAlive) row(pr PR, _ Typed, _ View) Annotation {
	return needsYou(launchNotes[x.Class], primary(attachLink(pr)))
}

func (StuckRefused) row(pr PR, _ Typed, _ View) Annotation {
	return Annotation{State: stateStuck, Note: "a post was refused over the limit; no more reviews on this PR until you start one again",
		Links: []Link{primary(startAgainLink(pr)), attachLink(pr)}}
}

func (x StuckFailed) row(pr PR, _ Typed, _ View) Annotation {
	return Annotation{State: stateStuck, Note: inlineNote[x.Inline], Links: []Link{primary(attachLink(pr))}}
}

func (x HandledPosted) row(pr PR, _ Typed, _ View) Annotation {
	if x.Capped {
		return handled(pr, noteCapped)
	}
	return handled(pr, noteRoundPosted)
}

func (HandledNothing) row(pr PR, _ Typed, _ View) Annotation  { return handled(pr, noteNothing) }
func (HandledApproved) row(pr PR, _ Typed, _ View) Annotation { return handled(pr, noteApprovalStands) }

func handled(pr PR, note string) Annotation {
	return Annotation{State: stateHandled, Note: note, Links: []Link{attachLink(pr)}}
}

// needsYou is a row that waits on the user.
func needsYou(note string, links ...Link) Annotation {
	return Annotation{State: stateNeedsYou, Note: note, Links: links}
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

// inlineNote is a stuck{failed_twice} row's note: only the two facts a
// round can leave have words of their own; anything else says nothing
// about why Claude stopped.
var inlineNote = map[Inline]string{inlineYes: noteStoppedTwice, inlineNo: noteFailedTwice, inlineUnknown: noteStoppedUnknown}

const (
	noteFailedTwice    = "Claude failed twice"
	noteStoppedTwice   = "Claude stopped twice before posting its summary"
	noteStoppedUnknown = "Claude stopped; open it to see why"
)
