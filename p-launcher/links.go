package main

import (
	"bufio"
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// Links refresh: GitHub is the source of facts (state, timestamps, title,
// author), elly the source of the verdict "does this open PR need me". Every
// stored timestamp is GitHub's, so a missed refresh is repaired by the next
// one; only the verdict has a freshness gate.

type ghPR struct {
	State, Title, Body, Author    string
	Merged                        bool
	Add, Del                      int
	CreatedAt, ClosedAt, MergedAt time.Time
}

type ellyPR struct {
	ReviewStatus      string
	ThreadsActionable int
	Author            string
	LastCommenter     string
	IsDraft           bool
	LastUpdated       time.Time
}

// linkDeps are the remote readers, injected so tests need no network.
// gh returns the HTTP status (200 or 304) and the new ETag. checks folds the
// PR's CI into success|failure|pending and the latest completion time; it is
// optional (nil skips it) and best effort.
type linkDeps struct {
	gh     func(url, etag string) (ghPR, int, string, error)
	elly   func() (map[string]ellyPR, time.Time, error)
	checks func(url string) (string, time.Time, error)
	me     string
	now    time.Time
}

type refreshResult struct {
	Refreshed, Unchanged, Failed int
	EllyStale, EllyMissing       bool
}

func (r refreshResult) String() string {
	s := fmt.Sprintf("links: %d refreshed, %d unchanged, %d failed", r.Refreshed, r.Unchanged, r.Failed)
	if r.EllyMissing {
		s += "; elly unavailable"
	} else if r.EllyStale {
		s += "; elly data stale"
	}
	return s
}

// ellyStaleAfter is elly's 5 min interval times its backoff cap, plus slack.
const ellyStaleAfter = 30 * time.Minute

// refreshLinks updates every non-terminal link from GitHub (conditional
// requests) and applies elly's verdicts. Remote failures never return an
// error: values stay, the failure is counted and recorded in kv.
func refreshLinks(s *Store, d linkDeps) (refreshResult, error) {
	var res refreshResult
	type open struct {
		id        int64
		url, etag string
	}
	rows, err := s.db.Query(`select id, url, etag from link where merged = 0 and closed_at is null order by id`)
	if err != nil {
		return res, err
	}
	var opens []open
	for rows.Next() {
		var o open
		if err := rows.Scan(&o.id, &o.url, &o.etag); err != nil {
			rows.Close()
			return res, err
		}
		opens = append(opens, o)
	}
	rows.Close()
	nowS := d.now.UTC().Format(time.RFC3339)
	lastErr := ""
	stillOpen := map[string]int64{}
	mine := map[string]bool{} // open links authored by the user: CI checks matter
	for _, o := range opens {
		pr, status, etag, err := d.gh(o.url, o.etag)
		switch {
		case err != nil:
			res.Failed++
			lastErr = err.Error()
			stillOpen[o.url] = o.id
		case status == 304:
			res.Unchanged++
			if _, err := s.db.Exec(`update link set refreshed_at = ? where id = ?`, nowS, o.id); err != nil {
				return res, err
			}
			stillOpen[o.url] = o.id
			var author string
			if err := s.db.QueryRow(`select author from link where id = ?`, o.id).Scan(&author); err == nil && author == d.me {
				mine[o.url] = true
			}
		default:
			res.Refreshed++
			merged := 0
			if pr.Merged {
				merged = 1
			}
			if _, err := s.db.Exec(`update link set author = ?, title = ?, body = ?, additions = ?, deletions = ?,
				opened_at = coalesce(nullif(?, ''), opened_at), closed_at = nullif(?, ''), merged = ?, github_state = ?, etag = ?, refreshed_at = ?
				where id = ?`, pr.Author, pr.Title, pr.Body, pr.Add, pr.Del, rfcOrEmpty(pr.CreatedAt), rfcOrEmpty(pr.ClosedAt), merged, pr.State, etag, nowS, o.id); err != nil {
				return res, err
			}
			if !pr.Merged && pr.ClosedAt.IsZero() {
				stillOpen[o.url] = o.id
				if pr.Author == d.me {
					mine[o.url] = true
				}
			}
		}
	}
	if d.checks != nil {
		for url := range mine {
			state, at, err := d.checks(url)
			if err != nil {
				lastErr = "checks: " + err.Error()
				continue
			}
			if _, err := s.db.Exec(`update link set check_state = ?, check_at = nullif(?, '') where id = ?`, state, rfcOrEmpty(at), stillOpen[url]); err != nil {
				return res, err
			}
		}
	}
	if err := s.kvSet("links.last_refresh", nowS); err != nil {
		return res, err
	}
	if err := s.kvSet("links.last_error", lastErr); err != nil {
		return res, err
	}
	prs, lastFetched, err := d.elly()
	if err != nil {
		res.EllyMissing = true
		return res, nil
	}
	if !lastFetched.IsZero() {
		if err := s.kvSet("elly.last_fetched", lastFetched.UTC().Format(time.RFC3339)); err != nil {
			return res, err
		}
	}
	res.EllyStale = lastFetched.IsZero() || d.now.Sub(lastFetched) > ellyStaleAfter
	for url, id := range stillOpen {
		need, detail := 0, ""
		pr, listed := prs[url]
		if listed {
			if n, dt := ellyVerdict(pr, d.me); n {
				need, detail = 1, dt
			}
		}
		draft := 0
		if pr.IsDraft {
			draft = 1
		}
		if _, err := s.db.Exec(`update link set action_needed = ?, detail = ?, review_status = ?, threads_actionable = ?, last_commenter = ?, is_draft = ? where id = ?`,
			need, detail, pr.ReviewStatus, pr.ThreadsActionable, pr.LastCommenter, draft, id); err != nil {
			return res, err
		}
	}
	return res, nil
}

func rfcOrEmpty(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.UTC().Format(time.RFC3339)
}

// ellyVerdict says whether the user owes a reply on this open PR, mirroring
// what elly scores highest: threads where someone else has the last word, or
// changes requested on the user's own PR.
func ellyVerdict(pr ellyPR, me string) (bool, string) {
	switch {
	case pr.ThreadsActionable == 1:
		return true, "1 unresolved thread"
	case pr.ThreadsActionable > 1:
		return true, fmt.Sprintf("%d unresolved threads", pr.ThreadsActionable)
	case pr.ReviewStatus == "CHANGES_REQUESTED" && me != "" && pr.Author == me:
		return true, "changes requested"
	}
	return false, ""
}

// kvGet/kvSet are the small settings store (migration 5): refresh times and
// the last remote error.
func (s *Store) kvGet(key string) (string, error) {
	var v string
	err := s.db.QueryRow(`select value from kv where key = ?`, key).Scan(&v)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	return v, err
}

func (s *Store) kvSet(key, value string) error {
	_, err := s.db.Exec(`insert into kv (key, value) values (?, ?) on conflict(key) do update set value = excluded.value`, key, value)
	return err
}

// ghFetch reads one pull request through the gh CLI with a conditional
// request. A 304 costs no rate limit. gh exits nonzero for non-2xx, so the
// status line is parsed before the exit code is trusted.
func ghFetch(url, etag string) (ghPR, int, string, error) {
	o, r, n, ok := splitPRURL(url)
	if !ok {
		return ghPR{}, 0, "", fmt.Errorf("not a GitHub pull request URL: %s", url)
	}
	args := []string{"api", "-i", fmt.Sprintf("repos/%s/%s/pulls/%s", o, r, n)}
	if etag != "" {
		args = append(args, "-H", "If-None-Match: "+etag)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "gh", args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	runErr := cmd.Run()
	status, newEtag, body := parseHTTPResponse(stdout.String())
	switch {
	case status == 304:
		return ghPR{}, 304, etag, nil
	case status == 200:
		var raw struct {
			State, Title, Body   string
			Merged               bool
			User                 struct{ Login string }
			Additions, Deletions int
			CreatedAt            string `json:"created_at"`
			ClosedAt             string `json:"closed_at"`
			MergedAt             string `json:"merged_at"`
		}
		if err := json.Unmarshal([]byte(body), &raw); err != nil {
			return ghPR{}, 0, "", fmt.Errorf("gh api %s: %w", url, err)
		}
		return ghPR{State: raw.State, Merged: raw.Merged, Title: raw.Title, Body: raw.Body, Author: raw.User.Login,
			Add: raw.Additions, Del: raw.Deletions, CreatedAt: parseTime(raw.CreatedAt), ClosedAt: parseTime(raw.ClosedAt), MergedAt: parseTime(raw.MergedAt)}, 200, newEtag, nil
	}
	if runErr != nil {
		return ghPR{}, status, "", fmt.Errorf("gh api %s: %s", url, clip(stderr.String()+" "+strconv.Itoa(status)))
	}
	return ghPR{}, status, "", fmt.Errorf("gh api %s: HTTP %d", url, status)
}

func splitPRURL(url string) (owner, repo, num string, ok bool) {
	s := strings.TrimPrefix(url, "https://github.com/")
	parts := strings.Split(s, "/")
	if len(parts) != 4 || parts[2] != "pull" {
		return "", "", "", false
	}
	return parts[0], parts[1], parts[3], true
}

// parseHTTPResponse splits `gh api -i` output into status, ETag and body.
func parseHTTPResponse(out string) (status int, etag, body string) {
	sc := bufio.NewScanner(strings.NewReader(out))
	sc.Buffer(make([]byte, 1024*1024), 16*1024*1024)
	first := true
	var b strings.Builder
	inBody := false
	for sc.Scan() {
		line := sc.Text()
		switch {
		case inBody:
			b.WriteString(line)
		case first:
			first = false
			f := strings.Fields(line)
			if len(f) >= 2 {
				status, _ = strconv.Atoi(f[1])
			}
		case line == "":
			inBody = true
		case strings.HasPrefix(strings.ToLower(line), "etag:"):
			etag = strings.TrimSpace(line[len("etag:"):])
		}
	}
	return status, etag, b.String()
}

// ellyRead opens elly's own SQLite read-only (WAL: readers never block) and
// returns its open PRs plus its last successful fetch time. os.ErrNotExist
// when elly has never run here.
func ellyRead() (map[string]ellyPR, time.Time, error) {
	home, _ := os.UserHomeDir()
	path := filepath.Join(dataDir(home), "elly", "elly.db")
	if _, err := os.Stat(path); err != nil {
		return nil, time.Time{}, err
	}
	db, err := sql.Open("sqlite", "file:"+path+"?mode=ro&_pragma=busy_timeout(2000)")
	if err != nil {
		return nil, time.Time{}, err
	}
	defer db.Close()
	rows, err := db.Query(`select url, coalesce(review_status,''), coalesce(threads_actionable,0), coalesce(author,''), coalesce(last_updated,''),
		coalesce(last_pr_commenter,''), coalesce(is_draft,0) from prs`)
	if err != nil {
		return nil, time.Time{}, err
	}
	defer rows.Close()
	out := map[string]ellyPR{}
	for rows.Next() {
		var u, lu string
		var draft int
		var pr ellyPR
		if err := rows.Scan(&u, &pr.ReviewStatus, &pr.ThreadsActionable, &pr.Author, &lu, &pr.LastCommenter, &draft); err != nil {
			return nil, time.Time{}, err
		}
		pr.LastUpdated = parseAnyTime(lu)
		pr.IsDraft = draft != 0
		out[u] = pr
	}
	var lf string
	// elly's meta is key/value; last_fetched is RFC3339 with a UTC offset.
	if err := db.QueryRow(`select value from meta where key = 'last_fetched'`).Scan(&lf); err != nil && !errors.Is(err, sql.ErrNoRows) {
		return out, time.Time{}, err
	}
	return out, parseAnyTime(lf), nil
}

// parseAnyTime accepts RFC3339 or unix seconds.
func parseAnyTime(s string) time.Time {
	if t := parseTime(s); !t.IsZero() {
		return t
	}
	if n, err := strconv.ParseInt(strings.TrimSpace(s), 10, 64); err == nil && n > 0 {
		return time.Unix(n, 0)
	}
	return time.Time{}
}

// ghChecks folds a PR's CI status via gh: failure if any run failed, pending
// if any is still running, success otherwise; at is the latest completion.
func ghChecks(url string) (string, time.Time, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	out, err := runCmd(ctx, "gh", "pr", "view", url, "--json", "statusCheckRollup", "-q", ".statusCheckRollup")
	if err != nil {
		return "", time.Time{}, err
	}
	state, at := foldChecks(out)
	return state, at, nil
}

// foldChecks reduces gh's statusCheckRollup array. Check runs carry
// conclusion/status/completedAt; commit statuses carry state.
func foldChecks(raw []byte) (string, time.Time) {
	var runs []struct {
		Conclusion, Status, State, CompletedAt string
	}
	if err := json.Unmarshal(raw, &runs); err != nil || len(runs) == 0 {
		return "", time.Time{}
	}
	state := "success"
	var at time.Time
	for _, r := range runs {
		c := strings.ToUpper(r.Conclusion)
		if c == "" {
			c = strings.ToUpper(r.State)
		}
		switch c {
		case "FAILURE", "ERROR", "TIMED_OUT", "CANCELLED", "ACTION_REQUIRED", "STARTUP_FAILURE":
			state = "failure"
		case "", "PENDING", "EXPECTED":
			if state != "failure" && (strings.ToUpper(r.Status) == "IN_PROGRESS" || strings.ToUpper(r.Status) == "QUEUED" || strings.ToUpper(r.Status) == "PENDING" || c != "") {
				state = "pending"
			}
		}
		if t := parseTime(r.CompletedAt); t.After(at) {
			at = t
		}
	}
	return state, at
}

// realLinkDeps wires the CLI adapters.
func realLinkDeps() linkDeps {
	return linkDeps{gh: ghFetch, elly: ellyRead, checks: ghChecks, me: ghLogin(), now: time.Now()}
}
