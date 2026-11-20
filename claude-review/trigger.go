package main

import (
	"fmt"
	"strings"
	"time"
)

// Trigger is why a round started. It is sealed: the five variants below
// are all there is. The rounds table keeps the typed value as
// (trigger_kind, trigger_key, trigger_from) and, until the old column is
// dropped, the old text an older binary reads, rendered only here.
type Trigger interface {
	Kind() string
	// DedupeKey tells two triggers of one kind apart: the same request
	// does not run twice at one head.
	DedupeKey() string
	// CountsTowardCap: the daily cap and the breaker count it. A click is
	// the user's own round.
	CountsTowardCap() bool
	// Text is the old trigger column's text.
	Text() string
	sealed()
}

// Requested: a review was requested or re-requested of the user; At is the
// PR's last update.
type Requested struct{ At time.Time }

// Replies: someone answered in a thread the user owes; At is the PR's last
// update.
type Replies struct{ At time.Time }

// Answered: the user replied to a round's question; At is when tick saw it.
type Answered struct{ At time.Time }

// NewHead: the PR has commits the last round did not see.
type NewHead struct{ Head string }

// Click: the user started the round; From is where the link was clicked,
// statistics only.
type Click struct {
	At   time.Time
	From string
}

const (
	trigRequested = "requested"
	trigReplies   = "replies"
	trigAnswered  = "answered"
	trigNewHead   = "new_head"
	trigClick     = "click"
	// trigUnparsed marks a backfilled row whose old text no parser reads.
	trigUnparsed = "unparsed"
)

func stamp(t time.Time) string { return t.UTC().Format(time.RFC3339) }

func (t Requested) Kind() string { return trigRequested }
func (t Replies) Kind() string   { return trigReplies }
func (t Answered) Kind() string  { return trigAnswered }
func (t NewHead) Kind() string   { return trigNewHead }
func (t Click) Kind() string     { return trigClick }

func (t Requested) DedupeKey() string { return stamp(t.At) }
func (t Replies) DedupeKey() string   { return stamp(t.At) }
func (t Answered) DedupeKey() string  { return stamp(t.At) }
func (t NewHead) DedupeKey() string   { return t.Head }
func (t Click) DedupeKey() string     { return stamp(t.At) }

func (Requested) CountsTowardCap() bool { return true }
func (Replies) CountsTowardCap() bool   { return true }
func (Answered) CountsTowardCap() bool  { return true }
func (NewHead) CountsTowardCap() bool   { return true }
func (Click) CountsTowardCap() bool     { return false }

func (t Requested) Text() string { return "review_requested@" + stamp(t.At) }
func (t Replies) Text() string   { return "replies@" + stamp(t.At) }
func (t Answered) Text() string  { return "answered@" + stamp(t.At) }
func (NewHead) Text() string     { return "new_head" }
func (t Click) Text() string     { return "click@" + stamp(t.At) + "?from=" + t.From }

func (Requested) sealed() {}
func (Replies) sealed()   {}
func (Answered) sealed()  {}
func (NewHead) sealed()   {}
func (Click) sealed()     {}

// allTriggers has one value per variant, for the generated kind lists.
var allTriggers = []Trigger{Requested{}, Replies{}, Answered{}, NewHead{}, Click{}}

// capKinds are the kinds the cap and the breaker count.
func capKinds() []string {
	var out []string
	for _, t := range allTriggers {
		if t.CountsTowardCap() {
			out = append(out, t.Kind())
		}
	}
	return out
}

// capKindsSQL is capKinds as an SQL list; kinds are these constants, never
// input.
func capKindsSQL() string {
	return "'" + strings.Join(capKinds(), "','") + "'"
}

// triggerFrom is a click's source, "" for any other trigger.
func triggerFrom(t Trigger) string {
	if c, ok := t.(Click); ok {
		return c.From
	}
	return ""
}

// parseTrigger reads the old text back, for rows written before the typed
// columns or by a rolled-back binary; head is the row's head, which a
// new_head trigger is keyed by. A request and a re-request become one
// Requested.
func parseTrigger(text, head string) (Trigger, error) {
	if text == "new_head" {
		return NewHead{Head: head}, nil
	}
	name, rest, ok := strings.Cut(text, "@")
	if !ok {
		return nil, fmt.Errorf("trigger without a time: %q", text)
	}
	if name == "click" {
		at, from, _ := strings.Cut(rest, "?from=")
		t, err := time.Parse(time.RFC3339, at)
		if err != nil {
			return nil, fmt.Errorf("click trigger: %w", err)
		}
		return Click{At: t.UTC(), From: from}, nil
	}
	t, err := time.Parse(time.RFC3339, rest)
	if err != nil {
		return nil, fmt.Errorf("trigger %q: %w", name, err)
	}
	t = t.UTC()
	switch name {
	case "review_requested", "rereview_requested":
		return Requested{At: t}, nil
	case "replies":
		return Replies{At: t}, nil
	case "answered":
		return Answered{At: t}, nil
	}
	return nil, fmt.Errorf("unknown trigger %q", name)
}

// freeKindsSQL are the kinds the cap and the breaker do not count, as an SQL
// list. Querying by exclusion makes an unparsed row count. Kinds are these
// constants, never input.
func freeKindsSQL() string {
	var out []string
	for _, t := range allTriggers {
		if !t.CountsTowardCap() {
			out = append(out, t.Kind())
		}
	}
	return "'" + strings.Join(out, "','") + "'"
}

// triggerOf is a stored row's trigger, nil for an unparsed one.
func triggerOf(kind, key, from string) Trigger {
	at, _ := time.Parse(time.RFC3339, key)
	switch kind {
	case trigRequested:
		return Requested{At: at}
	case trigReplies:
		return Replies{At: at}
	case trigAnswered:
		return Answered{At: at}
	case trigNewHead:
		return NewHead{Head: key}
	case trigClick:
		return Click{At: at, From: from}
	}
	return nil
}
