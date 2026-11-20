package main

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"

	"github.com/chelmertz/dotfiles/claude-review/ids"
)

// forkPrompt is what the fork is asked once it opens: Open Claude on a PR
// whose review has ended. A question the review left is in the context it
// was forked with; otherwise the user came to talk.
const forkPrompt = `You are now an interactive fork of the review above, and the user is watching. The unattended review's command rules no longer apply: anything not allowed asks the user. If the review ended with a question for the user, answer it. Otherwise say in two sentences where the review left {url}, then wait for the user.

When it needs production data, query Metabase read-only: read ~/.claude/skills/metabase-sql/SKILL.md first, then run one query per Bash call with the SQL inline, so the user sees it before approving:

~/.claude/skills/metabase-sql/mb-sql <database> - <<'SQL'
SELECT ...
SQL

Aggregate in SQL. Never select columns that name people (names, emails, phone numbers) unless the user asks.

Then propose where the answer belongs on {url}: an inline comment on the line the finding is about, a reply in the thread the round started, or nothing when the answer clears the concern. Quote figures as aggregates with the date and the query. Post with ` + "`claude-review post {url} ...`" + ` once the user agrees; it asks before posting.`

// forkDenyRules close the ways out of a session that may hold prod rows.
// Everything not listed anywhere asks, since the fork loads no settings.
// forkEnvUnset runs the fork without the SSH agent: it never fetches, and
// without the socket a git push that slips past forkDenyRules cannot
// authenticate. Set at the command, so a shell rc that exports the socket
// again does not undo it.
var forkEnvUnset = []string{"env", "-u", "SSH_AUTH_SOCK"}

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
func ForkArgs(pr PR, session, forkID ids.SessionID, remote string, skillRules []string) []string {
	n := itoaPR(pr)
	args := []string{claudeCmd, flagResume, session.String(), "--fork-session", flagSessionID, forkID.String(),
		flagSettings, "", flagStrictMCP, flagMode, "default",
		fill(pr, remote, forkPrompt), flagAllowed,
		"Read(./**)", "Grep(./**)", "Glob(./**)",
		"Bash(gh pr view " + n + " -R " + pr.Owner + "/" + pr.Repo + ")", "Bash(gh pr diff " + n + " -R " + pr.Owner + "/" + pr.Repo + ")",
		threadsRule(pr),
	}
	args = append(append(args, readRules(pr)...), skillRules...)
	args = append(append(args, flagDenied), denyRules...)
	args = append(args, "Bash(git show *)", "Bash(git diff *)", "Bash(git log *)", "Bash(git grep *)")
	return append(append(append(args, secretReadRules...), protectedRules()...), forkDenyRules...)
}

// openFork opens the fork, under the PR's lock the caller holds. A link can
// come from anywhere a click can, so the ledger decides, not the link: the
// PR is in the review repos, a review of it has ended (so its session
// exists), and nothing is working on it.
func openFork(d Deps, cfg Config, pr PR) int {
	if !repoAllowed(pr.Owner, pr.Repo, cfg.ReviewRepos) {
		return fail(d, fmt.Errorf("claude-review: %s is not in review_repos", pr.URL()))
	}
	l, err := OpenLedger(LedgerPath(d.StateDir))
	if err != nil {
		return fail(d, err)
	}
	defer l.Close()
	ended, err := forkGate(l, pr)
	if err != nil {
		return fail(d, err)
	}
	if !ended {
		tell(d, "Claude has not looked at "+pr.URL()+" yet; start a review first")
		return 0
	}
	if windowOpen(d.Sessions, l, pr) {
		return fail(d, fmt.Errorf("claude-review: %s is already open in a window", pr.URL()))
	}
	// The newest round's session, as its row recorded it.
	session, err := l.RoundSession(pr)
	if err != nil {
		return fail(d, err)
	}
	if d.Sessions.JobState(session) == "working" {
		return fail(d, fmt.Errorf("claude-review: a round is working on %s; fork it when it ends", pr.URL()))
	}
	dir, remote, err := d.FindRepo(cfg.GitRepoRoots, cfg.ClonePath, pr)
	if err != nil {
		return fail(d, err)
	}
	// Each fork gets a fresh id, recorded before launch: a window under it
	// holds no session with the round's id, so only this record finds it.
	forkID := ids.NewSession()
	if err := l.SetForkSession(pr, forkID); err != nil {
		return fail(d, err)
	}
	if err := openTerminal(d, cfg, dir, [][]string{append(append([]string{}, forkEnvUnset...), ForkArgs(pr, session, forkID, remote, metabaseReadRules(d.Home))...)}, pr); err != nil {
		return fail(d, err)
	}
	return 0
}

// forkOpenStates are the stored states Open Claude may fork from: no
// worker that may still be alive (running, starting, a blocked question)
// and no Approve the fork would run beside.
var forkOpenStates = map[stateKey]bool{
	{stIdle, ""}: true, {stHandled, ""}: true, {stStuck, ""}: true,
	{stHeld, kindFresh}: true, {stAwaiting, kindDrafts}: true, {stAwaiting, kindDismiss}: true,
}

// forkGate decides from the ledger, never the link, whether pr may be
// forked. ended is false when no review of it has ended, so its session
// may not exist; err refuses.
func forkGate(l *Ledger, pr PR) (ended bool, err error) {
	if _, ok, err := l.Acceptance(pr); err != nil || !ok {
		return false, err
	}
	if _, _, ok, err := l.LastSettled(pr); err != nil || !ok {
		return false, err
	}
	s, ok, err := l.PRState(pr)
	if err != nil || !ok {
		return false, err
	}
	switch {
	case s.State == stHeld && s.Kind == kindWindow:
		return true, fmt.Errorf("claude-review: %s is already open in a window", pr.URL())
	case !forkOpenStates[stateKey{s.State, ""}] && !forkOpenStates[stateKey{s.State, s.Kind}]:
		return true, fmt.Errorf("claude-review: %s is %s; Open Claude waits until it settles", pr.URL(), stateName(s))
	}
	if live, err := l.HasLiveToken(pr); err != nil || live {
		if err == nil {
			err = fmt.Errorf("claude-review: %s has a live Approve link; approve or let it lapse before opening Claude", pr.URL())
		}
		return true, err
	}
	return true, nil
}

func stateName(s PRState) string {
	if s.Kind == "" {
		return string(s.State)
	}
	return string(s.State) + "{" + s.Kind + "}"
}

// windowOpen is a window on pr: any id its workers may run under, older
// policy versions' included, held by a non-worker session, or the last
// fork's recorded id held by any live session.
func windowOpen(s Sessions, l *Ledger, pr PR) bool {
	for _, id := range pr.workerIDs() {
		if s.WindowOpen(id) {
			return true
		}
	}
	fid, ok, err := l.ForkSession(pr)
	if err != nil {
		return true // an unreadable record cannot rule a window out
	}
	return ok && s.Window(fid) > 0
}
