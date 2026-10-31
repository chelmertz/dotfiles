package main

import (
	"strings"
	"testing"
)

var testPR = widgetsPR

func TestBuildPrompt_UsesTheMatchingRemote(t *testing.T) {
	got := BuildPrompt(Config{}, testPR, upstream, false)
	if !strings.Contains(got, "git fetch "+upstream+" pull/42/head") || strings.Contains(got, "fetch origin") {
		t.Errorf("remote not substituted:\n%s", got)
	}
}

func TestBuildPrompt_FirstRoundDefault(t *testing.T) {
	got := BuildPrompt(Config{}, testPR, "origin", false)
	for _, want := range []string{
		"adversarial code review of https://github.com/acme/widgets/pull/42.",
		"gh pr diff 42",
		"git fetch origin pull/42/head",
		"do not check out, switch branch, stash, reset or edit",
		"`--claude`",
		"wait for approval before posting",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("prompt lacks %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "{") {
		t.Errorf("unfilled placeholder:\n%s", got)
	}
	if strings.HasPrefix(got, "New review round") || strings.Contains(got, "without asking") {
		t.Errorf("first round, ask mode, got:\n%s", got)
	}
}

func TestBuildPrompt_RereviewAndAuto(t *testing.T) {
	got := BuildPrompt(Config{PostWithoutAsking: true}, testPR, "origin", true)
	if !strings.HasPrefix(got, "New review round for https://github.com/acme/widgets/pull/42:") {
		t.Errorf("missing preface:\n%s", got)
	}
	if !strings.Contains(got, "without asking.") || !strings.Contains(got, "as inline text.") || strings.Contains(got, "wait for approval") {
		t.Errorf("auto mode suffix wrong:\n%s", got)
	}
	if !strings.Contains(got, "adversarial code review of https://github.com/acme/widgets/pull/42") {
		t.Errorf("body must survive the policy append:\n%s", got)
	}
	if !strings.Contains(got, "claude-review post https://github.com/acme/widgets/pull/42 inline --path FILE --line N --at SHA") || strings.Contains(got, "{") {
		t.Errorf("auto mode must name the pre-approved forms, filled in:\n%s", got)
	}
}

func TestBuildPrompt_CustomPromptKeepsPlaceholdersAndPolicy(t *testing.T) {
	got := BuildPrompt(Config{ReviewPrompt: "Look at {url} ({number}) via {remote}"}, testPR, upstream, false)
	if !strings.HasPrefix(got, "Look at https://github.com/acme/widgets/pull/42 (42) via "+upstream) {
		t.Errorf("custom prompt not used or not filled:\n%s", got)
	}
	if !strings.Contains(got, "wait for approval") {
		t.Errorf("policy sentence must still be appended:\n%s", got)
	}
	// The clean-round and question paths, too: a custom prompt must not lose
	// them, and the question line comes last.
	if !strings.Contains(got, "they can approve with `! claude-review approve https://github.com/acme/widgets/pull/42 --at <that full sha>`. Never approve yourself.") {
		t.Errorf("clean-round sentence missing:\n%s", got)
	}
	if !strings.HasSuffix(got, "end with a line containing only `claude-review: question for the user`.") {
		t.Errorf("question sentence missing or not last:\n%s", got)
	}
}

func TestBootstrapArgs(t *testing.T) {
	got := BootstrapArgs(testPR)
	want := []string{claudeBin, "-p", "--session-id", testPR.SessionID(), "-n", "PR: acme/widgets#42", "--max-turns", "1",
		"This session is for reviewing https://github.com/acme/widgets/pull/42. The review instructions follow in the next message. Reply with one word."}
	if strings.Join(got, "\x00") != strings.Join(want, "\x00") {
		t.Errorf("got  %q\nwant %q", got, want)
	}
}

func TestReviewArgs_NoAddDir(t *testing.T) {
	got := ReviewArgs(Config{GitRepoRoots: []string{codeRoot}}, testPR, "origin", false, false)
	for _, a := range got {
		if a == "--add-dir" || a == codeRoot {
			t.Errorf("roots must not be passed to the session: %q", got)
		}
	}
}

func TestReviewArgs_FirstRoundWithTerminal(t *testing.T) {
	cfg := Config{GitRepoRoots: []string{codeRoot, "/srv"}}
	got := ReviewArgs(cfg, testPR, "origin", false, false)
	want := []string{claudeBin, resume, testPR.SessionID(), BuildPrompt(cfg, testPR, "origin", false),
		"--allowedTools", threadsAllow,
		"--disallowedTools", "Bash(gh api *)", "Bash(gh pr comment *)", "Bash(gh pr review *)", "Bash(gh pr edit *)",
		"Bash(gh pr merge *)", "Bash(gh pr close *)", "Bash(gh issue comment *)",
		"Bash(claude-review approve *)"}
	if strings.Join(got, "\x00") != strings.Join(want, "\x00") {
		t.Errorf("got  %q\nwant %q", got, want)
	}
}

func TestReviewArgs_RereviewDetachedAuto(t *testing.T) {
	cfg := Config{GitRepoRoots: []string{codeRoot}, PostWithoutAsking: true}
	got := ReviewArgs(cfg, testPR, "origin", true, true)
	want := []string{claudeBin, "--bg", resume, testPR.SessionID(), BuildPrompt(cfg, testPR, "origin", true),
		"--allowedTools", threadsAllow,
		"Bash(claude-review post https://github.com/acme/widgets/pull/42 *)",
		"--disallowedTools", "Bash(gh api *)", "Bash(gh pr comment *)", "Bash(gh pr review *)", "Bash(gh pr edit *)",
		"Bash(gh pr merge *)", "Bash(gh pr close *)", "Bash(gh issue comment *)",
		"Bash(claude-review approve *)"}
	if strings.Join(got, "\x00") != strings.Join(want, "\x00") {
		t.Errorf("got  %q\nwant %q", got, want)
	}
}

// The prompt must sit right after the session id: --allowedTools is a list
// flag that would swallow a trailing prompt.
func TestReviewArgs_PromptDirectlyFollowsSessionID(t *testing.T) {
	cfg := Config{GitRepoRoots: []string{codeRoot}, PostWithoutAsking: true}
	for _, resume := range []bool{false, true} {
		for _, detached := range []bool{false, true} {
			got := ReviewArgs(cfg, testPR, "origin", resume, detached)
			i := indexOf(got, testPR.SessionID())
			if i < 0 || got[i+1] != BuildPrompt(cfg, testPR, "origin", resume) {
				t.Errorf("resume=%v detached=%v: prompt not after the id: %q", resume, detached, got)
			}
			for _, w := range got[i+2:] {
				if strings.Contains(w, "code review") {
					t.Errorf("prompt text after the list flags: %q", got)
				}
			}
		}
	}
}

func indexOf(xs []string, x string) int {
	for i, v := range xs {
		if v == x {
			return i
		}
	}
	return -1
}

func TestAttachArgs(t *testing.T) {
	if got := strings.Join(AttachArgs(testPR), " "); got != "claude attach "+testPR.SessionID() {
		t.Errorf("got %q", got)
	}
}

// A deferral costs a human review round per extra PR; the built-in prompt
// makes it a finding and forbids the session from proposing one itself.
func TestBuildPrompt_DeferralIsAFinding(t *testing.T) {
	got := BuildPrompt(Config{}, testPR, upstream, false)
	for _, want := range []string{
		`"this should be a follow-up PR", "out of scope for this PR" and "can be fixed later"`,
		"as findings against this PR when the fix fits in it",
		"Every extra PR costs a human review round",
		"grows faster than the number of PRs",
		"debt this PR could have avoided",
		"Ask for the fix in this PR",
		"name a separate PR only when the change truly cannot land here, and say why",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("prompt lacks %q:\n%s", want, got)
		}
	}
	if custom := BuildPrompt(Config{ReviewPrompt: "Look at {url}"}, testPR, upstream, false); strings.Contains(custom, "follow-up PR") {
		t.Errorf("a custom review_prompt replaces the default, deferral rule included:\n%s", custom)
	}
}
