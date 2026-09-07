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
	Path       string // "m/dependabot"
	Name       string // "dependabot"
	Label      string // namespace label, "matchi"
	LastActive string // RFC3339 UTC or "" when never selected
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

// cut splits "m/dependabot" into ("m", "dependabot", true).
func cut(path string) (ns, name string, ok bool) {
	return strings.Cut(path, "/")
}

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
		if _, err := tx.Exec(`insert or ignore into project (namespace_id, path, name, first_seen_at)
			values ((select id from namespace where dir = ?), ?, ?, ?)`, f.Namespace, f.Path, f.Name, ts); err != nil {
			return fmt.Errorf("upsert project %s: %w", f.Path, err)
		}
	}
	return tx.Commit()
}

// ListProjects returns projects sorted for display: namespace order, then
// most recent activity, then never-active alphabetically.
func (s *Store) ListProjects() ([]Project, error) {
	rows, err := s.db.Query(`
		select p.path, p.name, n.label,
		       coalesce((select max(occurred_at) from activity a where a.project_id = p.id), '') as last_active
		from project p join namespace n on n.id = p.namespace_id
		order by n.sort_order, last_active = '', last_active desc, p.name`)
	if err != nil {
		return nil, fmt.Errorf("list projects: %w", err)
	}
	defer rows.Close()
	var out []Project
	for rows.Next() {
		var p Project
		if err := rows.Scan(&p.Path, &p.Name, &p.Label, &p.LastActive); err != nil {
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
	n, _ := res.RowsAffected()
	if n == 0 {
		return fmt.Errorf("record %s: unknown project %q", kind, path)
	}
	return nil
}
