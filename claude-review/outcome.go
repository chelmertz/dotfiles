package main

import (
	"regexp"
	"strings"
	"unicode"
)

// questionLine ends a round that stops on a question only the user can
// answer, the way the approve line ends a clean round. A runner reads it from
// the session's last message; it must stay byte-for-byte what the prompt asks for.
const questionLine = "claude-review: question for the user"

// questionRound follows cleanRound in every prompt.
const questionRound = "\n\nIf the round ends on a question only the user can answer (intent, a product decision, something neither the PR nor the code can tell you), ask it, then end with a line containing only `" + questionLine + "`."

// nothingLine ends a later round that found nothing to post: no new commits
// and no reply that needs an answer. Without it such a round reads as one
// that failed. Same byte-for-byte contract as questionLine.
const nothingLine = "claude-review: nothing to post"

type OutcomeKind int

const (
	OutcomeOther OutcomeKind = iota
	OutcomeApprove
	OutcomeQuestion
	OutcomeNothing
)

// Outcome is what a round's last message says it ended on. It is the
// session's own account, so a runner checks it against GitHub before acting.
type Outcome struct {
	Kind  OutcomeKind
	PrURL string
	SHA   string
	// Question is the question a question round asked, or "": the last
	// paragraph before the question line, on one line, at most
	// maxQuestionNote runes. Session text that may echo the PR: elly and
	// the desktop notification show it escaped.
	Question string
}

// maxQuestionNote caps the question in a note, in runes, the cut marked
// with "…". Behind decide's prefix it must fit ellyMaxNote.
const maxQuestionNote = 200

// ellyMaxNote is the longest note elly accepts, in runes: maxAnnotationNote
// in elly's internal/server/annotations.go.
const ellyMaxNote = 300

var approveLine = regexp.MustCompile(`claude-review approve (https://github\.com/[A-Za-z0-9._-]+/[A-Za-z0-9._-]+/pull/[1-9][0-9]*) --at ([0-9a-f]{40})\b`)

// ReadOutcome reads a round's last message. The question and nothing lines
// count only as the message's last non-empty line; when both are present the question wins,
// since it needs the user either way.
func ReadOutcome(msg string) Outcome {
	lines := strings.Split(strings.TrimSpace(msg), "\n")
	switch strings.TrimSpace(lines[len(lines)-1]) {
	case questionLine:
		return Outcome{Kind: OutcomeQuestion, Question: lastParagraph(lines[:len(lines)-1])}
	case nothingLine:
		return Outcome{Kind: OutcomeNothing}
	}
	if m := approveLine.FindStringSubmatch(msg); m != nil {
		return Outcome{Kind: OutcomeApprove, PrURL: m[1], SHA: m[2]}
	}
	return Outcome{Kind: OutcomeOther}
}

// lastParagraph is the last paragraph of lines that has text, its
// whitespace collapsed to single spaces and control characters dropped (elly
// refuses a note with any), capped at maxQuestionNote runes.
func lastParagraph(lines []string) string {
	end := len(lines)
	for end > 0 && strings.TrimSpace(lines[end-1]) == "" {
		end--
	}
	start := end
	for start > 0 && strings.TrimSpace(lines[start-1]) != "" {
		start--
	}
	text := strings.Join(strings.Fields(strings.Join(lines[start:end], " ")), " ")
	text = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return -1
		}
		return r
	}, text)
	return capRunes(text, maxQuestionNote)
}

// capRunes cuts s to at most n runes, the last one "…" when it cut.
func capRunes(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n-1]) + "…"
}
