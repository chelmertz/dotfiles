package main

import (
	"strings"
	"testing"
	"unicode/utf8"
)

const sha = "0123456789abcdef0123456789abcdef01234567"

func TestReadOutcome(t *testing.T) {
	cases := []struct {
		name string
		msg  string
		want Outcome
	}{
		{"approve line", "Nothing to raise at " + sha + ".\n\nYou can approve with `! claude-review approve https://github.com/acme/widgets/pull/7 --at " + sha + "`",
			Outcome{Kind: OutcomeApprove, PrURL: "https://github.com/acme/widgets/pull/7", SHA: sha}},
		{"question line", "One thing only you can answer: is the fee meant to be per booking?\n\n" + questionLine + "\n",
			Outcome{Kind: OutcomeQuestion, Question: "One thing only you can answer: is the fee meant to be per booking?"}},
		{"question: the last paragraph, whitespace collapsed", "Posted 2 comments.\n\nShould retries cap at 3,\n   or follow\tRetry-After?\n \n\n" + questionLine,
			Outcome{Kind: OutcomeQuestion, Question: "Should retries cap at 3, or follow Retry-After?"}},
		{"question: control characters dropped", "Is it \x1b[31mred\x07?\u0085\n" + questionLine,
			Outcome{Kind: OutcomeQuestion, Question: "Is it [31mred?"}},
		{"question line alone has no text", questionLine, Outcome{Kind: OutcomeQuestion}},
		{"findings posted", "Posted 3 inline comments and a summary.", Outcome{Kind: OutcomeOther}},
		{"short sha is not an approve line", "`! claude-review approve https://github.com/acme/widgets/pull/7 --at 0123456`", Outcome{Kind: OutcomeOther}},
		{"question marker quoted mid-message does not count", "I will end with " + questionLine + " if needed.\nPosted 2 comments.", Outcome{Kind: OutcomeOther}},
		{"both: the question wins", "approve with `! claude-review approve https://github.com/acme/widgets/pull/7 --at " + sha + "`\n\nWhich one?\n" + questionLine, Outcome{Kind: OutcomeQuestion, Question: "Which one?"}},
		{"nothing line", "Head unchanged, no replies.\n\n" + nothingLine + "\n", Outcome{Kind: OutcomeNothing}},
		{"nothing line quoted mid-message does not count", "Would have ended with " + nothingLine + ".\nPosted 1 reply.", Outcome{Kind: OutcomeOther}},
		{"empty", "", Outcome{Kind: OutcomeOther}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := ReadOutcome(c.msg); got != c.want {
				t.Errorf("got %+v, want %+v", got, c.want)
			}
		})
	}
}

func TestBuildPromptAsksForTheQuestionLine(t *testing.T) {
	for _, resume := range []bool{false, true} {
		for _, custom := range []string{"", "my own prompt"} {
			p := BuildPrompt(Config{ReviewPrompt: custom}, testPR, "origin", resume)
			if !strings.Contains(p, questionLine) {
				t.Errorf("resume=%v custom=%q: prompt lacks the question line", resume, custom)
			}
		}
	}
}

func TestRereviewPromptAsksForTheNothingLine(t *testing.T) {
	for _, custom := range []string{"", "my own prompt"} {
		if p := BuildPrompt(Config{ReviewPrompt: custom}, testPR, "origin", true); !strings.Contains(p, "end with a line containing only `"+nothingLine+"`") {
			t.Errorf("custom=%q: the re-review prompt lacks the nothing line", custom)
		}
	}
}

// A long question is cut to maxQuestionNote runes, the cut marked, and the
// note it makes still fits what elly accepts.
func TestReadOutcomeCapsTheQuestion(t *testing.T) {
	o := ReadOutcome(strings.Repeat("ö", 500) + "\n" + questionLine)
	if n := utf8.RuneCountInString(o.Question); n != maxQuestionNote || !strings.HasSuffix(o.Question, "…") {
		t.Errorf("got %d runes ending %q", n, o.Question[len(o.Question)-6:])
	}
	if o := ReadOutcome(strings.Repeat("ö", maxQuestionNote) + "\n" + questionLine); strings.HasSuffix(o.Question, "…") {
		t.Error("a question at the cap was cut")
	}
	// elly's maxAnnotationNote, internal/server/annotations.go
	if ellyMaxNote != 300 {
		t.Errorf("ellyMaxNote is %d; elly accepts notes of at most 300 runes", ellyMaxNote)
	}
	d := decide(Round{Head: sha}, o, RoundFacts{State: prOpen, Head: sha}, testPR)
	if n := utf8.RuneCountInString(d.Annotation.Note); n > ellyMaxNote {
		t.Errorf("the longest question note is %d runes, over elly's %d", n, ellyMaxNote)
	}
}
