package main

import (
	"database/sql"
	"os"
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

func TestMigrate007SnoozeKinds(t *testing.T) {
	db := openTestDB(t)
	if err := migrate(db); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`insert into project (namespace_id, path, name, first_seen_at) values (1, 'm/a', 'a', 'now')`); err != nil {
		t.Fatal(err)
	}
	for _, k := range []string{"created", "archived", "reopened", "snoozed", "woken"} {
		if _, err := db.Exec(`insert into project_event (project_id, kind, detail, occurred_at) values (1, ?, '', 'now')`, k); err != nil {
			t.Fatalf("%s rejected: %v", k, err)
		}
	}
	if _, err := db.Exec(`insert into project_event (project_id, kind, occurred_at) values (1, 'bogus', 'now')`); err == nil {
		t.Fatal("bogus kind accepted")
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

// TestMigratePopulated applies the migrations that predate the link column
// additions, fills a link, then runs the rest: a fresh empty DB never
// exercises the row-touching statements (migration 10 once set a NOT NULL
// column to null and only failed on the live DB).
func TestMigratePopulated(t *testing.T) {
	db := openTestDB(t)
	apply := func(from, to int) {
		for i := from; i < to; i++ {
			tx, err := db.Begin()
			if err != nil {
				t.Fatal(err)
			}
			if err := migrations[i](tx); err != nil {
				t.Fatalf("migration %d on populated db: %v", i+1, err)
			}
			if err := tx.Commit(); err != nil {
				t.Fatal(err)
			}
		}
	}
	apply(0, 9)
	if _, err := db.Exec(`insert into project (namespace_id, path, name, first_seen_at) values (1, 'm/a', 'a', '2026-09-01T00:00:00Z')`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`insert into link (project_id, url, kind, etag) values (1, 'https://github.com/o/r/pull/1', 'github_pr', '"e1"')`); err != nil {
		t.Fatal(err)
	}
	apply(9, len(migrations))
	var etag string
	var updated sql.NullString
	if err := db.QueryRow(`select etag, github_updated_at from link`).Scan(&etag, &updated); err != nil || etag != "" || updated.Valid {
		t.Fatalf("etag=%q updated=%v %v", etag, updated, err)
	}
}

// TestMigrateLiveCopy runs the migrations against a copy of this machine's
// live DB, so a migration that only fails on real rows fails here, before
// home-manager installs the binary and the timer hits it. Skips where there
// is no live DB (CI, the nix sandbox).
func TestMigrateLiveCopy(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skip(err)
	}
	live := filepath.Join(dataDir(home), "p-launcher", "p.db")
	if _, err := os.Stat(live); err != nil {
		t.Skip("no live DB")
	}
	dir := t.TempDir()
	for _, suffix := range []string{"", "-wal"} {
		b, err := os.ReadFile(live + suffix)
		if err != nil {
			if suffix == "" {
				t.Fatal(err)
			}
			continue
		}
		if err := os.WriteFile(filepath.Join(dir, "p.db"+suffix), b, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("P_LAUNCHER_NOTIFY_LOG", filepath.Join(dir, "notify.log"))
	s, err := OpenStore(filepath.Join(dir, "p.db"))
	if err != nil {
		t.Fatalf("migrating a copy of the live DB: %v", err)
	}
	s.Close()
}

func TestNotifyLog(t *testing.T) {
	log := filepath.Join(t.TempDir(), "n.log")
	t.Setenv("P_LAUNCHER_NOTIFY_LOG", log)
	notify("multi\nline")
	notifyInfo("hi")
	b, err := os.ReadFile(log)
	if err != nil || string(b) != "critical\tp-launcher failed\tmulti line\nlow\tp-launcher\thi\n" {
		t.Fatalf("%q %v", b, err)
	}
}
