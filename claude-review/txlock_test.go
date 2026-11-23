package main

import (
	"database/sql"
	"path/filepath"
	"testing"
	"time"
)

// Two processes write the ledger: the tick and a click (or a verb). A
// transaction that reads and then writes must not fail because the other
// wrote in between: in WAL mode a deferred transaction that read an older
// snapshot gets SQLITE_BUSY_SNAPSHOT on its first write, which busy_timeout
// never retries. Every transaction takes the write lock at BEGIN instead.
func TestAReadThenWriteTransactionSurvivesAnotherWriter(t *testing.T) {
	path := filepath.Join(t.TempDir(), "ledger.db")
	a, err := OpenLedger(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = a.Close() }()
	b, err := OpenLedger(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = b.Close() }()

	tx, err := a.db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback() }()
	var n int
	if err := tx.QueryRow(`select count(*) from meta`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	other := make(chan error, 1)
	go func() { other <- b.SetMeta("other", "1") }()
	// Give the other writer the time it needs to land, if it can.
	select {
	case err := <-other:
		other <- err
	case <-time.After(300 * time.Millisecond):
	}
	if _, err := tx.Exec(`insert into meta (key, value) values ('mine', '1')`); err != nil {
		t.Fatalf("the transaction's write after another writer: %v", err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	if err := <-other; err != nil {
		t.Fatalf("the other writer: %v", err)
	}
}

// The ledger relies on WAL for a reader beside a writer; a database that
// silently stays in another journal mode is refused at open.
func TestLedgerRefusesADatabaseNotInWAL(t *testing.T) {
	db, err := sql.Open("sqlite", "file:"+filepath.Join(t.TempDir(), "x.db")+"?_pragma=journal_mode(DELETE)")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	if err := assertWAL(db); err == nil {
		t.Error("a rollback-journal database passed the WAL check")
	}
	if _, err := db.Exec(`pragma journal_mode=WAL`); err != nil {
		t.Fatal(err)
	}
	if err := assertWAL(db); err != nil {
		t.Errorf("a WAL database failed the check: %v", err)
	}
}
