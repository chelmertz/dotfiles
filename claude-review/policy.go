package main

// secretReadRules deny reads of secret-shaped paths in the clone. An
// unattended round reads PR content an author controls, so a planted
// instruction must not be able to read a key into a comment.
var secretReadRules = []string{
	"Read(**/.env)", "Read(**/.env.*)", "Read(**/*.pem)", "Read(**/*.key)",
	"Read(**/.ssh/**)", "Read(**/.aws/**)", "Read(**/.netrc)",
	"Read(**/*credentials*)", "Read(**/*secret*)",
}

// unattendedRules follows the prompt of every unattended round. The first
// real rounds (2026-11-01) lost 22 of 61 tool calls to refused chains,
// `git -C` and `git ls-remote`, which match none of the pinned rules.
const unattendedRules = "\n\nThis round runs unattended, and a command it is not allowed is refused, not asked about. Run one command per Bash call: no `;`, `&&`, `|` or redirection, and no `git -C` (the session already runs in the clone). Get the PR head with `git fetch {remote} pull/{number}/head` and `git rev-parse FETCH_HEAD`, never `git ls-remote`. Read the PR's code with `claude-review show {url} <path>`, `claude-review grep {url} <text>` and `claude-review diff {url}`; raw `git show`, `git diff`, `git log` and `git grep` are refused. A refused command stays refused: work without it, or end with the question line."

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
		"--resume", pr.SessionID(), BuildPrompt(posting, pr, remote, true) + fill(pr, remote, unattendedRules),
		"--allowedTools",
		// Scoped like Read: a bare Grep or Glob searched the parent directory
		// in a live check on 2026-10-31, naming and matching files outside.
		"Read(./**)", "Grep(./**)", "Glob(./**)",
		// Claude Code saves a large tool output under the session and has
		// the model Read it back; without this, a big `gh pr diff` is lost.
		// Scoped to this session: checked live 2026-11-01, another id's
		// file is refused.
		"Read(~/.claude/projects/*/" + pr.SessionID() + "/tool-results/**)",
		"Bash(gh pr view " + n + ")", "Bash(gh pr diff " + n + ")",
		"Bash(git fetch " + remote + " pull/" + n + "/head)", "Bash(git rev-parse FETCH_HEAD)",
		threadsRule(pr),
	}
	args = append(args, readRules(pr)...)
	args = append(append(args, postRule(pr), "--disallowedTools"), denyRules...)
	// Not left to dontAsk: Claude Code lets some git reads through without
	// a rule (`git status` did on 2026-10-31), and these four take --output.
	args = append(args, "Bash(git show *)", "Bash(git diff *)", "Bash(git log *)", "Bash(git grep *)")
	args = append(args, secretReadRules...)
	return append(args, "Bash(curl *)", "Bash(wget *)", "WebFetch", "WebSearch")
}

func itoaPR(pr PR) string {
	return fill(pr, "", "{number}")
}
