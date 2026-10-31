package main

import (
	"regexp"
	"strings"
)

// questionLine ends a round that stops on a question only the user can
// answer, the way the approve line ends a clean round. A runner reads it from
// the session's last message; it must stay byte-for-byte what the prompt asks for.
const questionLine = "claude-review: question for the user"

// questionRound follows cleanRound in every prompt.
const questionRound = "\n\nIf the round ends on a question only the user can answer (intent, a product decision, something neither the PR nor the code can tell you), ask it, then end with a line containing only `" + questionLine + "`."

type OutcomeKind int

const (
	OutcomeOther OutcomeKind = iota
	OutcomeApprove
	OutcomeQuestion
)

// Outcome is what a round's last message says it ended on. It is the
// session's own account, so a runner checks it against GitHub before acting.
type Outcome struct {
	Kind  OutcomeKind
	PrURL string
	SHA   string
}

var approveLine = regexp.MustCompile(`claude-review approve (https://github\.com/[A-Za-z0-9._-]+/[A-Za-z0-9._-]+/pull/[1-9][0-9]*) --at ([0-9a-f]{40})\b`)

// ReadOutcome reads a round's last message. The question line counts only as
// the message's last non-empty line; when both are present the question wins,
// since it needs the user either way.
func ReadOutcome(msg string) Outcome {
	lines := strings.Split(strings.TrimSpace(msg), "\n")
	if strings.TrimSpace(lines[len(lines)-1]) == questionLine {
		return Outcome{Kind: OutcomeQuestion}
	}
	if m := approveLine.FindStringSubmatch(msg); m != nil {
		return Outcome{Kind: OutcomeApprove, PrURL: m[1], SHA: m[2]}
	}
	return Outcome{Kind: OutcomeOther}
}
