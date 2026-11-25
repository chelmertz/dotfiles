package main

import (
	"reflect"
	"testing"
)

// allVariants has one value per variant, kinds included; the round-trip
// and render tests walk it, and TestEveryKeyHasAVariant pins it against
// allStates.
var allVariants = []Variant{
	Idle{}, Starting{From: "elly"},
	Running{By: kindTick}, Running{By: kindClick, Round: rid(7)}, Running{By: kindRetry}, AwaitingQuestion{Text: "q", Round: rid(8)},
	AwaitingQuestion{Text: "which table?"}, AwaitingApprove{}, AwaitingDrafts{Count: 3}, AwaitingDismissed{},
	HeldFresh{}, HeldWindow{},
	HeldLaunchDead{Class: launchNotStarted}, HeldLaunchDead{Class: launchDuplicate},
	HeldLaunchMaybeAlive{Class: launchUnlisted}, HeldLaunchMaybeAlive{Class: launchUnknown},
	StuckRefused{}, StuckFailed{Inline: inlineYes}, StuckFailed{Inline: inlineNo}, StuckFailed{Inline: inlineUnknown}, StuckFailed{Unreadable: true},
	HandledPosted{}, HandledPosted{Capped: true}, HandledNothing{}, HandledApproved{},
}

// new -> old -> new is lossless for every variant: the stored fields carry
// only literals the converter maps back exactly.
func TestVariantsRoundTripThroughTheStoredShape(t *testing.T) {
	for _, v := range allVariants {
		s := untyped(Typed{V: v, Head: "h1"})
		back := typed(s)
		if !reflect.DeepEqual(back.V, v) || back.Head != "h1" {
			t.Errorf("%#v -> %+v -> %#v", v, s, back.V)
		}
	}
}

func TestEveryKeyHasAVariant(t *testing.T) {
	seen := map[stateKey]bool{}
	for _, v := range allVariants {
		seen[v.key()] = true
	}
	for _, k := range allStates {
		if !seen[k] {
			t.Errorf("%s{%s} has no variant", k.State, k.Kind)
		}
	}
}

// What the stored shape can hold but no variant means is converted to
// something safe, never parsed for meaning.
func TestTheConverterNormalisesOddRows(t *testing.T) {
	for _, c := range []struct {
		in   PRState
		want Variant
	}{
		{PRState{State: stHeld, Kind: kindLaunch, Detail: "nothing started: exit status 1"}, HeldLaunchMaybeAlive{Class: launchUnknown}},
		{PRState{State: stHeld, Kind: kindLaunch}, HeldLaunchMaybeAlive{Class: launchUnknown}},
		{PRState{State: stAwaiting, Kind: kindDrafts, Detail: "some"}, StuckFailed{Inline: inlineUnknown}},
		{PRState{State: stStuck, Kind: kindFailed, Detail: "anything"}, StuckFailed{Inline: inlineUnknown}},
		{PRState{State: "frozen", Kind: "solid"}, HeldLaunchMaybeAlive{Class: launchUnknown}},
		{PRState{State: stRunning, Kind: "sideways"}, HeldLaunchMaybeAlive{Class: launchUnknown}},
		{PRState{State: stGoneLegacy}, Idle{}},
		{PRState{State: stHandled, Kind: kindPosted, Detail: detailCapped}, HandledPosted{Capped: true}},
	} {
		if got := typed(c.in).V; !reflect.DeepEqual(got, c.want) {
			t.Errorf("%+v: %#v, want %#v", c.in, got, c.want)
		}
	}
	// The chain converts too.
	q := PRState{State: stAwaiting, Kind: kindDrafts, Detail: "x"}
	got := typed(PRState{State: stHeld, Kind: kindWindow, Prev: &q})
	if got.Prev == nil || !reflect.DeepEqual(got.Prev.V, StuckFailed{Inline: inlineUnknown}) {
		t.Errorf("prev %+v", got.Prev)
	}
}

// Zero drafts is a count, not a broken detail.
func TestAwaitingZeroDraftsIsACount(t *testing.T) {
	if v := variantOf(PRState{State: stAwaiting, Kind: kindDrafts, Detail: "0"}); v != (AwaitingDrafts{Count: 0}) {
		t.Errorf("got %#v", v)
	}
}
