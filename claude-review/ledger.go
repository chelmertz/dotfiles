package main

import (
	"crypto/rand"
	"crypto/subtle"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	_ "modernc.org/sqlite" // the ledger's driver, pure Go
)

// Ledger is the runner's own state, apart from Claude Code's session store
// and from GitHub: which PRs the user accepted, which are ignored, which
// rounds ran, the approve tokens, and the annotation each PR currently has.
type Ledger struct{ db *sql.DB }

const ledgerSchema = `
create table if not exists acceptance (pr text primary key, head text not null, base text not null, at integer not null);
create table if not exists ignored (pr text primary key);
create table if not exists rounds (
	id integer primary key, pr text not null, head text not null, trigger text not null,
	started_at integer not null, read_at integer, retries integer not null default 0);
create table if not exists tokens (token text primary key, pr text not null, sha text not null, used integer not null default 0);
create table if not exists annotations (pr text primary key, state text not null, note text not null, links text not null);
`

// LedgerPath is where the ledger lives: XDG state, beside nothing else.
func LedgerPath(home string) string {
	if d := os.Getenv("XDG_STATE_HOME"); d != "" {
		return filepath.Join(d, "claude-review", "ledger.db")
	}
	return filepath.Join(home, ".local", "state", "claude-review", "ledger.db")
}

func OpenLedger(path string) (*Ledger, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, fmt.Errorf("ledger dir: %w", err)
	}
	db, err := sql.Open("sqlite", "file:"+path+"?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)")
	if err != nil {
		return nil, fmt.Errorf("open ledger: %w", err)
	}
	if _, err := db.Exec(ledgerSchema); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("ledger schema: %w", err)
	}
	return &Ledger{db: db}, nil
}

func (l *Ledger) Close() error { return l.db.Close() }

// Acceptance is the user's first-look click: the head and base it saw.
type Acceptance struct {
	Head string
	Base string
}

// Accept records a click on pr, which also clears an ignore: clicking a PR
// again is taking it back.
func (l *Ledger) Accept(pr PR, head, base string, at time.Time) error {
	if _, err := l.db.Exec(`replace into acceptance (pr, head, base, at) values (?, ?, ?, ?)`, pr.URL(), head, base, at.Unix()); err != nil {
		return fmt.Errorf("accept: %w", err)
	}
	_, err := l.db.Exec(`delete from ignored where pr = ?`, pr.URL())
	return err
}

func (l *Ledger) Acceptance(pr PR) (Acceptance, bool, error) {
	var a Acceptance
	err := l.db.QueryRow(`select head, base from acceptance where pr = ?`, pr.URL()).Scan(&a.Head, &a.Base)
	if errors.Is(err, sql.ErrNoRows) {
		return Acceptance{}, false, nil
	}
	return a, err == nil, err
}

func (l *Ledger) Ignore(pr PR) error {
	_, err := l.db.Exec(`insert or ignore into ignored (pr) values (?)`, pr.URL())
	return err
}

func (l *Ledger) Ignored(pr PR) (bool, error) {
	var n int
	err := l.db.QueryRow(`select count(*) from ignored where pr = ?`, pr.URL()).Scan(&n)
	return n > 0, err
}

// Round is one unattended review round, recorded when it starts.
type Round struct {
	ID        int64
	PR        string
	Head      string
	Trigger   string
	StartedAt time.Time
	Retries   int
}

func (l *Ledger) StartRound(pr PR, head, trigger string, at time.Time) (int64, error) {
	res, err := l.db.Exec(`insert into rounds (pr, head, trigger, started_at) values (?, ?, ?, ?)`, pr.URL(), head, trigger, at.Unix())
	if err != nil {
		return 0, fmt.Errorf("start round: %w", err)
	}
	return res.LastInsertId()
}

// RanAt reports whether a round already started for pr at head for trigger.
func (l *Ledger) RanAt(pr PR, head, trigger string) (bool, error) {
	var n int
	err := l.db.QueryRow(`select count(*) from rounds where pr = ? and head = ? and trigger = ?`, pr.URL(), head, trigger).Scan(&n)
	return n > 0, err
}

