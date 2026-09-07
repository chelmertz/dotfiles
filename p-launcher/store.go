package main

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"time"

	_ "modernc.org/sqlite"
)

type Store struct{ db *sql.DB }

// Project is one row of the launcher list, already sorted for display.
type Project struct {
	Path       string // "m/dependabot"
	Name       string // "dependabot"
	Label      string // namespace label, "matchi"
	LastActive string // RFC3339 UTC or "" when never selected
	Ball       string // "you" (a Claude session waits on the user), "claude" (working), or ""
}

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

// ListProjects returns projects sorted for display: namespace order, then
// needs-you first, then most recent activity, then never-active
// alphabetically. Ball aggregates live sessions: any "you" wins over
// "claude" (max() works because 'you' > 'claude'); no live session is "".
func (s *Store) ListProjects() ([]Project, error) { return s.listProjectsAt(time.Now()) }

func (s *Store) listProjectsAt(at time.Time) ([]Project, error) {
	cutoff := at.Add(-staleSession).UTC().Format(time.RFC3339)
	rows, err := s.db.Query(`
		select p.path, p.name, n.label,
		       coalesce((select max(occurred_at) from activity a where a.project_id = p.id), '') as last_active,
		       coalesce((select max(state) from session_state ss where ss.project_id = p.id and ss.since > ?), '') as ball
		from project p join namespace n on n.id = p.namespace_id
		order by n.sort_order, ball = 'you' desc, last_active = '', last_active desc, p.name`, cutoff)
	if err != nil {
		return nil, fmt.Errorf("list projects: %w", err)
	}
	defer rows.Close()
	var out []Project
	for rows.Next() {
		var p Project
		if err := rows.Scan(&p.Path, &p.Name, &p.Label, &p.LastActive, &p.Ball); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
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
