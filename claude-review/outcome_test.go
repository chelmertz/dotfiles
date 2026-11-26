package main

import (
	"fmt"
	"reflect"
	"regexp"
	"slices"
	"strings"
	"testing"
	"unicode/utf8"

	"pgregory.net/rapid"
)

const sha = "0123456789abcdef0123456789abcdef01234567"

func TestReadOutcome(t *testing.T) {
	cases := []struct {
		name string
		msg  string
		want Outcome
	}{
		{"approve line", "Nothing to raise at " + sha + ".\n\nYou can approve with `! claude-review approve https://github.com/acme/widgets/pull/7 --at " + sha + "`",
			Outcome{Kind: OutcomeApprove, Approvals: []Approval{{"https://github.com/acme/widgets/pull/7", sha}}}},
		{"question line", "One thing only you can answer: is the fee meant to be per booking?\n\n" + questionLine + "\n",
			Outcome{Kind: OutcomeQuestion, Question: "One thing only you can answer: is the fee meant to be per booking?"}},
		{"question: the last paragraph, whitespace collapsed", "Posted 2 comments.\n\nShould retries cap at 3,\n   or follow\tRetry-After?\n \n\n" + questionLine,
			Outcome{Kind: OutcomeQuestion, Question: "Should retries cap at 3, or follow Retry-After?"}},
		{"question: control characters dropped", "Is it \x1b[31mred\x07?\u0085\n" + questionLine,
			Outcome{Kind: OutcomeQuestion, Question: "Is it [31mred?"}},
		{"question line alone has no text", questionLine, Outcome{Kind: OutcomeQuestion}},
		{"findings posted", "Posted 3 inline comments and a summary.", Outcome{Kind: OutcomeOther}},
		{"short sha is not an approve line", "`! claude-review approve https://github.com/acme/widgets/pull/7 --at 0123456`", Outcome{Kind: OutcomeOther}},
		{"empty", "", Outcome{Kind: OutcomeOther}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := ReadOutcome(c.msg); !reflect.DeepEqual(got, c.want) {
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
	if n := utf8.RuneCountInString(note(d)); n > ellyMaxNote {
		t.Errorf("the longest question note is %d runes, over elly's %d", n, ellyMaxNote)
	}
}

// Every marker line a prompt asks a round to end on ("end with a line
// containing only `…`") is one ReadOutcome reads, and every one it reads is
// asked for by some prompt: a prompt and the parser cannot drift apart on a
// marker.
func TestPromptsAndReadOutcomeShareTheMarkers(t *testing.T) {
	asked := regexp.MustCompile("end with a line containing only `([^`]+)`")
	used := map[string]bool{}
	for _, mentionsOnly := range []bool{false, true} {
		for _, auto := range []bool{false, true} {
			for _, resume := range []bool{false, true} {
				p := BuildPrompt(withNoMentions(Config{MentionsOnly: mentionsOnly, PostWithoutAsking: auto}), widgetsPR, "upstream", resume)
				for _, m := range asked.FindAllStringSubmatch(p, -1) {
					if !slices.Contains(markerLines, m[1]) {
						t.Errorf("a prompt asks for %q, which ReadOutcome does not read", m[1])
					}
					used[m[1]] = true
				}
			}
		}
	}
	for _, l := range markerLines {
		if !used[l] {
			t.Errorf("ReadOutcome reads %q, which no prompt asks for", l)
		}
	}
}

// A round's outcome is one ranking over its message, whatever the prose and
// whatever order its ending's markers come in: a question in the ending,
// else any approve line, else nothing in the ending, else other. A marker
// quoted in prose is prose. Which approve lines a message holds is a set:
// it approves this PR at its head exactly when that line is anywhere in it,
// whatever other approve lines (an older head, another PR) stand around it.
func TestPropOutcomeIsTheRankingOfItsEnding(t *testing.T) {
	pr7 := PR{Owner: "acme", Repo: "widgets", Number: 7}
	line := func(url, at string) string {
		return "You can approve with `! claude-review approve " + url + " --at " + at + "`"
	}
	const oldSHA = "fedcba9876543210fedcba9876543210fedcba98"
	approves := []string{line(pr7.URL(), sha), line(pr7.URL(), oldSHA), line("https://github.com/acme/widgets/pull/8", sha)}
	prose := []string{
		"Posted 2 inline comments.",
		"Which retry limit did you mean?",
		"I would have ended with " + questionLine + " here.",
		"Earlier I said " + nothingLine + ".",
		"Head unchanged, no replies.",
	}
	rapid.Check(t, func(rt *rapid.T) {
		paras := rapid.SliceOfN(rapid.SampledFrom(prose), 0, 3).Draw(rt, "prose")
		withApprove := rapid.SliceOfN(rapid.SampledFrom(approves), 0, 3).Draw(rt, "approve lines")
		for i, a := range withApprove {
			at := rapid.IntRange(0, len(paras)).Draw(rt, fmt.Sprintf("approve line %d at", i))
			paras = slices.Insert(slices.Clone(paras), at, a)
		}
		ending := rapid.SliceOfN(rapid.SampledFrom(markerLines), 0, 3).Draw(rt, "ending")
		sep := rapid.SampledFrom([]string{"\n\n", "\n \n"}).Draw(rt, "separator")
		msg := strings.Join(append(slices.Clone(paras), ending...), sep)

		got := ReadOutcome(msg)
		want := OutcomeOther
		switch {
		case slices.Contains(ending, questionLine):
			want = OutcomeQuestion
			if q := ""; len(paras) > 0 {
				q = paras[len(paras)-1]
				if got.Question != q {
					rt.Fatalf("%q: question %q, want the last paragraph %q", msg, got.Question, q)
				}
			}
		case len(withApprove) > 0:
			want = OutcomeApprove
		case slices.Contains(ending, nothingLine):
			want = OutcomeNothing
		}
		if got.Kind != want {
			rt.Fatalf("%q: kind %d, want %d", msg, got.Kind, want)
		}
		head := want == OutcomeApprove && slices.Contains(withApprove, approves[0])
		if got.Approves(pr7, sha) != head {
			rt.Fatalf("%q: approves %s at the head %v, want %v (%+v)", msg, pr7.URL(), !head, head, got.Approvals)
		}
	})
}
