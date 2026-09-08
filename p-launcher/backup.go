package main

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"time"
)

// Backup writes a consistent copy of the database (VACUUM INTO, safe while
// hooks keep writing) as p-<date>.db in dir and keeps the newest keep files.
// The live DB stays outside ~/p (a syncing client would corrupt a WAL
// database); these dated copies are what gets synced.
func Backup(s *Store, dir string, keep int, now time.Time) (string, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	path := filepath.Join(dir, "p-"+now.Format("2006-01-02")+".db")
	_ = os.Remove(path) // VACUUM INTO refuses to overwrite; today's copy is replaced
	if _, err := s.db.Exec(`vacuum into ?`, path); err != nil {
		return "", fmt.Errorf("backup to %s: %w", path, err)
	}
	old, err := filepath.Glob(filepath.Join(dir, "p-*.db"))
	if err != nil {
		return path, err
	}
	sort.Strings(old) // dates sort lexically
	for len(old) > keep {
		if err := os.Remove(old[0]); err != nil {
			return path, err
		}
		old = old[1:]
	}
	return path, nil
}
