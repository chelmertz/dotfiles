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
