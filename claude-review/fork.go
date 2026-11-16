package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
)

// forkPrompt is what the fork is asked once it opens. The round's question
// is in the context it was forked with.
const forkPrompt = `You are now an interactive fork of the review round above, and the user is watching. The unattended round's command rules no longer apply: anything not allowed asks the user. Answer the round's question for the user.

When it needs production data, query Metabase read-only: read ~/.claude/skills/metabase-sql/SKILL.md first, then run one query per Bash call with the SQL inline, so the user sees it before approving:

~/.claude/skills/metabase-sql/mb-sql <database> - <<'SQL'
SELECT ...
SQL

Aggregate in SQL. Never select columns that name people (names, emails, phone numbers) unless the user asks.

Then propose where the answer belongs on {url}: an inline comment on the line the finding is about, a reply in the thread the round started, or nothing when the answer clears the concern. Quote figures as aggregates with the date and the query. Post with ` + "`claude-review post {url} ...`" + ` once the user agrees; it asks before posting.`

// forkDenyRules close the ways out of a session that may hold prod rows.
// Everything not listed anywhere asks, since the fork loads no settings.
var forkDenyRules = []string{
	"Bash(curl *)", "Bash(wget *)", "WebFetch", "WebSearch",
	"Bash(git push *)", "Bash(gh gist *)", "Bash(op *)", "Bash(printenv *)", "Bash(env *)",
	// mb-sql reads its host and key reference from the environment.
	"Bash(MB_URL=*)", "Bash(MB_API_KEY*)",
	// A query file in the clone would be read by the next unattended round.
	"Write(./**)", "Edit(./**)",
}

// metabaseReadRules let the fork read the skill. Claude Code checks a Read
// rule on every hop of a symlink, and home-manager links the skill folder
// through two store paths before the clone (2026-11-16), so every hop of
// the folder's chain, and the folder SKILL.md resolves into, is allowed,
// found at launch rather than named here.
func metabaseReadRules(home string) []string {
	rules := []string{"Read(~/.claude/skills/metabase-sql/**)"}
	add := func(dir string) {
		if r := "Read(/" + dir + "/**)"; !slices.Contains(rules, r) {
			rules = append(rules, r)
		}
	}
	p := filepath.Join(home, ".claude", "skills", "metabase-sql")
	for range 8 {
		t, err := os.Readlink(p)
		if err != nil {
			break
		}
		if !filepath.IsAbs(t) {
			t = filepath.Join(filepath.Dir(p), t)
		}
		p = filepath.Clean(t)
		add(p)
	}
	if real, err := filepath.EvalSymlinks(filepath.Join(home, ".claude", "skills", "metabase-sql", "SKILL.md")); err == nil {
		add(filepath.Dir(real))
	}
	return rules
}

// ForkArgs opens an interactive fork of pr's round session, locked down: no
// settings source, so the user's broad allow list does not apply; no MCP
// servers; default mode, so mb-sql and claude-review post ask every time.
func ForkArgs(pr PR, remote string, skillRules []string) []string {
	n := itoaPR(pr)
	args := []string{claudeCmd, flagResume, pr.RoundSessionID().String(), "--fork-session",
		flagSettings, "", flagStrictMCP, flagMode, "default",
		fill(pr, remote, forkPrompt), flagAllowed,
		"Read(./**)", "Grep(./**)", "Glob(./**)",
		"Bash(gh pr view " + n + " -R " + pr.Owner + "/" + pr.Repo + ")", "Bash(gh pr diff " + n + " -R " + pr.Owner + "/" + pr.Repo + ")",
		threadsRule(pr),
	}
	args = append(append(args, readRules(pr)...), skillRules...)
	args = append(append(args, flagDenied), denyRules...)
	args = append(args, "Bash(git show *)", "Bash(git diff *)", "Bash(git log *)", "Bash(git grep *)")
	return append(append(args, secretReadRules...), forkDenyRules...)
}

func forkLink(pr PR) Link { return Link{Label: "Fork with prod data", URL: verbURL(pr, "fork")} }

// fork opens the fork. A link can come from anywhere a click can, so the
// ledger decides, not the link: the PR is in the review repos, its row holds
// the fork link tick wrote for a question, and no round is working.
func fork(d Deps, cfg Config, pr PR) int {
	if !repoAllowed(pr.Owner, pr.Repo, cfg.ReviewRepos) {
		return fail(d, fmt.Errorf("claude-review: %s is not in review_repos", pr.URL()))
	}
	unlock, err := d.Lock(pr.SessionID().String())
	if errors.Is(err, errLocked) {
		return fail(d, fmt.Errorf("claude-review: %s is being started; try again in a moment", pr.URL()))
	}
	if err != nil {
		return fail(d, fmt.Errorf("claude-review: locking %s: %w", pr.URL(), err))
	}
	defer unlock()
	l, err := OpenLedger(LedgerPath(d.StateDir))
	if err != nil {
		return fail(d, err)
	}
	a, ok, err := l.Annotation(pr)
	_ = l.Close()
	if err != nil {
		return fail(d, err)
	}
	if !ok || !slices.Contains(a.Links, forkLink(pr)) {
		return fail(d, fmt.Errorf("claude-review: no question from a round is waiting on %s", pr.URL()))
	}
	if d.Sessions.JobState(pr.RoundSessionID()) == "working" {
		return fail(d, fmt.Errorf("claude-review: a round is working on %s; fork it when it ends", pr.URL()))
	}
	dir, remote, err := d.FindRepo(cfg.GitRepoRoots, cfg.ClonePath, pr)
	if err != nil {
		return fail(d, err)
	}
	if err := openTerminal(d, cfg, dir, [][]string{ForkArgs(pr, remote, metabaseReadRules(d.Home))}, pr); err != nil {
		return fail(d, err)
	}
	return 0
}
