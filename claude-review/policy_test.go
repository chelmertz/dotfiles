package main

import (
	"slices"
	"strings"
	"testing"
)

// TestUnattendedArgsArePinned fails on any change to what an unattended
// round may do. Loosening it is a decision, so it has to show up here.
func TestUnattendedArgsArePinned(t *testing.T) {
	args := UnattendedArgs(Config{}, widgetsPR, "upstream")
	prompt := BuildPrompt(Config{PostWithoutAsking: true}, widgetsPR, "upstream", true)
	want := []string{
		"claude", "--bg",
		"--permission-mode", "dontAsk",
		"--setting-sources", "project,local",
		"--strict-mcp-config",
		"--resume", widgetsPR.SessionID(), prompt,
		"--allowedTools",
		"Read(./**)", "Grep(./**)", "Glob(./**)",
		"Bash(gh pr view 42)", "Bash(gh pr diff 42)",
		"Bash(git fetch upstream pull/42/head)", "Bash(git show FETCH_HEAD:*)", "Bash(git rev-parse FETCH_HEAD)",
		"Bash(claude-review threads https://github.com/acme/widgets/pull/42)",
		"Bash(claude-review post https://github.com/acme/widgets/pull/42 *)",
		"--disallowedTools",
	}
	want = append(want, denyRules...)
	want = append(want, secretReadRules...)
	want = append(want, "Bash(curl *)", "Bash(wget *)", "WebFetch", "WebSearch")
	if !slices.Equal(args, want) {
		t.Errorf("unattended args changed:\ngot  %q\nwant %q", args, want)
	}
}

func TestUnattendedArgsNeverInheritTheUsersPermissions(t *testing.T) {
	args := strings.Join(UnattendedArgs(Config{PostWithoutAsking: false, Terminal: TerminalSetting{}}, widgetsPR, "origin"), " ")
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
