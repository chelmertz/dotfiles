package main

import (
	"database/sql"
	"fmt"
	"time"
)

// migrations are applied in order; index+1 is the db_version. Append-only:
// never edit a shipped migration, add a new one.
var migrations = []func(*sql.Tx) error{
	migrate001,
	migrate002,
	migrate003,
	migrate004,
}

func migrate001(tx *sql.Tx) error {
	_, err := tx.Exec(`
create table namespace (
  id         integer primary key,
  dir        text not null unique,
  label      text not null,
  sort_order integer not null
);
insert into namespace (dir, label, sort_order) values ('m', 'matchi', 0), ('personal', 'personal', 1);

create table project (
  id            integer primary key,
  namespace_id  integer not null references namespace(id),
  path          text not null unique,
  name          text not null,
  first_seen_at text not null
);

create table activity (
  id          integer primary key,
  project_id  integer not null references project(id),
  occurred_at text not null,
  kind        text not null check (kind in ('launch', 'focus'))
);
create index activity_project_time on activity (project_id, occurred_at);`)
	return err
}

// migrate002 adds Claude Code session tracking: the live per-session ball
// state shown in the menu and the append-only event log for `report`.
// project_id is nullable: sessions started outside ~/p are recorded too.
func migrate002(tx *sql.Tx) error {
	_, err := tx.Exec(`
create table session_state (
  session_id text primary key,
  project_id integer references project(id),
  state      text not null check (state in ('claude', 'you')),
  since      text not null
);
create index session_state_project on session_state (project_id);

create table session_event (
  id          integer primary key,
  session_id  text not null,
  project_id  integer references project(id),
  cwd         text not null,
  kind        text not null,
  detail      text not null default '',
  occurred_at text not null
);
create index session_event_project_time on session_event (project_id, occurred_at);
create index session_event_session on session_event (session_id, occurred_at);`)
	return err
}

// migrate003 adds the tables the report reads and later commands fill:
// project lifecycle (create/archive/reopen with a reason), permission asks
// with their allowlist rule, and PR links refreshed from elly.
func migrate003(tx *sql.Tx) error {
	_, err := tx.Exec(`
create table project_event (
  id          integer primary key,
  project_id  integer not null references project(id),
  kind        text not null check (kind in ('created', 'archived', 'reopened')),
  detail      text not null default '',
  occurred_at text not null
);
create index project_event_project_time on project_event (project_id, occurred_at);

create table permission_request (
  id          integer primary key,
  session_id  text not null,
  project_id  integer references project(id),
  tool_name   text not null,
  rule        text not null,
  occurred_at text not null
);
create index permission_request_project_time on permission_request (project_id, occurred_at);

create table link (
  id            integer primary key,
  project_id    integer not null references project(id),
  url           text not null unique,
  kind          text not null default 'github_pr',
  author        text not null default '',
  title         text not null default '',
  body          text not null default '',
  additions     integer,
  deletions     integer,
  opened_at     text,
  closed_at     text,
  merged        integer not null default 0,
  action_needed integer not null default 0,
  refreshed_at  text
);
create index link_project on link (project_id);`)
	return err
}

// migrate004 records why the ball is with the user (permission_prompt,
// idle_prompt, stop), so an approved tool run can hand it back and the live
// view can say what it waits for.
func migrate004(tx *sql.Tx) error {
	_, err := tx.Exec(`alter table session_state add column reason text not null default ''`)
	return err
}

// migrate applies any migrations newer than the recorded max(db_version).
func migrate(db *sql.DB) error {
	if _, err := db.Exec(`create table if not exists meta (db_version integer not null, ts text not null)`); err != nil {
		return err
	}
	var current int
	if err := db.QueryRow(`select coalesce(max(db_version), 0) from meta`).Scan(&current); err != nil {
		return err
	}
	for i := current; i < len(migrations); i++ {
		tx, err := db.Begin()
		if err != nil {
			return err
		}
		if err := migrations[i](tx); err != nil {
			tx.Rollback()
			return fmt.Errorf("migration %d: %w", i+1, err)
		}
		if _, err := tx.Exec(`insert into meta (db_version, ts) values (?, ?)`, i+1, now()); err != nil {
			tx.Rollback()
			return err
		}
		if err := tx.Commit(); err != nil {
			return err
		}
	}
	return nil
}

func now() string { return time.Now().UTC().Format(time.RFC3339) }
