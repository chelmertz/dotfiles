package main

import (
	"slices"
	"strings"
	"testing"
)

// TestUnattendedArgsArePinned fails on any change to what a round may do.
// Loosening it is a decision, so it has to show up here; and since a
// background session keeps the options of its first launch, any change also
// needs a new policyVersion, which the fingerprint check forces.
func TestUnattendedArgsArePinned(t *testing.T) {
	id := widgetsPR.RoundSessionID()
	flags := []string{
		"--permission-mode", "dontAsk",
		"--setting-sources", "",
		"--strict-mcp-config",
		"--allowedTools",
		"Read(./**)", "Grep(./**)", "Glob(./**)",
		"Read(~/.claude/projects/*/" + id + "/tool-results/**)",
		"Bash(gh pr view 42)", "Bash(gh pr diff 42)",
		"Bash(git fetch upstream pull/42/head)", "Bash(git rev-parse FETCH_HEAD)",
		"Bash(claude-review threads https://github.com/acme/widgets/pull/42)",
		"Bash(claude-review show https://github.com/acme/widgets/pull/42 *)",
		"Bash(claude-review grep https://github.com/acme/widgets/pull/42 *)",
		"Bash(claude-review diff https://github.com/acme/widgets/pull/42)",
		"Bash(claude-review post https://github.com/acme/widgets/pull/42 *)",
		// The persona reviewer prompts, for the first round's persona pass.
		// Claude Code checks a Read rule on every hop of a symlink, and
		// home-manager links the folder in through two store paths.
		"Read(~/.claude/skills/persona-review/**)",
		"Read(//nix/store/*-home-manager-files/.claude/skills/persona-review/**)",
		"Read(//nix/store/*-hm_personareview/**)",
		"Agent",
		"--disallowedTools",
	}
	flags = append(flags, denyRules...)
	flags = append(flags, "Bash(git show *)", "Bash(git diff *)", "Bash(git log *)", "Bash(git grep *)")
	flags = append(flags, secretReadRules...)
	flags = append(flags, "Bash(curl *)", "Bash(wget *)", "WebFetch", "WebSearch")
	if got := PolicyFlags(widgetsPR, "upstream"); !slices.Equal(got, flags) {
		t.Errorf("policy flags changed:\ngot  %q\nwant %q", got, flags)
	}

	for _, resume := range []bool{true, false} {
		prompt := BuildPrompt(Config{PostWithoutAsking: true}, widgetsPR, "upstream", resume) +
			"\n\nThis round runs unattended, and a command it is not allowed is refused, not asked about. Run one command per Bash call: no `;`, `&&`, `|` or redirection, and no `git -C` (the session already runs in the clone). Get the PR head with `git fetch upstream pull/42/head` and `git rev-parse FETCH_HEAD`, never `git ls-remote`. Read the PR's code with `claude-review show https://github.com/acme/widgets/pull/42 <path>`, `claude-review grep https://github.com/acme/widgets/pull/42 <text>` and `claude-review diff https://github.com/acme/widgets/pull/42`; raw `git show`, `git diff`, `git log` and `git grep` are refused. A refused command stays refused: work without it, or end with the question line."
		// The prompt sits before the tool lists, which would swallow it.
		first := append(append([]string{"claude", "--bg"}, flags[:5]...), "--resume", id, prompt)
		first = append(first, flags[5:]...)
		if got := RoundArgs(Config{}, widgetsPR, "upstream", resume); !slices.Equal(got, first) {
			t.Errorf("resume=%v: first launch changed:\ngot  %q\nwant %q", resume, got, first)
		}
		later := []string{"claude", "--bg", "--resume", id, prompt}
		if got := ResumeArgs(Config{}, widgetsPR, "upstream", resume); !slices.Equal(got, later) {
			t.Errorf("resume=%v: later launches must carry no flags:\ngot  %q\nwant %q", resume, got, later)
		}
	}

	if got := policyFingerprint(); got != pinnedPolicyFingerprint {
		t.Errorf("the round policy changed (fingerprint %s, pinned %s): bump policyVersion, so no session saved under the old flags is resumed, and pin the new fingerprint", got, pinnedPolicyFingerprint)
	}
}

// A policy version names a new session: one saved under older flags is
// never resumed, and the old history stays in the old session.
func TestRoundSessionIDIsVersioned(t *testing.T) {
	if got := widgetsPR.RoundSessionID(); got != uuidV5(urlNamespace, widgetsPR.URL()+" round-policy "+policyVersion) || got == widgetsPR.SessionID() {
		t.Errorf("round session id %s", got)
	}
	if policyVersion != "3" {
		t.Errorf("policyVersion %q: the literal here moves with it", policyVersion)
	}
}

func TestUnattendedArgsNeverInheritTheUsersPermissions(t *testing.T) {
	args := strings.Join(RoundArgs(Config{PostWithoutAsking: false, Terminal: TerminalSetting{}}, widgetsPR, "origin", true), " ")
	for _, banned := range []string{"--permission-mode auto", "bypassPermissions", "--setting-sources user", "Bash(gh *)", "Bash(curl *) --"} {
		if strings.Contains(args, banned) {
			t.Errorf("unattended args contain %q", banned)
		}
	}
	for _, secret := range []string{".env", ".ssh", "*.pem", "credentials"} {
		if !strings.Contains(args, secret) {
			t.Errorf("no deny rule for %s", secret)
		}
	}
}
