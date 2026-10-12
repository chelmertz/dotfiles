package main

import (
	"fmt"
	"io"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// wait: a session that would otherwise poll CI or a reviewer registers what
// it is waiting for and ends its turn; tend resolves the wait on its 10-minute
// timer and opens a fresh session with the next step, or notifies when a
// session is still open in the project. Polling from inside a session cost
// 324 loops and two orphaned watchers across padelboard's transcripts
// (2026-09-16..10-07), and burned the context the session needed for the fix.

var waitConds = []string{"checks", "review", "merge"}

// waitExpiry ends a wait nothing resolved, so a CI that never reports or a
// reviewer who never comes back surfaces as a notification instead of a row
// that waits forever.
const waitExpiry = 48 * time.Hour

type waitRow struct {
	ID                 int64
	Project, URL, Cond string
	Note               string
	Created            time.Time
	SawPending         bool
	// link facts, as of the refresh that ran just before tend
	Merged, Closed bool
	LastUpdated    time.Time
	LastCommenter  string
	// fresh CI state for checks waits; empty when unknown
	CheckState string
	CheckAt    time.Time
}

// waitOutcome is pure: "" while still waiting, else the outcome. sawPending
// reports whether this observation saw the checks running.
func waitOutcome(w waitRow, me string, bots []string, now time.Time) (outcome string, sawPending bool) {
	switch {
	case w.Merged:
		return "merged", false
	case w.Closed:
		return "closed", false
	}
	switch w.Cond {
	case "checks":
		if w.CheckState == "pending" {
			return "", true
		}
		// A terminal state counts only once it postdates the wait: the row
		// still says "failure" from the run the session just pushed a fix for.
		if (w.CheckState == "success" || w.CheckState == "failure") && (w.SawPending || w.CheckAt.After(w.Created)) {
			return w.CheckState, false
		}
	case "review":
		if w.LastUpdated.After(w.Created) && w.LastCommenter != "" && w.LastCommenter != me && !isBot(w.LastCommenter, bots) {
			return "review", false
		}
	}
	if now.Sub(w.Created) > waitExpiry {
		return "expired", false
	}
	return "", false
}

// waitActs says whether an outcome is worth a session. A checks or review
// wait whose PR merged or closed meanwhile has nothing left to fix, and
// opening a session on a closed PR is how a stray agent force-pushed to one
// on 2026-10-07.
func waitActs(cond, outcome string) bool {
	switch outcome {
	case "success", "failure", "review":
		return true
	case "merged":
		return cond == "merge"
	}
	return false
}

var waitWhat = map[string]string{
	"success": "checks passed",
	"failure": "checks failed",
	"review":  "new review activity",
	"merged":  "the PR merged",
	"closed":  "the PR was closed",
	"expired": "nothing happened for 48h",
}

// waitPrompt is the resumed session's first message. The permissions are the
// user's call of 2026-10-12: fix, push and reply unattended; never open PRs
// or ask anyone for review.
func waitPrompt(w waitRow, outcome string) string {
	guard := ""
	if w.Cond != "merge" {
		guard = fmt.Sprintf("First run `gh pr view %s --json state -q .state`; if it is not OPEN, note that in HANDOFF.md and stop.\n\n", w.URL)
	}
	return fmt.Sprintf(`A wait registered in this project has cleared: %s on %s. The session that registered it left this next step:

%s

%sRead HANDOFF.md, then do that next step. You may fix code, run tests, push to the PR branch, and reply on review threads; end every reply with a line containing only --claude. Do not open new PRs, request or re-request review, approve, merge, or resolve threads: if the next step needs one of those, or a decision, add it under "## Open decisions" in HANDOFF.md and stop. Do not poll CI or reviews: to wait again, run p-launcher wait <url> checks|review|merge "<next step>" and end your turn. Finish by running the handoff skill.`,
		waitWhat[outcome], w.URL, w.Note, guard)
}

// addWait registers a wait for the project the caller's cwd is in. An open
// wait on the same URL and condition is replaced, so re-registering after
// another push does not stack duplicates.
func addWait(s *Store, root, cwd, url, cond, note string, checks func(string) (string, time.Time, error), now time.Time) (string, error) {
	path := projectPathFor(root, cwd)
	if path == "" {
		return "", fmt.Errorf("wait: %s is not inside a ~/p project", cwd)
	}
	if ref, ok := parseGitHubURL(url); !ok || ref.Kind != "github_pr" {
		return "", fmt.Errorf("wait: not a pull request URL: %s", url)
	}
	ok := false
	for _, c := range waitConds {
		ok = ok || c == cond
	}
	if !ok {
		return "", fmt.Errorf("wait: condition must be one of %s, got %q", strings.Join(waitConds, "|"), cond)
	}
	if strings.TrimSpace(note) == "" {
		return "", fmt.Errorf("wait: say what the next session should do when it clears")
	}
	if err := s.AddLink(path, url); err != nil {
		return "", err
	}
	pending := 0
	if cond == "checks" && checks != nil {
		if state, _, err := checks(url); err == nil && state == "pending" {
			pending = 1
		}
	}
	nowS := now.UTC().Format(time.RFC3339)
	tx, err := s.db.Begin()
	if err != nil {
		return "", err
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`update wait set resolved_at = ?, outcome = 'replaced' where url = ? and cond = ? and resolved_at is null`, nowS, url, cond); err != nil {
		return "", err
	}
	if _, err := tx.Exec(`insert into wait (project_id, url, cond, note, created_at, saw_pending)
		select id, ?, ?, ?, ?, ? from project where path = ?`, url, cond, note, nowS, pending, path); err != nil {
		return "", err
	}
	if err := tx.Commit(); err != nil {
		return "", err
	}
	return path, nil
}

func loadOpenWaits(s *Store) ([]waitRow, error) {
	rows, err := s.db.Query(`select w.id, p.path, w.url, w.cond, w.note, w.created_at, w.saw_pending,
		coalesce(l.merged, 0), coalesce(l.closed_at, ''), coalesce(l.elly_updated_at, l.github_updated_at, ''), coalesce(l.last_commenter, '')
		from wait w join project p on p.id = w.project_id left join link l on l.url = w.url
		where w.resolved_at is null order by w.id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []waitRow
	for rows.Next() {
		var w waitRow
		var created, closed, updated string
		var saw, merged int
		if err := rows.Scan(&w.ID, &w.Project, &w.URL, &w.Cond, &w.Note, &created, &saw, &merged, &closed, &updated, &w.LastCommenter); err != nil {
			return nil, err
		}
		w.Created, w.SawPending, w.Merged, w.Closed = parseTime(created), saw == 1, merged == 1, closed != ""
		w.LastUpdated = parseTime(updated)
		out = append(out, w)
	}
	return out, rows.Err()
}

// listWaits prints the open waits, newest last.
func listWaits(s *Store, now time.Time, w io.Writer) error {
	ws, err := loadOpenWaits(s)
	if err != nil {
		return err
	}
	if len(ws) == 0 {
		fmt.Fprintln(w, "no open waits")
		return nil
	}
	for _, x := range ws {
		fmt.Fprintf(w, "%-24s %-7s %s  (%s)  %s\n", x.Project, x.Cond, x.URL, now.Sub(x.Created).Round(time.Minute), x.Note)
	}
	return nil
}

// runWaits resolves open waits ahead of tend's own pass. It returns the
// projects it opened a session in, so tend does not open a second one on top
// in the same run. Acting shares tend's switch, live-session rule and daily
// cap: a wait is the same unattended loop, entered on purpose.
func runWaits(s *Store, root string, dry bool, cfg tendCfg, live map[string]bool, d tendDeps, stdout io.Writer) (map[string]bool, error) {
	ws, err := loadOpenWaits(s)
	if err != nil {
		return nil, err
	}
	launched := map[string]bool{}
	nowS := d.now.UTC().Format(time.RFC3339)
	for _, w := range ws {
		if w.Cond == "checks" && !w.Merged && !w.Closed && d.checks != nil {
			if state, at, err := d.checks(w.URL); err == nil {
				w.CheckState, w.CheckAt = state, at
			}
		}
		outcome, sawPending := waitOutcome(w, cfg.Me, cfg.Bots, d.now)
		if sawPending && !w.SawPending && !dry {
			if _, err := s.db.Exec(`update wait set saw_pending = 1 where id = ?`, w.ID); err != nil {
				return nil, err
			}
		}
		if outcome == "" {
			fmt.Fprintf(stdout, "%-6s %-24s %s  wait %s\n", "wait", w.Project, w.URL, w.Cond)
			continue
		}
		kind, why := "notify", ""
		switch {
		case !waitActs(w.Cond, outcome):
		case !cfg.Enabled:
			why = "disabled"
		case live[w.Project] || launched[w.Project]:
			why = "live session"
		case !isDir(filepath.Join(root, w.Project)):
			why = "dir missing"
		case cfg.UsedToday >= cfg.DailyCap:
			why = "daily cap"
		default:
			kind = "act"
		}
		if why != "" {
			why = "  (" + why + ")"
		}
		fmt.Fprintf(stdout, "%-6s %-24s %s  wait %s: %s%s\n", kind, w.Project, w.URL, w.Cond, waitWhat[outcome], why)
		if dry {
			continue
		}
		if kind == "act" {
			if err := d.launch(filepath.Join(root, w.Project), tagFor(w.Project), waitPrompt(w, outcome)); err != nil {
				return launched, fmt.Errorf("wait %s: %w", w.URL, err)
			}
			launched[w.Project] = true
			cfg.UsedToday++
			if err := s.kvSet("tend.used."+d.now.Format("2006-01-02"), strconv.Itoa(cfg.UsedToday)); err != nil {
				return launched, err
			}
			// the resumed session owns this activity; tend's own pass must
			// not answer the same failure or comment a second time
			if _, err := s.db.Exec(`update link set tended_at = ? where url = ?`, nowS, w.URL); err != nil {
				return launched, err
			}
		} else {
			d.notify(fmt.Sprintf("PR %s: %s. Next: %s", shortPR(w.URL), waitWhat[outcome], w.Note))
		}
		if _, err := s.db.Exec(`update wait set resolved_at = ?, outcome = ? where id = ?`, nowS, outcome, w.ID); err != nil {
			return launched, err
		}
		if err := s.RecordSessionEvent(SessionEvent{SessionID: "wait:" + strconv.FormatInt(w.ID, 10), Path: w.Project, Kind: "wait", Detail: kind + " " + outcome + " " + w.URL}); err != nil {
			return launched, err
		}
	}
	return launched, nil
}
