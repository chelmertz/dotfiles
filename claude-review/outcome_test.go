package main

import (
	"strings"
	"testing"
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
			Outcome{Kind: OutcomeQuestion}},
		{"findings posted", "Posted 3 inline comments and a summary.", Outcome{Kind: OutcomeOther}},
		{"short sha is not an approve line", "`! claude-review approve https://github.com/acme/widgets/pull/7 --at 0123456`", Outcome{Kind: OutcomeOther}},
		{"question marker quoted mid-message does not count", "I will end with " + questionLine + " if needed.\nPosted 2 comments.", Outcome{Kind: OutcomeOther}},
		{"both: the question wins", "approve with `! claude-review approve https://github.com/acme/widgets/pull/7 --at " + sha + "`\n" + questionLine, Outcome{Kind: OutcomeQuestion}},
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
