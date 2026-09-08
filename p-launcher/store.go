package main

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

type Store struct{ db *sql.DB }

// Project is one row of the launcher list, already sorted for display.
type Project struct {
	Path        string    // "m/dependabot"
	Name        string    // "dependabot"
	Label       string    // namespace label, "matchi"
	LastActive  string    // RFC3339 UTC or "" when never selected
	Ball        string    // "you" (a Claude session waits on the user), "claude" (working), or ""
	Archived    bool      // latest project_event is "archived"
	Snoozed     bool      // latest project_event is "snoozed" with a wake time still ahead
	Review      bool      // a fresh elly verdict says a linked PR waits on the user
	Description string    // one sentence of intent, "" when unset
	Reason      string    // why the ball is where it is: stop, idle_prompt, question, permission_prompt, …
	Since       time.Time // when the ball state started; zero without a live session
}

// SetDescription stores the project's one-sentence intent ("" clears it).
func (s *Store) SetDescription(path, text string) error {
	res, err := s.db.Exec(`update project set description = ? where path = ?`, strings.TrimSpace(text), path)
	if err != nil {
		return fmt.Errorf("describe %s: %w", path, err)
	}
	if n, err := res.RowsAffected(); err != nil || n == 0 {
		if err != nil {
			return err
		}
		return fmt.Errorf("describe: unknown project %q", path)
	}
	return nil
}

var errNoRows = sql.ErrNoRows

// OpenStore opens (creating dirs and file as needed) and migrates the DB.
func OpenStore(path string) (*Store, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, fmt.Errorf("create db dir: %w", err)
	}
	db, err := sql.Open("sqlite", path+"?_pragma=busy_timeout(5000)&_pragma=foreign_keys(1)")
	if err != nil {
		return nil, fmt.Errorf("open db %s: %w", path, err)
	}
	if err := migrate(db); err != nil {
		db.Close()
		return nil, fmt.Errorf("migrate db %s: %w", path, err)
	}
	return &Store{db: db}, nil
}

func (s *Store) Close() error { return s.db.Close() }

// UpsertProjects makes sure every discovered namespace and project exists.
// Unknown namespaces are appended after the seeded ones with label = dir.
func (s *Store) UpsertProjects(found []Found) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	ts := now()
	for _, f := range found {
		// An aggregate select without GROUP BY always yields one row, so a
		// WHERE guard cannot suppress the insert; the UNIQUE(dir) constraint
		// plus OR IGNORE is what makes this a no-op for known namespaces.
		if _, err := tx.Exec(`insert or ignore into namespace (dir, label, sort_order)
			select ?, ?, coalesce(max(sort_order), -1) + 1 from namespace`, f.Namespace, f.Namespace); err != nil {
			return fmt.Errorf("upsert namespace %s: %w", f.Namespace, err)
		}
		// OR IGNORE also swallows a NULL namespace_id (the subselect finding no
		// row), so the namespace insert above must stay first in this same
		// transaction or an unknown namespace silently drops its project.
		if _, err := tx.Exec(`insert or ignore into project (namespace_id, path, name, first_seen_at)
			values ((select id from namespace where dir = ?), ?, ?, ?)`, f.Namespace, f.Path, f.Name, ts); err != nil {
			return fmt.Errorf("upsert project %s: %w", f.Path, err)
		}
	}
	return tx.Commit()
}

// staleSession is how long a session_state row counts without a new hook
// event. Sessions that die without SessionEnd (closed terminal, kill -9)
// leave a row behind; after this it is ignored.
const staleSession = 24 * time.Hour

// ListProjects returns every project (archived included) sorted for display:
// namespace order, then needs-you first, then most recent activity, then
// never-active alphabetically. Ball aggregates live sessions: any "you" wins
// over "claude" (max() works because 'you' > 'claude'); no live session is "".
func (s *Store) ListProjects() ([]Project, error) { return s.listProjectsAt(true, time.Now()) }

// ListProjectsFiltered hides archived and postponed projects unless all is
// set; a postponed project still shows while Claude or a reviewer waits on
// the user. The menu and `list` use it, hooks and the report use ListProjects.
func (s *Store) ListProjectsFiltered(all bool) ([]Project, error) {
	return s.listProjectsAt(all, time.Now())
}

// latestKindExpr / latestDetailExpr read the project's newest lifecycle event.
const latestKindExpr = `coalesce((select pe.kind from project_event pe where pe.project_id = p.id order by pe.occurred_at desc, pe.id desc limit 1), '')`
const latestDetailExpr = `coalesce((select pe.detail from project_event pe where pe.project_id = p.id order by pe.occurred_at desc, pe.id desc limit 1), '')`

