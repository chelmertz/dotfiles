package main

import (
	"strconv"
	"time"

	"github.com/chelmertz/dotfiles/claude-review/ids"
)

// Variant is a PR state with only its own data. It is sealed: the types
// below are all there is. The rule table works on keys (Variant.key); the
// stored JSON is still the old (State, Kind, Detail) shape, which untyped
// writes from a Variant and typed reads back, so an older binary reads a
// valid state and a newer one never parses free text for meaning.
//
//sumtype:decl
type Variant interface {
	key() stateKey
	// row is the variant's elly row, before the paused note and the cap.
	row(pr PR, t Typed, v View) Annotation
	// yieldsToMention: an open mention of the user takes this state's row.
	// Only states whose row asks nothing of the user yield (idle, handled)
	// and Approve, which an open mention withholds; a question, a running
	// or starting round, a hold and a stuck state keep their row (the
	// user, 2026-11-21: question first, then the mention).
	yieldsToMention() bool
	sealed()
}

type (
	Idle     struct{}
	Starting struct{ From string } // where the click came from
	// Running: By is kindTick, kindClick or kindRetry; Round is zero for one
	// stored before rounds were named.
	Running struct {
		By    string
		Round ids.RoundID
	}

	AwaitingQuestion struct {
		Text  string
		Round ids.RoundID
	}
	AwaitingApprove   struct{}
	AwaitingDrafts    struct{ Count int }
	AwaitingDismissed struct{}

	HeldFresh  struct{}
	HeldWindow struct{}
	// HeldLaunchDead: nothing started (Class launchNotStarted or
	// launchDuplicate, whose workers were stopped).
	HeldLaunchDead struct{ Class string }
	// HeldLaunchMaybeAlive: a worker may be running (launchUnlisted or
	// launchUnknown); nothing launches beside it until the roster says no.
	HeldLaunchMaybeAlive struct{ Class string }

	StuckRefused struct{}
	// StuckFailed: a round that stopped twice before its summary, or whose
	// transcript could not be read (Unreadable).
	StuckFailed struct {
		Inline     Inline
		Unreadable bool
	}

	HandledPosted   struct{ Capped bool }
	HandledNothing  struct{}
	HandledApproved struct{}
)

// Inline is whether a round that stopped twice had posted inline comments.
type Inline int

const (
	inlineUnknown Inline = iota
	inlineNo
	inlineYes
)

func (Idle) key() stateKey                 { return stateKey{stIdle, ""} }
func (Starting) key() stateKey             { return stateKey{stStarting, kindClick} }
func (v Running) key() stateKey            { return stateKey{stRunning, v.By} }
func (AwaitingQuestion) key() stateKey     { return stateKey{stAwaiting, kindQuestion} }
func (AwaitingApprove) key() stateKey      { return stateKey{stAwaiting, kindApprove} }
func (AwaitingDrafts) key() stateKey       { return stateKey{stAwaiting, kindDrafts} }
func (AwaitingDismissed) key() stateKey    { return stateKey{stAwaiting, kindDismiss} }
func (HeldFresh) key() stateKey            { return stateKey{stHeld, kindFresh} }
func (HeldWindow) key() stateKey           { return stateKey{stHeld, kindWindow} }
func (HeldLaunchDead) key() stateKey       { return stateKey{stHeld, kindLaunch} }
func (HeldLaunchMaybeAlive) key() stateKey { return stateKey{stHeld, kindLaunch} }
func (StuckRefused) key() stateKey         { return stateKey{stStuck, kindRefused} }
func (StuckFailed) key() stateKey          { return stateKey{stStuck, kindFailed} }
func (HandledPosted) key() stateKey        { return stateKey{stHandled, kindPosted} }
func (HandledNothing) key() stateKey       { return stateKey{stHandled, kindNothing} }
func (HandledApproved) key() stateKey      { return stateKey{stHandled, kindApproved} }

func (Idle) sealed()                 {}
func (Starting) sealed()             {}
func (Running) sealed()              {}
func (AwaitingQuestion) sealed()     {}
func (AwaitingApprove) sealed()      {}
func (AwaitingDrafts) sealed()       {}
func (AwaitingDismissed) sealed()    {}
func (HeldFresh) sealed()            {}
func (HeldWindow) sealed()           {}
func (HeldLaunchDead) sealed()       {}
func (HeldLaunchMaybeAlive) sealed() {}
func (StuckRefused) sealed()         {}
func (StuckFailed) sealed()          {}
func (HandledPosted) sealed()        {}
func (HandledNothing) sealed()       {}
func (HandledApproved) sealed()      {}

