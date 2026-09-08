package main

// Daily brief: one read-only headless Claude run over the PR queue, turned
// into an executive summary and a ranked list of recommended actions the
// user can act on in five minutes. The launcher builds the input from
// elly's data and its own state, so the run is cheap and repeatable; Claude
// only reads and reasons, never comments, pushes or merges.
//
// Every brief is stored with its items, so the next one knows what it
// already recommended (an item the user acted on drops out; one they did
// not is marked as repeated) and the report can measure how long a
// recommendation waits before it moves.

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// briefPR is one pull request as the model sees it.
type briefPR struct {
	URL           string `json:"url"`
	Title         string `json:"title"`
	Repo          string `json:"repo"`
	Mine          bool   `json:"mine"`
	Draft         bool   `json:"draft,omitempty"`
	ReviewStatus  string `json:"review_status,omitempty"`
	Threads       int    `json:"unanswered_threads,omitempty"`
	LastCommenter string `json:"last_commenter,omitempty"`
	AskToReview   string `json:"ask_to_re_review,omitempty"`
	Reviewers     string `json:"reviewers_assigned,omitempty"`
	IdleDays      int    `json:"idle_days"`
	Size          int    `json:"lines_changed,omitempty"`
	Project       string `json:"project,omitempty"`
	Verdict       string `json:"launcher_verdict,omitempty"`
}

// briefPrevious is an item from the last brief, with what happened since.
type briefPrevious struct {
	URL       string `json:"url"`
	Action    string `json:"action"`
	Age       int    `json:"briefs_ago"`
	StillOpen bool   `json:"still_open"`
}

// briefInput is the whole prompt payload: no network access needed to build
// it, so a brief is deterministic given the database.
type briefInput struct {
	Date      string          `json:"date"`
	Me        string          `json:"me"`
	PRs       []briefPR       `json:"prs"`
	Previous  []briefPrevious `json:"previous_recommendations,omitempty"`
	Postponed []string        `json:"postponed_projects,omitempty"`
	EllyAge   string          `json:"pr_data_age,omitempty"`
}

// briefItem is one recommended action.
type briefItem struct {
	URL    string `json:"url"`
	Action string `json:"action"`
	Why    string `json:"why"`
}

// briefOut is what the model returns. Anything it adds is ignored; a body
// that will not parse is kept as text so nothing is lost.
type briefOut struct {
	Summary string      `json:"summary"`
	Items   []briefItem `json:"items"`
	Waiting []struct {
		URL      string `json:"url"`
		Reviewer string `json:"reviewer"`
		Days     int    `json:"days"`
	} `json:"waiting_on_others"`
	Skip string `json:"skip,omitempty"`
}

// briefPrompt is the fixed instruction. The input arrives as JSON on the
// same message; the model answers with one JSON object and nothing else.
func briefPrompt(in string) string {
	return `You write a daily brief about one engineer's pull request queue. Input follows as JSON.

Rules:
- Read only. Do not comment, edit, merge, close, approve or push anything. You may run "gh pr view", "gh pr checks" or "gh api" to verify or enrich a fact, at most for the PRs you actually recommend acting on.
- Recommend at most 8 actions, most valuable first. Group PRs that share one action into one item.
- An action is a verb the engineer can do today: answer threads, fix a failed check, ask <name> to re-review, add a reviewer, merge, rebase, close as superseded, split, mark ready.
- Never invent facts. If the input is not enough for an item, leave it out.
- previous_recommendations are what you said last time. Drop what is done. Say "still" in the why when an item repeats, and prefer a different, more forceful action if it has repeated more than twice.
- postponed_projects are decided: do not recommend anything for their PRs.
- Reference every PR by its full URL, never by number.

Answer with exactly one JSON object, no prose around it, no code fence:
{"summary": "at most 3 sentences: state of the queue and the one thing that matters today",
 "items": [{"url": "...", "action": "ping adam", "why": "half a sentence"}],
 "waiting_on_others": [{"url": "...", "reviewer": "name", "days": 3}],
 "skip": "one line naming what needs nothing today"}

Input:
` + in
}