func (s *Store) listProjectsAt(all bool, at time.Time) ([]Project, error) {
	cutoff := at.Add(-staleSession).UTC().Format(time.RFC3339)
	// Review verdicts come from elly; they only count while elly's data is
	// fresh, otherwise a dead elly would pin projects at the top forever.
	lf, err := s.kvGet("elly.last_fetched")
	if err != nil {
		return nil, err
	}
	fresh := false
	if t := parseTime(lf); !t.IsZero() && at.Sub(t) <= ellyStaleAfter {
		fresh = true
	}
	rows, err := s.db.Query(`
		select p.path, p.name, n.label, p.description,
		       coalesce((select max(occurred_at) from activity a where a.project_id = p.id), '') as last_active,
		       coalesce((select max(state) from session_state ss where ss.project_id = p.id and ss.since > ?), '') as ball,
		       coalesce((select reason from session_state ss where ss.project_id = p.id and ss.since > ? order by (state = 'you') desc, since desc limit 1), '') as reason,
		       coalesce((select since from session_state ss where ss.project_id = p.id and ss.since > ? order by (state = 'you') desc, since desc limit 1), '') as since,
		       `+latestKindExpr+` as kind, `+latestDetailExpr+` as detail,
		       (? and exists(select 1 from link l where l.project_id = p.id and l.action_needed = 1)) as review
		from project p join namespace n on n.id = p.namespace_id
		order by n.sort_order, (ball = 'you' or review) desc, last_active = '', last_active desc, p.name`, cutoff, cutoff, cutoff, fresh)
	if err != nil {
		return nil, fmt.Errorf("list projects: %w", err)
	}
	defer rows.Close()
	var out []Project
	for rows.Next() {
		var p Project
		var kind, detail, since string
		if err := rows.Scan(&p.Path, &p.Name, &p.Label, &p.Description, &p.LastActive, &p.Ball, &p.Reason, &since, &kind, &detail, &p.Review); err != nil {
			return nil, err
		}
		p.Since = parseTime(since)
		p.Archived = kind == "archived"
		p.Snoozed = kind == "snoozed" && parseTime(detail).After(at)
		needsYou := p.Ball == "you" || p.Review
		if !all && (p.Archived || (p.Snoozed && !needsYou)) {
			continue
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// ProjectEvent appends a lifecycle event (created, archived with a reason,
// reopened) for a known project.
func (s *Store) ProjectEvent(path, kind, detail string) error {
	switch kind {
	case "created", "archived", "reopened", "snoozed", "woken":
	default:
		return fmt.Errorf("project event: unknown kind %q", kind)
	}
	res, err := s.db.Exec(`insert into project_event (project_id, kind, detail, occurred_at)
		select id, ?, ?, ? from project where path = ?`, kind, detail, now(), path)
	if err != nil {
		return fmt.Errorf("project event %s for %s: %w", kind, path, err)
	}
	if n, err := res.RowsAffected(); err != nil || n == 0 {
		if err != nil {
			return err
		}
		return fmt.Errorf("project event: unknown project %q", path)
	}
	return nil
}

// RecordActivity appends a launch/focus event for a known project.
func (s *Store) RecordActivity(path, kind string) error {
	return s.recordAt(path, kind, time.Now())
}

func (s *Store) recordAt(path, kind string, at time.Time) error {
	res, err := s.db.Exec(`insert into activity (project_id, occurred_at, kind)
		select id, ?, ? from project where path = ?`, at.UTC().Format(time.RFC3339), kind, path)
	if err != nil {
		return fmt.Errorf("record %s for %s: %w", kind, path, err)
	}
	if n, err := res.RowsAffected(); err != nil || n == 0 {
		if err != nil {
			return fmt.Errorf("record %s for %s: %w", kind, path, err)
		}
		return fmt.Errorf("record %s: unknown project %q", kind, path)
	}
	return nil
}

// SetSessionState records whose turn it is in one Claude Code session.
// state is "claude" or "you"; reason says why it is "you" (permission_prompt,
// idle_prompt, stop) and is "" for "claude". The project must be known.
func (s *Store) SetSessionState(sessionID, path, state, reason string) error {
	return s.setStateAt(sessionID, path, state, reason, time.Now())
}

func (s *Store) setStateAt(sessionID, path, state, reason string, at time.Time) error {
	res, err := s.db.Exec(`insert into session_state (session_id, project_id, state, reason, since)
		select ?, id, ?, ?, ? from project where path = ?
		on conflict(session_id) do update set project_id = excluded.project_id, state = excluded.state, reason = excluded.reason, since = excluded.since`,
		sessionID, state, reason, at.UTC().Format(time.RFC3339), path)
	if err != nil {
		return fmt.Errorf("set state %s for %s: %w", state, path, err)
	}
	if n, err := res.RowsAffected(); err != nil || n == 0 {
		if err != nil {
			return err
		}
		return fmt.Errorf("set state: unknown project %q", path)
	}
	return nil
}

// SessionBall reads a session's current state and reason; "" for unknown.
func (s *Store) SessionBall(sessionID string) (state, reason string, err error) {
	err = s.db.QueryRow(`select state, reason from session_state where session_id = ?`, sessionID).Scan(&state, &reason)
	if err == sql.ErrNoRows {
		return "", "", nil
	}
	return state, reason, err
}

// ClearSession forgets a session's ball state (SessionEnd). Unknown
// sessions are a no-op.
func (s *Store) ClearSession(sessionID string) error {
	_, err := s.db.Exec(`delete from session_state where session_id = ?`, sessionID)
	return err
}

// SessionEvent is one Claude Code hook invocation. Path is "" when cwd is
// outside the project root; the event is still recorded with a NULL project.
type SessionEvent struct {
	SessionID, Path, Cwd, Kind, Detail string
}

// RecordSessionEvent appends to the analytics log.
func (s *Store) RecordSessionEvent(e SessionEvent) error {
	return s.recordEventAt(e, time.Now())
}

func (s *Store) recordEventAt(e SessionEvent, at time.Time) error {
	ts := at.UTC().Format(time.RFC3339)
	if e.Path == "" {
		if _, err := s.db.Exec(`insert into session_event (session_id, project_id, cwd, kind, detail, occurred_at)
			values (?, null, ?, ?, ?, ?)`, e.SessionID, e.Cwd, e.Kind, e.Detail, ts); err != nil {
			return fmt.Errorf("record event %s: %w", e.Kind, err)
		}
		return nil
	}
	res, err := s.db.Exec(`insert into session_event (session_id, project_id, cwd, kind, detail, occurred_at)
		select ?, id, ?, ?, ?, ? from project where path = ?`, e.SessionID, e.Cwd, e.Kind, e.Detail, ts, e.Path)
	if err != nil {
		return fmt.Errorf("record event %s for %s: %w", e.Kind, e.Path, err)
	}
	if n, err := res.RowsAffected(); err != nil || n == 0 {
		if err != nil {
			return err
		}
		return fmt.Errorf("record event: unknown project %q", e.Path)
	}
	return nil
}

// RenameProject changes a project's path and name in place; its id, and so
// every row pointing at it, stays.
func (s *Store) RenameProject(oldPath, newPath, newName string) error {
	res, err := s.db.Exec(`update project set path = ?, name = ? where path = ?`, newPath, newName, oldPath)
	if err != nil {
		return fmt.Errorf("rename %s: %w", oldPath, err)
	}
	if n, err := res.RowsAffected(); err != nil || n == 0 {
		if err != nil {
			return err
		}
		return fmt.Errorf("rename: unknown project %q", oldPath)
	}
	return nil
}

// RecordPermission logs which tool the user approved and the allowlist rule
// that would have avoided the ask. path may be "" (no project).
func (s *Store) RecordPermission(sessionID, path, tool, rule string) error {
	ts := now()
	var err error
	if path == "" {
		_, err = s.db.Exec(`insert into permission_request (session_id, project_id, tool_name, rule, occurred_at) values (?, null, ?, ?, ?)`, sessionID, tool, rule, ts)
	} else {
		_, err = s.db.Exec(`insert into permission_request (session_id, project_id, tool_name, rule, occurred_at)
			select ?, id, ?, ?, ? from project where path = ?`, sessionID, tool, rule, ts, path)
	}
	if err != nil {
		return fmt.Errorf("record permission: %w", err)
	}
	return nil
}

// AddLink attaches a pull-request URL to a project; a known URL is a no-op.
// opened_at is provisional until `links refresh` replaces it with GitHub's
// created_at.
func (s *Store) AddLink(path, url string) error {
	res, err := s.db.Exec(`insert or ignore into link (project_id, url, opened_at)
		select id, ?, ? from project where path = ?`, url, now(), path)
	if err != nil {
		return fmt.Errorf("add link %s: %w", url, err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		var exists int
		if err := s.db.QueryRow(`select count(*) from link where url = ?`, url).Scan(&exists); err == nil && exists == 0 {
			return fmt.Errorf("add link: unknown project %q", path)
		}
	}
	return nil
}
