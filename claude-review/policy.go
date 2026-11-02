package main

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"
)

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

// policyVersion names the round policy a PR's background session was first
// launched under. Claude Code keeps a background session's options from its
// first launch: a later `--bg --resume` with flags forks it (2.1.314), so
// every later round resumes without flags and runs under those saved
// options. A policy change therefore needs a new session, and the version
// is part of the session id (PR.RoundSessionID). Bump it whenever
// PolicyFlags changes; TestUnattendedArgsArePinned fails until
// pinnedPolicyFingerprint matches the new flags.
const policyVersion = "2"

// pinnedPolicyFingerprint is policyFingerprint() for policyVersion.
const pinnedPolicyFingerprint = "698dc36792da235f"

// PolicyFlags is the round policy. It inherits nothing from the user's own
// Claude Code setup: no settings file at all (the user's run in auto mode
// with broad gh and curl rules; a clone's local file held
// `Bash(python3:*)`, and its committed one can add hooks, which are shell
// commands its authors wrote), no MCP servers, and dontAsk, which denies
// every tool not allowed here instead of asking. What is allowed is pinned
// to this PR. Agent is allowed for the first round's persona pass: a
// subagent is held to the same allow list (checked live 2026-11-02, it was
// refused a `touch`). The persona-review skill itself does not load
// without a settings source ("Unknown skill", 2.1.314), and no source is
// added for it. Claude Code saves these flags with the session on its
// first background launch, in this order (jobs/<short>/state.json
// respawnFlags), and every later round is checked against them.
func PolicyFlags(pr PR, remote string) []string {
	return policyFlags(pr, remote, pr.RoundSessionID())
}

func policyFlags(pr PR, remote, id string) []string {
	n := itoaPR(pr)
	args := []string{
		"--permission-mode", "dontAsk",
		"--setting-sources", "",
		"--strict-mcp-config",
		"--allowedTools",
		// Scoped like Read: a bare Grep or Glob searched the parent directory
		// in a live check on 2026-10-31, naming and matching files outside.
		"Read(./**)", "Grep(./**)", "Glob(./**)",
		// Claude Code saves a large tool output under the session and has
		// the model Read it back; without this, a big `gh pr diff` is lost.
		// Scoped to this session: checked live 2026-11-01, another id's
		// file is refused.
		"Read(~/.claude/projects/*/" + id + "/tool-results/**)",
		"Bash(gh pr view " + n + ")", "Bash(gh pr diff " + n + ")",
		"Bash(git fetch " + remote + " pull/" + n + "/head)", "Bash(git rev-parse FETCH_HEAD)",
		threadsRule(pr),
	}
	args = append(args, readRules(pr)...)
	args = append(append(args, postRule(pr), "Agent", "--disallowedTools"), denyRules...)
	// Not left to dontAsk: Claude Code lets some git reads through without
	// a rule (`git status` did on 2026-10-31), and these four take --output.
	args = append(args, "Bash(git show *)", "Bash(git diff *)", "Bash(git log *)", "Bash(git grep *)")
	args = append(args, secretReadRules...)
	return append(args, "Bash(curl *)", "Bash(wget *)", "WebFetch", "WebSearch")
}

// policyHead is how many leading PolicyFlags come before the tool lists;
// the prompt goes between them, since a list flag would swallow it.
const policyHead = 5

// policyFingerprint is a hash of the policy for a placeholder PR and id.
func policyFingerprint() string {
	sum := sha256.Sum256([]byte(strings.Join(policyFlags(PR{Owner: "o", Repo: "r", Number: 1}, "{remote}", "{id}"), "\x00")))
	return hex.EncodeToString(sum[:8])
}

// RoundPrompt is a round's prompt: posting without asking, plus the rules
// for a round nobody watches. resume picks the re-review prompt.
func RoundPrompt(cfg Config, pr PR, remote string, resume bool) string {
	posting := cfg
	posting.PostWithoutAsking = true
	return BuildPrompt(posting, pr, remote, resume) + fill(pr, remote, unattendedRules)
}

// RoundArgs is the first background launch of the PR's round session,
// tick's or a click's: the policy flags, saved with the session from then on.
func RoundArgs(cfg Config, pr PR, remote string, resume bool) []string {
	flags := PolicyFlags(pr, remote)
	args := append([]string{claudeCmd, flagBg}, flags[:policyHead]...)
	args = append(args, "--resume", pr.RoundSessionID(), RoundPrompt(cfg, pr, remote, resume))
	return append(args, flags[policyHead:]...)
}

// ResumeArgs is every later round: no flags, so Claude Code continues the
// session under its saved options instead of forking it.
func ResumeArgs(cfg Config, pr PR, remote string, resume bool) []string {
	return []string{claudeCmd, flagBg, "--resume", pr.RoundSessionID(), RoundPrompt(cfg, pr, remote, resume)}
}

// RoundBootstrapArgs creates the round session under its fixed id, under
// the policy's permission flags: `claude --bg` cannot be given an id.
func RoundBootstrapArgs(pr PR) []string {
	return append([]string{claudeCmd, "-p", flagSessionID, pr.RoundSessionID(), "-n", "PR: " + pr.Slug(), flagMaxTurns, "1"},
		append(PolicyFlags(pr, "")[:policyHead], fill(pr, "", bootstrapPrompt))...)
}

func itoaPR(pr PR) string {
	return fill(pr, "", "{number}")
}
