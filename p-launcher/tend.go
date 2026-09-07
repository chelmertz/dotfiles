package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

// tend: the PR feedback loop. Decisions are pure (tendDecide) and tested
// gate by gate; tendApply does the side effects through injected deps.
// Design: 2026-09-07-p-launcher-tend-design.md.

type tendLink struct {
	ID                                  int64
	URL, Project, Author, LastCommenter string
	CheckState, Detail, LastBody        string
	ActionNeeded, IsDraft, Open         bool
	LastUpdated, CheckAt, TendedAt      time.Time
	Rounds                              int
}

type tendCfg struct {
	Enabled   bool
	DailyCap  int
	UsedToday int
	Me        string
	Bots      []string
}

var defaultBots = []string{"dependabot[bot]", "renovate[bot]", "github-actions[bot]"}

// decision is what tend would do with one link. Kind: act | notify | skip.
// Reason is the feedback ("changes requested", "check failed"); Why explains
// a notify or skip.
type decision struct {
	Link   tendLink
	Kind   string
	Reason string
	Why    string
}

func (d decision) Project() string { return d.Link.Project }

func isBot(name string, bots []string) bool {
	if strings.HasSuffix(name, "[bot]") {
		return true
	}
	for _, b := range bots {
		if b == name {
			return true
		}
	}
	return false
}

// tendDecide applies the gates in order. The enabled switch comes after the
// eligibility gates so a dry run shows what would have acted.
func tendDecide(links []tendLink, cfg tendCfg, live map[string]bool, dirExists func(string) bool, now time.Time) []decision {
	sort.Slice(links, func(i, j int) bool {
		if links[i].Project != links[j].Project {
			return links[i].Project < links[j].Project
		}
		return links[i].URL < links[j].URL
	})
	acts := 0
	var out []decision
	for _, l := range links {
		d := decision{Link: l, Kind: "skip"}
		failedCheck := l.CheckState == "failure"
		if l.ActionNeeded && l.Detail != "" {
			d.Reason = l.Detail
		} else if l.ActionNeeded {
			d.Reason = "review feedback"
		}
		if failedCheck {
			if d.Reason != "" {
				d.Reason += "; "
			}
			d.Reason += "check failed"
		}
		switch {
		case !l.Open:
			d.Why = "not open"
		case l.Author != cfg.Me:
			d.Why = "not my PR"
		case l.IsDraft:
			d.Why = "draft"
		case !l.ActionNeeded && !failedCheck:
			d.Why = "no feedback"
		case !l.TendedAt.IsZero() && !latestActivity(l, failedCheck).After(l.TendedAt):
			d.Why = "no new activity"
		case !failedCheck && l.LastCommenter == cfg.Me:
			d.Why = "last commenter is me"
		case !failedCheck && isBot(l.LastCommenter, cfg.Bots):
			d.Why = "bot comment"
		case !failedCheck && strings.HasSuffix(strings.TrimSpace(l.LastBody), "--claude"):
			d.Why = "own reply"
		case !cfg.Enabled:
			d.Why = "disabled"
		case dirExists != nil && !dirExists(l.Project):
			d.Why = "dir missing"
		case live[l.Project]:
			d.Kind, d.Why = "notify", "live session"
		case cfg.UsedToday+acts >= cfg.DailyCap:
			d.Kind, d.Why = "notify", "daily cap"
		default:
			d.Kind = "act"
			acts++
		}
		out = append(out, d)
	}
	return out
}

func latestActivity(l tendLink, failedCheck bool) time.Time {
	t := l.LastUpdated
	if failedCheck && l.CheckAt.After(t) {
		t = l.CheckAt
	}
	return t
}

// tendPrompt is Claude's first message in an automatic session. Fixed text
// from the design doc; only the URL and reason vary.
func tendPrompt(url, reason string) string {
	return fmt.Sprintf(`Review feedback on %s: %s. For each open review thread or failed check: read it, verify the claim against the code before acting, fix what is right, and reply on the thread with what you did or why not. Run the project's tests before pushing. Push to the PR branch. Do not approve, merge, resolve threads, or reply to your own earlier replies. End your replies with a line containing only --claude. Stop when every thread has a reply.`, url, reason)
}

// tendDeps are the side effects, injected for tests.
type tendDeps struct {
	launch func(dir, tag, prompt string) error
	notify func(msg string)
	now    time.Time
}

// tendApply performs decisions: act launches a session and records the
// round; notify raises one notification per activity burst.
func tendApply(s *Store, root string, ds []decision, d tendDeps) error {
	nowS := d.now.UTC().Format(time.RFC3339)
	var firstErr error
	for _, x := range ds {
		switch x.Kind {
		case "act":
			dir := filepath.Join(root, x.Link.Project)
			if err := d.launch(dir, tagFor(x.Link.Project), tendPrompt(x.Link.URL, x.Reason)); err != nil {
				if firstErr == nil {
					firstErr = fmt.Errorf("tend %s: %w", x.Link.URL, err)
				}
				continue
			}
			if _, err := s.db.Exec(`update link set tended_at = ?, tend_rounds = tend_rounds + 1 where id = ?`, nowS, x.Link.ID); err != nil {
				return err
			}
			key := "tend.used." + d.now.Format("2006-01-02")
			v, _ := s.kvGet(key)
			n, _ := strconv.Atoi(v)
			if err := s.kvSet(key, strconv.Itoa(n+1)); err != nil {
				return err
			}
			if err := s.RecordSessionEvent(SessionEvent{SessionID: "tend:" + strconv.FormatInt(x.Link.ID, 10), Path: x.Link.Project, Cwd: dir, Kind: "tend", Detail: x.Link.URL}); err != nil {
				return err
			}
		case "notify":
			var notified string
			if err := s.db.QueryRow(`select coalesce(notified_at,'') from link where id = ?`, x.Link.ID).Scan(&notified); err != nil {
				return err
			}
			if t := parseTime(notified); !t.IsZero() && !x.Link.LastUpdated.After(t) {
				continue // same activity burst, already told
			}
			d.notify(fmt.Sprintf("PR %s %s; %s", shortPR(x.Link.URL), x.Reason, x.Why))
			if _, err := s.db.Exec(`update link set notified_at = ? where id = ?`, nowS, x.Link.ID); err != nil {
				return err
			}
		}
	}
	return firstErr
}