// RoundsSince counts pr's rounds started at or after since, for the daily cap.
func (l *Ledger) RoundsSince(pr PR, since time.Time) (int, error) {
	var n int
	err := l.db.QueryRow(`select count(*) from rounds where pr = ? and started_at >= ?`, pr.URL(), since.Unix()).Scan(&n)
	return n, err
}

// UnreadRounds are rounds whose outcome has not been processed yet.
func (l *Ledger) UnreadRounds() ([]Round, error) {
	rows, err := l.db.Query(`select id, pr, head, trigger, started_at, retries from rounds where read_at is null order by id`)
	if err != nil {
		return nil, fmt.Errorf("unread rounds: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var out []Round
	for rows.Next() {
		var r Round
		var started int64
		if err := rows.Scan(&r.ID, &r.PR, &r.Head, &r.Trigger, &started, &r.Retries); err != nil {
			return nil, err
		}
		r.StartedAt = time.Unix(started, 0).UTC()
		out = append(out, r)
	}
	return out, rows.Err()
}

func (l *Ledger) MarkRead(id int64) error {
	_, err := l.db.Exec(`update rounds set read_at = ? where id = ?`, time.Now().Unix(), id)
	return err
}

func (l *Ledger) MarkRetried(id int64) error {
	_, err := l.db.Exec(`update rounds set retries = retries + 1 where id = ?`, id)
	return err
}

// MintToken makes the one-time token an Approve link carries for pr at sha.
func (l *Ledger) MintToken(pr PR, sha string) (string, error) {
	b := make([]byte, 24)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	tok := hex.EncodeToString(b)
	_, err := l.db.Exec(`insert into tokens (token, pr, sha) values (?, ?, ?)`, tok, pr.URL(), sha)
	return tok, err
}

// UseToken spends tok if it was minted for pr at sha and never used.
func (l *Ledger) UseToken(pr PR, sha, tok string) (bool, error) {
	var stored string
	err := l.db.QueryRow(`select token from tokens where pr = ? and sha = ? and used = 0 and token = ?`, pr.URL(), sha, tok).Scan(&stored)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil || subtle.ConstantTimeCompare([]byte(stored), []byte(tok)) != 1 {
		return false, err
	}
	res, err := l.db.Exec(`update tokens set used = 1 where token = ? and used = 0`, tok)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	return n == 1, err
}

// Annotation is what the runner tells elly about one PR.
type Annotation struct {
	State string
	Note  string
	Links []Link
}

type Link struct {
	Label string `json:"label"`
	URL   string `json:"url"`
}

const (
	stateHandled  = "handled"
	stateNeedsYou = "needs_you"
)

func (l *Ledger) SetAnnotation(pr PR, a Annotation) error {
	links, err := json.Marshal(a.Links)
	if err != nil {
		return err
	}
	_, err = l.db.Exec(`replace into annotations (pr, state, note, links) values (?, ?, ?, ?)`, pr.URL(), a.State, a.Note, string(links))
	return err
}

func (l *Ledger) ClearAnnotation(pr PR) error {
	_, err := l.db.Exec(`delete from annotations where pr = ?`, pr.URL())
	return err
}

// Annotations are the live annotations by PR URL, re-sent on every tick.
func (l *Ledger) Annotations() (map[string]Annotation, error) {
	rows, err := l.db.Query(`select pr, state, note, links from annotations`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	out := map[string]Annotation{}
	for rows.Next() {
		var pr, links string
		var a Annotation
		if err := rows.Scan(&pr, &a.State, &a.Note, &links); err != nil {
			return nil, err
		}
		if err := json.Unmarshal([]byte(links), &a.Links); err != nil {
			return nil, err
		}
		out[pr] = a
	}
	return out, rows.Err()
}

// LastHead is the head of pr's most recent round, or "" before any.
func (l *Ledger) LastHead(pr PR) (string, error) {
	var head string
	err := l.db.QueryRow(`select head from rounds where pr = ? order by id desc limit 1`, pr.URL()).Scan(&head)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	return head, err
}
