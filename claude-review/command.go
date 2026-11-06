package main

import (
	"fmt"
	"strings"
)

// defaultPrompt is the first-round review prompt. {url}, {number}, {repo}
// (owner/repo) and {remote} (the clone's remote that points at the PR's
// repository) are filled in. Every gh command names the repo with -R: gh
// resolves a bare number against the clone's default repository, which in a
// fork-style clone is another repository's PR. The read-only rules are part of the prompt because the clone
// may be someone's live work.
const defaultPrompt = `Perform an adversarial code review of {url}.

Work read-only against this checkout: do not check out, switch branch, stash, reset or edit anything in the working tree. Get the PR itself from GitHub: ` + "`gh pr view {number} -R {repo}`, `gh pr diff {number} -R {repo}`, and `git fetch {remote} pull/{number}/head` followed by `claude-review show {url} <path>` or `claude-review grep {url} <text>`" + ` for file contents at the PR head. Use this repository for context.

` + deferralRule + `

Sign every comment you post with a line containing only ` + "`--claude`" + `.`

// deferralRule keeps fixes in the PR under review. A personal review_prompt
// replaces defaultPrompt and so needs its own copy of it.
const deferralRule = `Treat "this should be a follow-up PR", "out of scope for this PR" and "can be fixed later", in the description, the comments or the code, as findings against this PR when the fix fits in it. Every extra PR costs a human review round, that cost grows faster than the number of PRs, and a deferred fix is debt this PR could have avoided. Do not make such suggestions yourself either. Ask for the fix in this PR, and name a separate PR only when the change truly cannot land here, and say why.`

// cleanRound follows the posting policy, so a custom review_prompt keeps it.
// The summary opens with the verdict because the author reads it with no
// approval beside it: the approval is the user's click, after the round.
const cleanRound = "\n\nOpen the summary with a one-line verdict for the author: `Reviewed at <short sha>: nothing found. Recommended for approval; a human approval follows separately.` when the round finds nothing worth raising, otherwise `Reviewed at <short sha>: N points to address before approval.` (the sha is `git rev-parse FETCH_HEAD`). If the round finds nothing worth raising, end by telling the user they can approve with `! claude-review approve {url} --at <that full sha>`. Never approve yourself."

// rereviewPreface is prepended on every click after the first.
const rereviewPreface = "New review round for {url}: re-check your earlier findings against the new commits and the replies on the PR, then review what changed since. `claude-review diff {url}` prints the commits and the diff since the last round. `claude-review threads {url}` prints every comment, review and inline thread with its id. Answer an existing thread instead of opening a new one, and never post a finding that is already on the PR. Every thread where the author replied gets an answer in that thread with `claude-review post {url} reply --to <id>`: one line naming what settled it (the commit, or the reply) when it is settled, or the point that remains when it is not. The summary may list them too, but it does not replace the reply in the thread. Then resolve each thread you started that you judge settled with `claude-review post {url} resolve --thread <id>`, and leave open every thread you do not judge settled; never resolve a thread someone else started. If there are no new commits and no reply that needs an answer, say so, post nothing, and end with a line containing only `" + nothingLine + "`.\n\n"

const postAuto = "\n\nWhen done, post the findings as inline review comments and, last, one summary review, without asking. Inline and PR-level comments share a budget of 12 per commit: when there are more findings, post the most important inline and list the rest in the summary review, which has its own budget. Post only through `claude-review post {url} inline --path FILE --line N --at SHA --body TEXT` (SHA is the full `git rev-parse FETCH_HEAD` you reviewed), `claude-review post {url} reply --to COMMENT_ID --body TEXT`, `claude-review post {url} comment --body TEXT` and `claude-review post {url} review --at SHA --body TEXT`; those run without a prompt and sign for you. Inline comments and replies wait in a draft review that the summary review sends, so the author gets one notification: post the summary last, also in a round that only replies. Pass the body as inline text."
const postAsk = "\n\nWhen done, present the findings here and wait for approval before posting anything to GitHub."

// bootstrapPrompt is the one headless turn that creates the session under
// the PR-derived id. Neither `claude --bg` nor an interactive `claude`
// honours --session-id for the transcript (observed on 2.1.283), but
// `claude -p` does, and every mode can --resume what -p created.
const bootstrapPrompt = "This session is for reviewing {url}. The review instructions follow in the next message. Reply with one word."

// claudeCmd is the Claude Code binary, resolved on PATH by the shell.
const claudeCmd = "claude"

// Claude Code flags this tool passes in more than one place.
const (
	flagSessionID = "--session-id"
	flagMaxTurns  = "--max-turns"
	flagBg        = "--bg"
)

func fill(pr PR, remote, s string) string {
	return strings.NewReplacer("{url}", pr.URL(), "{number}", fmt.Sprint(pr.Number), "{repo}", pr.Owner+"/"+pr.Repo, "{remote}", remote).Replace(s)
}

// BuildPrompt assembles the prompt for one click. remote is the clone's
// remote for the PR's repository.
func BuildPrompt(cfg Config, pr PR, remote string, resume bool) string {
	body := cfg.ReviewPrompt
	if body == "" {
		body = defaultPrompt
	}
	if cfg.PostWithoutAsking {
		body += postAuto
	} else {
		body += postAsk
	}
	body += cleanRound + questionRound
	if resume {
		body = rereviewPreface + body
	}
	return fill(pr, remote, body)
}

// BootstrapArgs is the headless command that creates the session.
func BootstrapArgs(pr PR) []string {
	return []string{claudeCmd, "-p", flagSessionID, pr.SessionID(), "-n", "PR: " + pr.Slug(), flagMaxTurns, "1", fill(pr, "", bootstrapPrompt)}
}

// ReviewArgs is the command for one click, run after the session exists:
// resume it with the prompt, detached (--bg) or not. resume picks the
// re-review prompt and means the session existed before this click. The
// prompt comes right after the session id: --allowedTools takes a list, and
// a prompt placed after it is read as one more value (verified with
// `claude -p --add-dir /tmp "..."`, same parser, which then complains that no
// prompt was given). No --add-dir: the session reads only its own clone, so
// a prompt injection cannot reach other repositories (revisit with usage).
func ReviewArgs(cfg Config, pr PR, remote string, resume, detached bool) []string {
	args := []string{claudeCmd}
	if detached {
		args = append(args, flagBg)
	}
	args = append(args, "--resume", pr.SessionID(), BuildPrompt(cfg, pr, remote, resume))
	args = append(append(args, "--allowedTools", threadsRule(pr)), readRules(pr)...)
	if cfg.PostWithoutAsking {
		// One rule, pinned to the PR, for a tool with no file-reading flag.
		// gh itself is never pre-approved: a prefix rule cannot constrain
		// flags, and gh has several that read a file into a body.
		args = append(args, postRule(pr))
	}
	return append(append(args, "--disallowedTools"), denyRules...)
}

// AttachArgs opens a session that is already running in the background.
func AttachArgs(pr PR) []string {
	return []string{claudeCmd, "attach", pr.SessionID()}
}
