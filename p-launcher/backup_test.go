package main

import (
	"database/sql"
	"path/filepath"
	"testing"
	"time"
)

func TestBackup(t *testing.T) {
	s := openTestStore(t)
	if err := s.UpsertProjects(found("m/a")); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(t.TempDir(), "backup")
	day := ts("2026-09-01T03:30:00Z")
	var last string
	for i := 0; i < 9; i++ {
		p, err := Backup(s, dir, 7, day.AddDate(0, 0, i))
		if err != nil {
			t.Fatal(err)
		}
		last = p
	}
	files, _ := filepath.Glob(filepath.Join(dir, "p-*.db"))
	if len(files) != 7 || filepath.Base(files[0]) != "p-2026-09-03.db" || filepath.Base(last) != "p-2026-09-09.db" {
		t.Fatalf("%v", files)
	}
	// the copy is a working database with the data
	db, err := sql.Open("sqlite", last)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var n int
	if err := db.QueryRow(`select count(*) from project where path = 'm/a'`).Scan(&n); err != nil || n != 1 {
		t.Fatalf("%d %v", n, err)
	}
	// same day twice: replaced, not duplicated or refused
	if _, err := Backup(s, dir, 7, day.AddDate(0, 0, 8)); err != nil {
		t.Fatal(err)
	}
	if files, _ = filepath.Glob(filepath.Join(dir, "p-*.db")); len(files) != 7 {
		t.Fatalf("%v", files)
	}
	_ = time.Now
}
