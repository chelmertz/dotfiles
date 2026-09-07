package main

import (
	"database/sql"
	"path/filepath"
	"testing"

	_ "modernc.org/sqlite"
)

func openTestDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "p.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

func TestMigrateFreshAndIdempotent(t *testing.T) {
	db := openTestDB(t)
	for i := 0; i < 2; i++ {
		if err := migrate(db); err != nil {
			t.Fatalf("run %d: %v", i, err)
		}
	}
	var v int
	if err := db.QueryRow(`select max(db_version) from meta`).Scan(&v); err != nil {
		t.Fatal(err)
	}
	if v != len(migrations) {
		t.Fatalf("db_version %d, want %d", v, len(migrations))
	}
	var n int
	if err := db.QueryRow(`select count(*) from meta`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != len(migrations) {
		t.Fatalf("meta rows %d, want %d (second run must be a no-op)", n, len(migrations))
	}
}

func TestMigrateSeedsNamespaces(t *testing.T) {
	db := openTestDB(t)
	if err := migrate(db); err != nil {
		t.Fatal(err)
	}
	rows, err := db.Query(`select dir, label, sort_order from namespace order by sort_order`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var got []string
	var sortOrders []int
	for rows.Next() {
		var dir, label string
		var so int
		if err := rows.Scan(&dir, &label, &so); err != nil {
			t.Fatal(err)
		}
		got = append(got, dir+"="+label)
		sortOrders = append(sortOrders, so)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0] != "m=matchi" || got[1] != "personal=personal" {
		t.Fatalf("got %v", got)
	}
	if len(sortOrders) != 2 || sortOrders[0] != 0 || sortOrders[1] != 1 {
		t.Fatalf("sort_order = %v, want [0 1]", sortOrders)
	}
}

func TestMigrate002Tables(t *testing.T) {
	db := openTestDB(t)
	if err := migrate(db); err != nil {
		t.Fatal(err)
	}
	for _, tbl := range []string{"session_state", "session_event"} {
		var n int
		if err := db.QueryRow(`select count(*) from sqlite_master where type='table' and name=?`, tbl).Scan(&n); err != nil {
			t.Fatal(err)
		}
		if n != 1 {
			t.Fatalf("table %s missing", tbl)
		}
	}
	// state is constrained to the two ball holders
	if _, err := db.Exec(`insert into session_state (session_id, project_id, state, since) values ('x', null, 'bogus', '2026-01-01T00:00:00Z')`); err == nil {
		t.Fatal("bogus state accepted")
	}
}

func TestMigrate003Tables(t *testing.T) {
	db := openTestDB(t)
	if err := migrate(db); err != nil {
		t.Fatal(err)
	}
	for _, tbl := range []string{"project_event", "permission_request", "link"} {
		var n int
		if err := db.QueryRow(`select count(*) from sqlite_master where type='table' and name=?`, tbl).Scan(&n); err != nil {
			t.Fatal(err)
		}
		if n != 1 {
			t.Fatalf("table %s missing", tbl)
		}
	}
	if _, err := db.Exec(`insert into project_event (project_id, kind, occurred_at) values (1, 'bogus', 'x')`); err == nil {
		t.Fatal("bogus project_event kind accepted")
	}
}

func TestMigrate004Reason(t *testing.T) {
	db := openTestDB(t)
	if err := migrate(db); err != nil {
		t.Fatal(err)
	}
	rows, err := db.Query(`pragma table_info(session_state)`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	found := false
	for rows.Next() {
		var cid int
		var name, typ string
		var notnull, pk int
		var dflt sql.NullString
		if err := rows.Scan(&cid, &name, &typ, &notnull, &dflt, &pk); err != nil {
			t.Fatal(err)
		}
		if name == "reason" {
			found = true
		}
	}
	if !found {
		t.Fatal("session_state.reason missing")
	}
}

func TestMigrate005LinksAndKV(t *testing.T) {
	db := openTestDB(t)
	if err := migrate(db); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`insert into kv (key, value) values ('a', 'b')`); err != nil {
		t.Fatal(err)
	}
	for _, col := range []string{"etag", "review_status", "threads_actionable", "detail", "github_state"} {
		if _, err := db.Exec(`select ` + col + ` from link limit 1`); err != nil {
			t.Fatalf("link.%s missing: %v", col, err)
		}
	}
}