// buildBriefInput assembles the payload from elly's PRs and the launcher's
// own links, projects and previous briefs.
func buildBriefInput(s *Store, prs map[string]ellyPR, ellyFetched time.Time, now time.Time, me string) (briefInput, error) {
	in := briefInput{Date: now.Format("2006-01-02"), Me: me}
	if !ellyFetched.IsZero() {
		in.EllyAge = fmtDur(int(now.Sub(ellyFetched) / time.Second))
	}
	owner := map[string]string{} // url -> project path
	verdict := map[string]string{}
	rows, err := s.db.Query(`select l.url, coalesce(p.path,''), coalesce(l.detail,'') from link l left join project p on p.id = l.project_id`)
	if err != nil {
		return briefInput{}, err
	}
	for rows.Next() {
		var u, path, detail string
		if err := rows.Scan(&u, &path, &detail); err != nil {
			rows.Close()
			return briefInput{}, err
		}
		owner[u], verdict[u] = path, detail
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return briefInput{}, err
	}
	ps, err := s.ListProjects()
	if err != nil {
		return briefInput{}, err
	}
	for _, p := range ps {
		if p.Snoozed || p.Archived {
			in.Postponed = append(in.Postponed, p.Path)
		}
	}
	for url, pr := range prs {
		if pr.Buried {
			continue // the user hid it in elly; that is a decision
		}
		b := briefPR{URL: url, Title: pr.Title, Repo: pr.Repo, Mine: me != "" && pr.Author == me,
			Draft: pr.IsDraft, ReviewStatus: pr.ReviewStatus, Threads: pr.ThreadsActionable,
			LastCommenter: pr.LastCommenter, AskToReview: pr.RereviewFrom, Reviewers: pr.ReviewRequested,
			Size: pr.Additions + pr.Deletions, Project: owner[url], Verdict: verdict[url]}
		if !pr.LastUpdated.IsZero() {
			b.IdleDays = int(now.Sub(pr.LastUpdated).Hours() / 24)
		}
		in.PRs = append(in.PRs, b)
	}
	sort.Slice(in.PRs, func(i, j int) bool {
		if in.PRs[i].Mine != in.PRs[j].Mine {
			return in.PRs[i].Mine // my PRs first: the queue is mostly mine
		}
		return in.PRs[i].URL < in.PRs[j].URL
	})
	in.Previous, err = previousItems(s, prs)
	if err != nil {
		return briefInput{}, err
	}
	return in, nil
}

