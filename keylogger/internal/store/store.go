// Package store persists sessions and aggregate counts in sqlite. The daemon
// flushes the aggregator's *cumulative* snapshot periodically; upserts overwrite
// each row's count, so a flush is idempotent and a crash loses only the seconds
// since the last flush.
package store

import (
	"database/sql"
	"fmt"

	"github.com/chelmertz/dotfiles/keylogger/internal/model"
	_ "modernc.org/sqlite"
)

type Store struct{ db *sql.DB }

const schema = `
CREATE TABLE IF NOT EXISTS sessions (
  id         INTEGER PRIMARY KEY AUTOINCREMENT,
  started_at INTEGER NOT NULL,
  ended_at   INTEGER NOT NULL DEFAULT 0,
  host       TEXT NOT NULL DEFAULT '',
  note       TEXT NOT NULL DEFAULT ''
);
CREATE TABLE IF NOT EXISTS unigrams (
  session_id INTEGER NOT NULL,
  device TEXT NOT NULL, app TEXT NOT NULL, workspace TEXT NOT NULL, filetype TEXT NOT NULL,
  keycode INTEGER NOT NULL, modmask INTEGER NOT NULL,
  count INTEGER NOT NULL,
  PRIMARY KEY (session_id, device, app, workspace, filetype, keycode, modmask)
) WITHOUT ROWID;
CREATE TABLE IF NOT EXISTS bigrams (
  session_id INTEGER NOT NULL,
  device TEXT NOT NULL, app TEXT NOT NULL, workspace TEXT NOT NULL, filetype TEXT NOT NULL,
  kc1 INTEGER NOT NULL, kc2 INTEGER NOT NULL,
  count INTEGER NOT NULL, interval_sum_ms INTEGER NOT NULL,
  PRIMARY KEY (session_id, device, app, workspace, filetype, kc1, kc2)
) WITHOUT ROWID;
CREATE TABLE IF NOT EXISTS skipgrams (
  session_id INTEGER NOT NULL,
  device TEXT NOT NULL, app TEXT NOT NULL, workspace TEXT NOT NULL, filetype TEXT NOT NULL,
  kc1 INTEGER NOT NULL, kc2 INTEGER NOT NULL,
  count INTEGER NOT NULL,
  PRIMARY KEY (session_id, device, app, workspace, filetype, kc1, kc2)
) WITHOUT ROWID;
CREATE TABLE IF NOT EXISTS corrections (
  session_id INTEGER NOT NULL,
  device TEXT NOT NULL, app TEXT NOT NULL, workspace TEXT NOT NULL, filetype TEXT NOT NULL,
  wrong_kc INTEGER NOT NULL, right_kc INTEGER NOT NULL,
  count INTEGER NOT NULL,
  PRIMARY KEY (session_id, device, app, workspace, filetype, wrong_kc, right_kc)
) WITHOUT ROWID;
`

// Open opens (creating if needed) the sqlite database and ensures the schema.
func Open(path string) (*Store, error) {
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, err
	}
	// long-running single writer: WAL + relaxed sync is the right tradeoff.
	for _, p := range []string{
		"PRAGMA journal_mode=WAL",
		"PRAGMA synchronous=NORMAL",
		"PRAGMA busy_timeout=5000",
	} {
		if _, err := db.Exec(p); err != nil {
			return nil, fmt.Errorf("pragma %q: %w", p, err)
		}
	}
	if _, err := db.Exec(schema); err != nil {
		return nil, fmt.Errorf("schema: %w", err)
	}
	return &Store{db: db}, nil
}

func (s *Store) Close() error { return s.db.Close() }

