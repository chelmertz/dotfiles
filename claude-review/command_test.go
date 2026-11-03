package main

import (
	"os"
	"path/filepath"
	"slices"
	"strconv"
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
		"gh pr diff 42 -R acme/widgets",
		"gh pr view 42 -R acme/widgets",
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
	// The author reads the summary, not the session: it opens with the
	// verdict, and a clean one says the approval follows from a human.
	if !strings.Contains(got, "`Reviewed at <short sha>: nothing found. Recommended for approval; a human approval follows separately.`") ||
		!strings.Contains(got, "`Reviewed at <short sha>: N points to address before approval.`") {
		t.Errorf("summary verdict lines missing:\n%s", got)
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
		"Bash(claude-review show https://github.com/acme/widgets/pull/42 *)",
		"Bash(claude-review grep https://github.com/acme/widgets/pull/42 *)",
		"Bash(claude-review diff https://github.com/acme/widgets/pull/42)",
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
		"Bash(claude-review show https://github.com/acme/widgets/pull/42 *)",
		"Bash(claude-review grep https://github.com/acme/widgets/pull/42 *)",
		"Bash(claude-review diff https://github.com/acme/widgets/pull/42)",
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

// The configured review_prompt replaces defaultPrompt, so it must name the
// same read commands: raw `git show` is refused in unattended rounds, and a
// prompt that asks for it stalls the round.
func TestConfiguredPromptReadsLikeTheDefault(t *testing.T) {
	b, err := os.ReadFile(filepath.Join("..", "nix", "home.nix"))
	if err != nil {
		t.Skip("nix/home.nix not beside this module")
	}
	const reads = "followed by `claude-review show {url} <path>` or `claude-review grep {url} <text>` for file contents at the PR head."
	if !strings.Contains(defaultPrompt, reads) || !strings.Contains(string(b), reads) {
		t.Errorf("defaultPrompt and nix/home.nix's review_prompt must both say %q", reads)
	}
	if strings.Contains(string(b), "git show") {
		t.Error("nix/home.nix's review_prompt still asks for git show")
	}
}

// An author's reply in a thread a round started must be answered in that
// thread: settled only in the summary, the reply stays the last word on
// GitHub and the thread reads, and counts in elly, as unanswered.
func TestRereviewAnswersEveryRepliedThreadInIt(t *testing.T) {
	for _, custom := range []string{"", "my own prompt"} {
		p := BuildPrompt(Config{ReviewPrompt: custom, PostWithoutAsking: true}, testPR, "origin", true)
		want := "Every thread where the author replied gets an answer in that thread with `claude-review post https://github.com/acme/widgets/pull/42 reply --to <id>`: one line naming what settled it (the commit, or the reply) when it is settled, or the point that remains when it is not. The summary may list them too, but it does not replace the reply in the thread."
		if !strings.Contains(p, want) {
			t.Errorf("custom=%q: the re-review prompt does not ask for an answer in each replied thread:\n%s", custom, p)
		}
	}
}

// A round that confirms a fix resolves the thread, or the user's open
// threads keep a clean round from earning an Approve link; it never
// resolves a thread it still disputes, nor one it did not start.
func TestRereviewResolvesSettledThreadsOfOurs(t *testing.T) {
	p := BuildPrompt(Config{PostWithoutAsking: true}, testPR, "origin", true)
	want := "Then resolve each thread you started that you judge settled with `claude-review post https://github.com/acme/widgets/pull/42 resolve --thread <id>`, and leave open every thread you do not judge settled; never resolve a thread someone else started."
	if !strings.Contains(p, want) {
		t.Errorf("the re-review prompt does not ask to resolve settled threads:\n%s", p)
	}
}

// Skills do not load in a round (no settings source), so the configured
// prompt has the round read the persona prompts itself, from the one folder
// the policy lets it read, and start the reviewers with Agent.
func TestConfiguredPromptReadsThePersonasItself(t *testing.T) {
	b, err := os.ReadFile(filepath.Join("..", "nix", "home.nix"))
	if err != nil {
		t.Skip("nix/home.nix not beside this module")
	}
	nix := string(b)
	if strings.Contains(nix, "run the persona-review skill") {
		t.Error("nix/home.nix's review_prompt still invokes the skill, which does not load in a round")
	}
	const reads = "read `~/.claude/skills/persona-review/SKILL.md` and the persona prompts in `~/.claude/skills/persona-review/personas/`, then start one reviewer per persona with the Agent tool"
	if !strings.Contains(nix, reads) {
		t.Errorf("nix/home.nix's review_prompt must say %q", reads)
	}
	if !slices.Contains(PolicyFlags(widgetsPR, "upstream"), "Read(~/.claude/skills/persona-review/**)") {
		t.Error("the policy does not let a round read the folder the prompt names")
	}
}

// A round that does not know its inline budget spends it before its
// summary; the prompt states the limit and where the rest goes.
func TestPostAutoStatesTheInlineLimit(t *testing.T) {
	if !strings.Contains(postAuto, strconv.Itoa(newPostLimit)) || !strings.Contains(postAuto, "own budget") {
		t.Errorf("postAuto does not state the inline limit and the summary's own budget: %q", postAuto)
	}
}

// gh resolves a bare `gh pr view 9` against the clone's default repository,
// which in a fork-style clone is not the PR's: the session would read
// another repository's PR 9. Prompt and round policy both name the repo.
func TestRoundsNameThePRsRepositoryToGh(t *testing.T) {
	fork := PR{Owner: "me", Repo: "widgets", Number: 9}
	for _, text := range []string{BuildPrompt(Config{}, fork, "origin", false), RoundPrompt(Config{}, fork, "origin", true)} {
		if strings.Contains(text, "gh pr view 9`") || strings.Contains(text, "gh pr diff 9`") || !strings.Contains(text, "`gh pr view 9 -R me/widgets`") {
			t.Errorf("a gh command without -R me/widgets:\n%s", text)
		}
	}
	flags := PolicyFlags(fork, "origin")
	for _, rule := range []string{"Bash(gh pr view 9 -R me/widgets)", "Bash(gh pr diff 9 -R me/widgets)"} {
		if !slices.Contains(flags, rule) {
			t.Errorf("the round policy lacks %s", rule)
		}
	}
	if slices.Contains(flags, "Bash(gh pr view 9)") || slices.Contains(flags, "Bash(gh pr diff 9)") {
		t.Error("the round policy still allows gh without -R")
	}
	if got := fill(fork, "origin", "{repo}"); got != "me/widgets" {
		t.Errorf("{repo} is %q", got)
	}
}