// previousItems reads the last brief's items and says which PRs are still
// open, so a repeated recommendation is visible as repeated.
func previousItems(s *Store, prs map[string]ellyPR) ([]briefPrevious, error) {
	rows, err := s.db.Query(`select bi.url, bi.action, bi.repeats from brief_item bi
		where bi.brief_id = (select max(id) from brief) order by bi.rank`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []briefPrevious
	for rows.Next() {
		var p briefPrevious
		if err := rows.Scan(&p.URL, &p.Action, &p.Age); err != nil {
			return nil, err
		}
		_, p.StillOpen = prs[p.URL]
		out = append(out, p)
	}
	return out, rows.Err()
}

// parseBriefOut takes the model's answer. Claude sometimes wraps JSON in a
// fence or adds a sentence before it, so the outermost object is extracted
// rather than the whole body being required to be JSON.
func parseBriefOut(body string) (briefOut, error) {
	start, end := strings.Index(body, "{"), strings.LastIndex(body, "}")
	if start < 0 || end <= start {
		return briefOut{}, fmt.Errorf("no JSON object in the answer (%d bytes)", len(body))
	}
	var out briefOut
	if err := json.Unmarshal([]byte(body[start:end+1]), &out); err != nil {
		return briefOut{}, fmt.Errorf("parse brief: %w", err)
	}
	if out.Summary == "" && len(out.Items) == 0 {
		return briefOut{}, fmt.Errorf("brief has neither summary nor items")
	}
	return out, nil
}

// storeBrief writes the brief and its items, carrying the repeat count of
// any URL the previous brief also named.
func (s *Store) storeBrief(out briefOut, raw string, at time.Time) (int64, error) {
	prev := map[string]int{}
	rows, err := s.db.Query(`select url, repeats from brief_item where brief_id = (select max(id) from brief)`)
	if err != nil {
		return 0, err
	}
	for rows.Next() {
		var u string
		var n int
		if err := rows.Scan(&u, &n); err != nil {
			rows.Close()
			return 0, err
		}
		prev[u] = n
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return 0, err
	}
	res, err := s.db.Exec(`insert into brief (created_at, summary, skip, raw) values (?, ?, ?, ?)`,
		at.UTC().Format(time.RFC3339), out.Summary, out.Skip, raw)
	if err != nil {
		return 0, fmt.Errorf("store brief: %w", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return 0, err
	}
	for i, it := range out.Items {
		if _, err := s.db.Exec(`insert into brief_item (brief_id, rank, url, action, why, repeats) values (?, ?, ?, ?, ?, ?)`,
			id, i, it.URL, it.Action, it.Why, prev[it.URL]+1); err != nil {
			return 0, fmt.Errorf("store brief item %s: %w", it.URL, err)
		}
	}
	return id, nil
}

// LatestBrief returns the newest stored brief for rendering.
func (s *Store) LatestBrief() (briefOut, time.Time, error) {
	var summary, skip, at string
	err := s.db.QueryRow(`select summary, skip, created_at from brief order by id desc limit 1`).Scan(&summary, &skip, &at)
	if err != nil {
		return briefOut{}, time.Time{}, err
	}
	out := briefOut{Summary: summary, Skip: skip}
	rows, err := s.db.Query(`select url, action, why from brief_item where brief_id = (select max(id) from brief) order by rank`)
	if err != nil {
		return out, parseTime(at), err
	}
	defer rows.Close()
	for rows.Next() {
		var it briefItem
		if err := rows.Scan(&it.URL, &it.Action, &it.Why); err != nil {
			return out, parseTime(at), err
		}
		out.Items = append(out.Items, it)
	}
	return out, parseTime(at), rows.Err()
}

// briefDeps are the side effects, injected for tests.
type briefDeps struct {
	now    time.Time
	me     string // the user's GitHub login
	elly   func() (map[string]ellyPR, time.Time, error)
	ask    func(prompt string) (string, error) // one headless Claude run
	notify func(string)
}

// runBrief builds the input, asks Claude, stores the answer and renders it.
// dry makes it print the input and stop, so the prompt can be inspected
// without spending a run.
func runBrief(s *Store, d briefDeps, dry bool, outDir string, stdout io.Writer) error {
	prs, fetched, err := d.elly()
	if err != nil {
		return fmt.Errorf("brief needs elly's data: %w", err)
	}
	in, err := buildBriefInput(s, prs, fetched, d.now, d.me)
	if err != nil {
		return err
	}
	payload, err := json.MarshalIndent(in, "", "  ")
	if err != nil {
		return err
	}
	if dry {
		_, err := fmt.Fprintln(stdout, briefPrompt(string(payload)))
		return err
	}
	body, err := d.ask(briefPrompt(string(payload)))
	if err != nil {
		return err
	}
	out, err := parseBriefOut(body)
	if err != nil {
		// keep the answer: a brief that will not parse is still readable
		if _, serr := s.storeBrief(briefOut{Summary: "unparsed answer, see raw"}, body, d.now); serr != nil {
			return serr
		}
		return err
	}
	if _, err := s.storeBrief(out, body, d.now); err != nil {
		return err
	}
	path := filepath.Join(outDir, "brief.html")
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	if err := renderBrief(f, out, d.now, themes[briefTheme(s)]); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	if d.notify != nil {
		d.notify(briefHeadline(out))
	}
	_, err = fmt.Fprintln(stdout, path)
	return err
}

// briefHeadline is the notification text: how many actions, and the first.
func briefHeadline(out briefOut) string {
	switch len(out.Items) {
	case 0:
		return "brief: nothing to act on today"
	case 1:
		return "brief: 1 action · " + out.Items[0].Action
	}
	return fmt.Sprintf("brief: %d actions · first: %s", len(out.Items), out.Items[0].Action)
}

// briefTheme follows the system colour scheme like the menu does.
func briefTheme(s *Store) string {
	if v, _ := s.kvGet("theme"); v == "light" {
		return "light"
	}
	return "dark"
}

// askClaude runs one headless, read-only Claude session and returns its
// answer. The tool allowlist is the enforcement: no write tool is on it.
func askClaude(prompt string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
	defer cancel()
	out, err := runCmd(ctx, "claude", "-p", prompt,
		"--allowedTools", "Bash(gh pr view *),Bash(gh pr checks *),Bash(gh api *)",
		"--output-format", "text")
	if err != nil {
		return "", fmt.Errorf("claude -p: %w", err)
	}
	return string(out), nil
}

// briefDue keeps the brief to one run per day unless forced.
func briefDue(last string, now time.Time) bool {
	t := parseTime(last)
	return t.IsZero() || t.UTC().Format("2006-01-02") != now.UTC().Format("2006-01-02")
}

// briefRunCount is the number of briefs stored, for the report.
func (s *Store) briefRunCount() (int, error) {
	var n int
	err := s.db.QueryRow(`select count(*) from brief`).Scan(&n)
	return n, err
}
