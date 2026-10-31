package main

// secretReadRules deny reads of secret-shaped paths in the clone. An
// unattended round reads PR content an author controls, so a planted
// instruction must not be able to read a key into a comment.
var secretReadRules = []string{
	"Read(**/.env)", "Read(**/.env.*)", "Read(**/*.pem)", "Read(**/*.key)",
	"Read(**/.ssh/**)", "Read(**/.aws/**)", "Read(**/.netrc)",
	"Read(**/*credentials*)", "Read(**/*secret*)",
}

// UnattendedArgs is the command for a round nobody watches. It inherits
// nothing from the user's own Claude Code setup: no user settings (which run
// in auto mode with broad gh and curl rules), no MCP servers, and dontAsk,
// which denies every tool not allowed here instead of asking. What is allowed
// is pinned to this PR. TestUnattendedArgsArePinned fails on any change.
func UnattendedArgs(cfg Config, pr PR, remote string) []string {
	posting := cfg
	posting.PostWithoutAsking = true
	n := itoaPR(pr)
	args := []string{
		claudeCmd, "--bg",
		"--permission-mode", "dontAsk",
		"--setting-sources", "project,local",
		"--strict-mcp-config",
		"--resume", pr.SessionID(), BuildPrompt(posting, pr, remote, true),
		"--allowedTools",
		// Scoped like Read: a bare Grep or Glob searched the parent directory
		// in a live check on 2026-10-31, naming and matching files outside.
		"Read(./**)", "Grep(./**)", "Glob(./**)",
		"Bash(gh pr view " + n + ")", "Bash(gh pr diff " + n + ")",
		"Bash(git fetch " + remote + " pull/" + n + "/head)", "Bash(git show FETCH_HEAD:*)", "Bash(git rev-parse FETCH_HEAD)",
		threadsRule(pr), postRule(pr),
		"--disallowedTools",
	}
	args = append(args, denyRules...)
	args = append(args, secretReadRules...)
	return append(args, "Bash(curl *)", "Bash(wget *)", "WebFetch", "WebSearch")
}

func itoaPR(pr PR) string {
	return fill(pr, "", "{number}")
}
