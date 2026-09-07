package main

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"
)

// loadReportData reads everything the report needs for [from, now). Events
// and permission asks also cover the prior window of the same length so the
// friction deltas have a baseline; lead times use the project's whole event
// history via the same rows. Best-effort inputs (i3 tree, GitHub login) turn
// into Notes instead of errors.
func loadReportData(s *Store, from, now time.Time) (rawData, error) {
	raw, err := loadReportDataAt(s, from, now)
	if err != nil {
		return raw, err
	}
	if tree, err := getTree(); err == nil {
		if open, err := OpenTags(tree); err == nil {
			raw.Open = open
		}
	}
	raw.Me = ghLogin()
	if raw.Me == "" {
		raw.Notes = append(raw.Notes, "gh login unknown: PR authorship not marked")
	}
	return raw, nil
}

// loadReportDataAt is the DB-only part (no i3, no gh), testable with a clock.
// elly verdicts older than ellyStaleAfter are gated off with a note.
func loadReportDataAt(s *Store, from, now time.Time) (rawData, error) {
	var raw rawData
	priorFrom := from.Add(-now.Sub(from))
	rows, err := s.db.Query(`
		select e.session_id, coalesce(p.path, ''), e.kind, e.detail, e.occurred_at
		from session_event e left join project p on p.id = e.project_id
		where e.occurred_at >= ?
		order by e.occurred_at, e.id`, rfc(priorFrom))
	if err != nil {
		return raw, fmt.Errorf("load events: %w", err)
	}
	for rows.Next() {
		var e rawEvent
		var at string
		if err := rows.Scan(&e.SessionID, &e.Project, &e.Kind, &e.Detail, &at); err != nil {
			rows.Close()
			return raw, err
		}
		e.At = parseTime(at)
		raw.Events = append(raw.Events, e)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return raw, err
	}

	rows, err = s.db.Query(`
		select p.path, l.url, l.author, l.title, l.body, coalesce(l.additions, 0), coalesce(l.deletions, 0),
		       coalesce(l.opened_at, ''), coalesce(l.closed_at, ''), l.merged, l.action_needed, coalesce(l.refreshed_at, ''), l.tend_rounds
		from link l join project p on p.id = l.project_id`)
	if err != nil {
		return raw, fmt.Errorf("load links: %w", err)
	}
	for rows.Next() {
		var l rawLink
		var opened, closed, refreshed string
		var merged, action int
		if err := rows.Scan(&l.Project, &l.URL, &l.Author, &l.Title, &l.Body, &l.Add, &l.Del, &opened, &closed, &merged, &action, &refreshed, &l.Rounds); err != nil {
			rows.Close()
			return raw, err
		}
		l.OpenedAt, l.ClosedAt, l.RefreshedAt = parseTime(opened), parseTime(closed), parseTime(refreshed)
		l.Merged, l.ActionNeeded = merged == 1, action == 1
		if l.Merged {
			l.MergedAt = l.ClosedAt
		}
		raw.Links = append(raw.Links, l)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return raw, err
	}

	rows, err = s.db.Query(`
		select p.path, pe.kind, pe.detail, pe.occurred_at
		from project_event pe join project p on p.id = pe.project_id
		order by pe.occurred_at, pe.id`)
	if err != nil {
		return raw, fmt.Errorf("load project events: %w", err)
	}
	for rows.Next() {
		var x rawProjectEvent
		var at string
		if err := rows.Scan(&x.Project, &x.Kind, &x.Detail, &at); err != nil {
			rows.Close()
			return raw, err
		}
		x.At = parseTime(at)
		raw.PEvents = append(raw.PEvents, x)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return raw, err
	}

	rows, err = s.db.Query(`
		select pr.session_id, coalesce(p.path, ''), pr.tool_name, pr.rule, pr.occurred_at
		from permission_request pr left join project p on p.id = pr.project_id
		where pr.occurred_at >= ?`, rfc(priorFrom))
	if err != nil {
		return raw, fmt.Errorf("load permission requests: %w", err)
	}
	for rows.Next() {
		var x rawPerm
		var at string
		if err := rows.Scan(&x.SessionID, &x.Project, &x.Tool, &x.Rule, &at); err != nil {
			rows.Close()
			return raw, err
		}
		x.At = parseTime(at)
		raw.Perms = append(raw.Perms, x)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return raw, err
	}

	rows, err = s.db.Query(`
		select ss.session_id, coalesce(p.path, ''), ss.state, ss.since
		from session_state ss left join project p on p.id = ss.project_id
		where ss.since >= ?`, rfc(now.Add(-staleLive)))
	if err != nil {
		return raw, fmt.Errorf("load session states: %w", err)
	}
	for rows.Next() {
		var x rawState
		var since string
		if err := rows.Scan(&x.SessionID, &x.Project, &x.State, &since); err != nil {
			rows.Close()
			return raw, err
		}
		x.Since = parseTime(since)
		raw.States = append(raw.States, x)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return raw, err
	}

	ps, err := s.ListProjects()
	if err != nil {
		return raw, err
	}
	for _, p := range ps {
		raw.Projects = append(raw.Projects, p.Path)
	}

	// freshness gate: a verdict from a stale or absent elly is not shown
	lf, err := s.kvGet("elly.last_fetched")
	if err != nil {
		return raw, err
	}
	if t := parseTime(lf); t.IsZero() || now.Sub(t) > ellyStaleAfter {
		hidden := 0
		for i := range raw.Links {
			if raw.Links[i].ActionNeeded {
				raw.Links[i].ActionNeeded = false
				hidden++
			}
		}
		if hidden > 0 {
			if t.IsZero() {
				raw.Notes = append(raw.Notes, fmt.Sprintf("%d review verdicts hidden: no elly data", hidden))
			} else {
				raw.Notes = append(raw.Notes, fmt.Sprintf("%d review verdicts hidden: elly data stale since %s", hidden, t.In(now.Location()).Format("15:04")))
			}
		}
	}
	return raw, nil
}

func rfc(t time.Time) string { return t.UTC().Format(time.RFC3339) }

// parseTime reads the RFC3339 strings the store writes; "" or junk is zero.
func parseTime(s string) time.Time {
	if s == "" {
		return time.Time{}
	}
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		return time.Time{}
	}
	return t
}

// ghLogin asks the gh CLI who the user is, for PRChip.Mine. Empty on any
// failure; the report is still correct, just without the authored marker.
func ghLogin() string {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	out, err := runCmd(ctx, "gh", "api", "user", "-q", ".login")
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// countRows is a tiny helper for the footer.
func countRows(db *sql.DB, table string) int {
	var n int
	if err := db.QueryRow(`select count(*) from ` + table).Scan(&n); err != nil {
		return 0
	}
	return n
}