// BeginSession inserts a new running session and returns its id.
func (s *Store) BeginSession(startedAt int64, host, note string) (int64, error) {
	res, err := s.db.Exec(
		`INSERT INTO sessions (started_at, host, note) VALUES (?, ?, ?)`,
		startedAt, host, note)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

// EndSession stamps ended_at.
func (s *Store) EndSession(id, endedAt int64) error {
	_, err := s.db.Exec(`UPDATE sessions SET ended_at=? WHERE id=?`, endedAt, id)
	return err
}

// Flush upserts a cumulative snapshot for a session in one transaction.
func (s *Store) Flush(sessionID int64, c model.Counts) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback() //nolint:errcheck

	uni, err := tx.Prepare(`INSERT INTO unigrams
		(session_id, device, app, workspace, filetype, keycode, modmask, count)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT DO UPDATE SET count=excluded.count`)
	if err != nil {
		return err
	}
	defer uni.Close()
	for _, u := range c.Unigrams {
		if _, err := uni.Exec(sessionID, u.Device, u.App, u.Workspace, u.Filetype,
			u.Keycode, u.Modmask, u.Count); err != nil {
			return err
		}
	}

	big, err := tx.Prepare(`INSERT INTO bigrams
		(session_id, device, app, workspace, filetype, kc1, kc2, count, interval_sum_ms)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT DO UPDATE SET count=excluded.count, interval_sum_ms=excluded.interval_sum_ms`)
	if err != nil {
		return err
	}
	defer big.Close()
	for _, b := range c.Bigrams {
		if _, err := big.Exec(sessionID, b.Device, b.App, b.Workspace, b.Filetype,
			b.KC1, b.KC2, b.Count, b.IntervalSumMs); err != nil {
			return err
		}
	}

	skip, err := tx.Prepare(`INSERT INTO skipgrams
		(session_id, device, app, workspace, filetype, kc1, kc2, count)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT DO UPDATE SET count=excluded.count`)
	if err != nil {
		return err
	}
	defer skip.Close()
	for _, sk := range c.Skipgrams {
		if _, err := skip.Exec(sessionID, sk.Device, sk.App, sk.Workspace, sk.Filetype,
			sk.KC1, sk.KC2, sk.Count); err != nil {
			return err
		}
	}

	corr, err := tx.Prepare(`INSERT INTO corrections
		(session_id, device, app, workspace, filetype, wrong_kc, right_kc, count)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT DO UPDATE SET count=excluded.count`)
	if err != nil {
		return err
	}
	defer corr.Close()
	for _, cr := range c.Corrections {
		if _, err := corr.Exec(sessionID, cr.Device, cr.App, cr.Workspace, cr.Filetype,
			cr.WrongKC, cr.RightKC, cr.Count); err != nil {
			return err
		}
	}

	return tx.Commit()
}

// LatestSessionID returns the most recent session id, or 0 if none.
func (s *Store) LatestSessionID() (int64, error) {
	var id int64
	err := s.db.QueryRow(`SELECT id FROM sessions ORDER BY id DESC LIMIT 1`).Scan(&id)
	if err == sql.ErrNoRows {
		return 0, nil
	}
	return id, err
}

// LoadSession reads session metadata.
func (s *Store) LoadSession(id int64) (model.Session, error) {
	var m model.Session
	err := s.db.QueryRow(
		`SELECT id, started_at, ended_at, host, note FROM sessions WHERE id=?`, id).
		Scan(&m.ID, &m.StartedAt, &m.EndedAt, &m.Host, &m.Note)
	return m, err
}

// LoadCounts reads back all counts for a session (for the report).
func (s *Store) LoadCounts(id int64) (model.Counts, error) {
	var c model.Counts

	rows, err := s.db.Query(`SELECT device, app, workspace, filetype, keycode, modmask, count
		FROM unigrams WHERE session_id=?`, id)
	if err != nil {
		return c, err
	}
	for rows.Next() {
		var u model.Unigram
		if err := rows.Scan(&u.Device, &u.App, &u.Workspace, &u.Filetype, &u.Keycode, &u.Modmask, &u.Count); err != nil {
			rows.Close()
			return c, err
		}
		c.Unigrams = append(c.Unigrams, u)
	}
	rows.Close()

	rows, err = s.db.Query(`SELECT device, app, workspace, filetype, kc1, kc2, count, interval_sum_ms
		FROM bigrams WHERE session_id=?`, id)
	if err != nil {
		return c, err
	}
	for rows.Next() {
		var b model.Bigram
		if err := rows.Scan(&b.Device, &b.App, &b.Workspace, &b.Filetype, &b.KC1, &b.KC2, &b.Count, &b.IntervalSumMs); err != nil {
			rows.Close()
			return c, err
		}
		c.Bigrams = append(c.Bigrams, b)
	}
	rows.Close()

	rows, err = s.db.Query(`SELECT device, app, workspace, filetype, kc1, kc2, count
		FROM skipgrams WHERE session_id=?`, id)
	if err != nil {
		return c, err
	}
	for rows.Next() {
		var sk model.Skipgram
		if err := rows.Scan(&sk.Device, &sk.App, &sk.Workspace, &sk.Filetype, &sk.KC1, &sk.KC2, &sk.Count); err != nil {
			rows.Close()
			return c, err
		}
		c.Skipgrams = append(c.Skipgrams, sk)
	}
	rows.Close()

	rows, err = s.db.Query(`SELECT device, app, workspace, filetype, wrong_kc, right_kc, count
		FROM corrections WHERE session_id=?`, id)
	if err != nil {
		return c, err
	}
	for rows.Next() {
		var cr model.Correction
		if err := rows.Scan(&cr.Device, &cr.App, &cr.Workspace, &cr.Filetype, &cr.WrongKC, &cr.RightKC, &cr.Count); err != nil {
			rows.Close()
			return c, err
		}
		c.Corrections = append(c.Corrections, cr)
	}
	rows.Close()

	return c, nil
}
