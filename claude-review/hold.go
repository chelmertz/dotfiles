package main

import "strings"

// Hold is why a PR's owed review waits this pass: none, its daily cap, or
// the breaker, which stops every PR and so wins over the cap. It is never
// stored; tick computes it per pass and render words it.
type Hold struct {
	kind  holdKind
	cause Cause
}

type holdKind int

const (
	holdNone holdKind = iota
	holdDailyCap
	holdBreaker
)

// Cause is why the breaker tripped, typed from its stored reason's prefix.
type Cause int

const (
	causeUnknown Cause = iota
	causeTotalCap
	causeLoop
	causeDuplicate
	causeSelfCheck
)

// causeOf types a stored breaker reason. A total-cap reason from before the
// prefixes carries its old suffix; anything else unprefixed is Unknown.
func causeOf(reason string) Cause {
	switch {
	case strings.HasPrefix(reason, breakerLoop):
		return causeLoop
	case strings.HasPrefix(reason, breakerCap), strings.HasSuffix(reason, "the limit across all PRs"):
		return causeTotalCap
	case strings.HasPrefix(reason, breakerDuplicate):
		return causeDuplicate
	case strings.HasPrefix(reason, breakerSelfCheck):
		return causeSelfCheck
	}
	return causeUnknown
}

// causeWords are the fixed words for each cause; no stored reason text
// reaches a row.
var causeWords = map[Cause]string{
	causeTotalCap:  "too many rounds started in 24 hours, the limit across all PRs",
	causeDuplicate: "two workers held one session",
	causeLoop:      "the limit across all PRs was reached twice in a week",
	causeSelfCheck: "the self-check on a new Claude Code failed",
	causeUnknown:   "the breaker tripped",
}

// holdFor merges the PR's daily cap with the breaker's stored reason ("" when
// it has not tripped); the breaker wins.
func holdFor(dailyCap bool, breaker string) Hold {
	switch {
	case breaker != "":
		return Hold{kind: holdBreaker, cause: causeOf(breaker)}
	case dailyCap:
		return Hold{kind: holdDailyCap}
	}
	return Hold{}
}

// note is the row's words for h, "" for no hold.
func (h Hold) note() string {
	switch h.kind {
	case holdDailyCap:
		return "paused: the daily review limit for this PR is reached"
	case holdBreaker:
		if h.cause == causeTotalCap {
			return "paused: " + causeWords[h.cause] + "; claude-review starts again once fewer than the limit started in the last 24 hours"
		}
		return "paused: " + causeWords[h.cause] + "; claude-review starts no round until it is reset"
	case holdNone:
	}
	return ""
}
