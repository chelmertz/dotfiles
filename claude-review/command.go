package main

import (
	"fmt"
	"strings"
)

// defaultPrompt is the first-round review prompt. {url}, {number} and
// {remote} (the clone's remote that points at the PR's repository) are
// filled in. The read-only rules are part of the prompt because the clone
// may be someone's live work.
const defaultPrompt = `Perform an adversarial code review of {url}.

Work read-only against this checkout: do not check out, switch branch, stash, reset or edit anything in the working tree. Get the PR itself from GitHub: ` + "`gh pr view {number}`, `gh pr diff {number}`, and `git fetch {remote} pull/{number}/head` followed by `git show FETCH_HEAD:<path>`" + ` for file contents at the PR head. Use this repository for context.

` + deferralRule + `

Sign every comment you post with a line containing only ` + "`--claude`" + `.`

// deferralRule keeps fixes in the PR under review. A personal review_prompt
// replaces defaultPrompt and so needs its own copy of it.
const deferralRule = `Treat "this should be a follow-up PR", "out of scope for this PR" and "can be fixed later", in the description, the comments or the code, as findings against this PR when the fix fits in it. Every extra PR costs a human review round, that cost grows faster than the number of PRs, and a deferred fix is debt this PR could have avoided. Do not make such suggestions yourself either. Ask for the fix in this PR, and name a separate PR only when the change truly cannot land here, and say why.`

// cleanRound follows the posting policy, so a custom review_prompt keeps it.
const cleanRound = "\n\nIf the round finds nothing worth raising, say in the summary which commit you reviewed (`git rev-parse FETCH_HEAD`), then end by telling the user they can approve with `! claude-review approve {url} --at <that full sha>`. Never approve yourself."

// rereviewPreface is prepended on every click after the first.
const rereviewPreface = "New review round for {url}: re-check your earlier findings against the new commits and the replies on the PR, then review what changed since. `claude-review threads {url}` prints every comment, review and inline thread with its id. Answer an existing thread instead of opening a new one, and never post a finding that is already on the PR. If there are no new commits and no new replies, say so and post nothing.\n\n"

const postAuto = "\n\nWhen done, post the findings as inline review comments and one PR-level summary comment, without asking. Post only through `claude-review post {url} inline --path FILE --line N --at SHA --body TEXT` (SHA is the full `git rev-parse FETCH_HEAD` you reviewed), `claude-review post {url} reply --to COMMENT_ID --body TEXT`, `claude-review post {url} comment --body TEXT` and `claude-review post {url} review --body TEXT`; those run without a prompt and sign for you. Pass the body as inline text."
const postAsk = "\n\nWhen done, present the findings here and wait for approval before posting anything to GitHub."

// bootstrapPrompt is the one headless turn that creates the session under
// the PR-derived id. Neither `claude --bg` nor an interactive `claude`
// honours --session-id for the transcript (observed on 2.1.283), but
// `claude -p` does, and every mode can --resume what -p created.
const bootstrapPrompt = "This session is for reviewing {url}. The review instructions follow in the next message. Reply with one word."

// claudeCmd is the Claude Code binary, resolved on PATH by the shell.
const claudeCmd = "claude"

func fill(pr PR, remote, s string) string {
	return strings.NewReplacer("{url}", pr.URL(), "{number}", fmt.Sprint(pr.Number), "{remote}", remote).Replace(s)
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
	return []string{claudeCmd, "-p", "--session-id", pr.SessionID(), "-n", "PR: " + pr.Slug(), "--max-turns", "1", fill(pr, "", bootstrapPrompt)}
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
		args = append(args, "--bg")
	}
	args = append(args, "--resume", pr.SessionID(), BuildPrompt(cfg, pr, remote, resume))
	args = append(args, "--allowedTools", threadsRule(pr))
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