// loadTendLinks reads every link with its project; Open is derived from the
// GitHub state columns.
func loadTendLinks(s *Store) ([]tendLink, error) {
	rows, err := s.db.Query(`select l.id, l.url, p.path, l.author, l.last_commenter, l.check_state, l.detail,
		l.action_needed, l.is_draft, l.merged, coalesce(l.closed_at,''), coalesce(l.refreshed_at,''), coalesce(l.check_at,''), coalesce(l.tended_at,''), l.tend_rounds
		from link l join project p on p.id = l.project_id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []tendLink
	for rows.Next() {
		var l tendLink
		var action, draft, merged int
		var closed, refreshed, checkAt, tended string
		if err := rows.Scan(&l.ID, &l.URL, &l.Project, &l.Author, &l.LastCommenter, &l.CheckState, &l.Detail,
			&action, &draft, &merged, &closed, &refreshed, &checkAt, &tended, &l.Rounds); err != nil {
			return nil, err
		}
		l.ActionNeeded, l.IsDraft = action == 1, draft == 1
		l.Open = merged == 0 && closed == ""
		// elly's own last_updated is not stored per link; the refresh time is
		// the closest thing to "when this verdict was seen"
		l.LastUpdated = parseTime(refreshed)
		l.CheckAt, l.TendedAt = parseTime(checkAt), parseTime(tended)
		out = append(out, l)
	}
	return out, rows.Err()
}

func liveProjects(s *Store, now time.Time) (map[string]bool, error) {
	rows, err := s.db.Query(`select distinct p.path from session_state ss join project p on p.id = ss.project_id where ss.since > ?`,
		now.Add(-staleSession).UTC().Format(time.RFC3339))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]bool{}
	for rows.Next() {
		var p string
		if err := rows.Scan(&p); err != nil {
			return nil, err
		}
		out[p] = true
	}
	return out, rows.Err()
}

func loadTendCfg(s *Store, me string, now time.Time) (tendCfg, error) {
	cfg := tendCfg{DailyCap: 4, Me: me, Bots: defaultBots}
	if v, err := s.kvGet("tend.enabled"); err != nil {
		return cfg, err
	} else if v == "1" {
		cfg.Enabled = true
	}
	if v, _ := s.kvGet("tend.daily_cap"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n >= 0 {
			cfg.DailyCap = n
		}
	}
	if v, _ := s.kvGet("tend.used." + now.Format("2006-01-02")); v != "" {
		cfg.UsedToday, _ = strconv.Atoi(v)
	}
	return cfg, nil
}

// runTend is the subcommand: decide, print, and unless dry, apply. Signed
// own replies are checked only for links that would act (one gh call each).
func runTend(s *Store, root string, dry bool, max int, d tendDeps, lastComment func(url string) (string, error), stdout io.Writer) error {
	links, err := loadTendLinks(s)
	if err != nil {
		return err
	}
	me := ghLogin()
	cfg, err := loadTendCfg(s, me, d.now)
	if err != nil {
		return err
	}
	live, err := liveProjects(s, d.now)
	if err != nil {
		return err
	}
	ds := tendDecide(links, cfg, live, func(p string) bool { return isDir(filepath.Join(root, p)) }, d.now)
	acts := 0
	for i := range ds {
		if ds[i].Kind != "act" {
			continue
		}
		if lastComment != nil {
			if body, err := lastComment(ds[i].Link.URL); err == nil && strings.HasSuffix(strings.TrimSpace(body), "--claude") {
				ds[i].Kind, ds[i].Why = "skip", "own reply"
				continue
			}
		}
		acts++
		if acts > max {
			ds[i].Kind, ds[i].Why = "notify", "max per run"
		}
	}
	for _, x := range ds {
		why := x.Why
		if why != "" {
			why = "  (" + why + ")"
		}
		fmt.Fprintf(stdout, "%-6s %-24s %s  %s%s\n", x.Kind, x.Link.Project, x.Link.URL, x.Reason, why)
	}
	if dry {
		return nil
	}
	return tendApply(s, root, ds, d)
}

// ghLastComment returns the body of the newest comment on a PR, looking at
// both conversation comments and review comments.
func ghLastComment(url string) (string, error) {
	o, r, n, ok := splitPRURL(url)
	if !ok {
		return "", fmt.Errorf("not a pull request URL: %s", url)
	}
	type c struct {
		Body      string `json:"body"`
		CreatedAt string `json:"created_at"`
	}
	var newest c
	for _, path := range []string{"issues/%s/comments", "pulls/%s/comments"} {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		out, err := runCmd(ctx, "gh", "api", fmt.Sprintf("repos/%s/%s/"+path+"?per_page=1&sort=created&direction=desc", o, r, n))
		cancel()
		if err != nil {
			return "", err
		}
		var cs []c
		if err := json.Unmarshal(out, &cs); err != nil {
			return "", err
		}
		if len(cs) > 0 && cs[0].CreatedAt > newest.CreatedAt {
			newest = cs[0]
		}
	}
	return newest.Body, nil
}