// Typed is a stored state as a Variant with the fields every state has.
type Typed struct {
	V      Variant
	Head   string
	Review string
	Since  time.Time
	Prev   *Typed
}

// typed reads a stored state. Anything the old shape can hold that no
// variant means becomes a safe variant: free text in a launch failure, or a
// pair no build wrote, is a launch whose worker may be alive (no rule
// launches from it, and the roster re-check resolves it); a drafts count
// that is not a number, or a stuck reason that is not one of the two
// sentences, is a stop whose reason is unknown. A "gone" never stays
// stored; read, it is idle.
func typed(s PRState) Typed {
	t := Typed{V: variantOf(s), Head: s.Head, Review: s.Review, Since: s.Since}
	if s.Prev != nil {
		p := typed(*s.Prev)
		t.Prev = &p
	}
	return t
}

// plain are the variants that carry nothing but their key.
var plain = map[stateKey]Variant{
	{stIdle, ""}: Idle{}, {stGoneLegacy, ""}: Idle{},
	{stAwaiting, kindApprove}: AwaitingApprove{}, {stAwaiting, kindDismiss}: AwaitingDismissed{},
	{stHeld, kindFresh}: HeldFresh{}, {stHeld, kindWindow}: HeldWindow{},
	{stStuck, kindRefused}:   StuckRefused{},
	{stHandled, kindNothing}: HandledNothing{}, {stHandled, kindApproved}: HandledApproved{},
}

func variantOf(s PRState) Variant {
	k := stateKey{s.State, s.Kind}
	if v, ok := plain[k]; ok {
		return v
	}
	switch k {
	case stateKey{stStarting, kindClick}:
		return Starting{From: s.Detail}
	case stateKey{stRunning, kindTick}, stateKey{stRunning, kindClick}, stateKey{stRunning, kindRetry}:
		return Running{By: s.Kind, Round: s.Round}
	case stateKey{stAwaiting, kindQuestion}:
		return AwaitingQuestion{Text: s.Detail, Round: s.Round}
	case stateKey{stAwaiting, kindDrafts}:
		if n, err := strconv.Atoi(s.Detail); err == nil && n >= 0 {
			return AwaitingDrafts{Count: n}
		}
		return StuckFailed{Inline: inlineUnknown}
	case stateKey{stHeld, kindLaunch}:
		switch s.Detail {
		case launchNotStarted, launchDuplicate:
			return HeldLaunchDead{Class: s.Detail}
		case launchUnlisted:
			return HeldLaunchMaybeAlive{Class: s.Detail}
		}
	case stateKey{stStuck, kindFailed}:
		if s.Detail == noteUnreadable {
			return StuckFailed{Unreadable: true}
		}
		return StuckFailed{Inline: map[string]Inline{noteStoppedTwice: inlineYes, noteFailedTwice: inlineNo}[s.Detail]}
	case stateKey{stHandled, kindPosted}:
		return HandledPosted{Capped: s.Detail == detailCapped}
	}
	return HeldLaunchMaybeAlive{Class: launchUnknown}
}

// untyped is the stored shape of t: each variant's own pair, with a fixed
// Detail literal typed reads back to the same variant.
func untyped(t Typed) PRState {
	k := t.V.key()
	s := PRState{State: k.State, Kind: k.Kind, Head: t.Head, Review: t.Review, Since: t.Since}
	switch v := t.V.(type) {
	case Running:
		s.Round = v.Round
	case Idle, AwaitingApprove, AwaitingDismissed, HeldFresh, HeldWindow, StuckRefused, HandledNothing, HandledApproved:
		// The pair alone says it all.
	case Starting:
		s.Detail = v.From
	case AwaitingQuestion:
		s.Detail, s.Round = v.Text, v.Round
	case AwaitingDrafts:
		s.Detail = strconv.Itoa(v.Count)
	case HeldLaunchDead:
		s.Detail = v.Class
	case HeldLaunchMaybeAlive:
		s.Detail = v.Class
	case StuckFailed:
		if v.Unreadable {
			s.Detail = noteUnreadable
			break
		}
		s.Detail = map[Inline]string{inlineYes: noteStoppedTwice, inlineNo: noteFailedTwice}[v.Inline]
	case HandledPosted:
		if v.Capped {
			s.Detail = detailCapped
		}
	}
	if t.Prev != nil {
		p := untyped(*t.Prev)
		s.Prev = &p
	}
	return s
}

// normalized is s as the converter reads it, in the stored shape.
func normalized(s PRState) PRState { return untyped(typed(s)) }
