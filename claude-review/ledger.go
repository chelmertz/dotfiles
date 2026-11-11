package main

import (
	"crypto/subtle"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	_ "modernc.org/sqlite" // the ledger's driver, pure Go

	"github.com/chelmertz/dotfiles/claude-review/ids"
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
create table if not exists annotations (pr text primary key, state text not null, note text not null, links text not null, expires integer not null default 0);
create table if not exists posts (pr text not null, head text not null, slot text not null, n integer not null, primary key (pr, head, slot));
create table if not exists post_refusals (pr text not null, at integer not null);
create table if not exists meta (key text primary key, value text not null);
create table if not exists breaker (id integer primary key check (id = 1), tripped text, reset_at integer not null default 0);
`

// LedgerPath is where the ledger lives under stateDir, the XDG state home
// (Deps.StateDir), beside nothing else.
func LedgerPath(stateDir string) string {
	return filepath.Join(stateDir, "claude-review", "ledger.db")
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
	// "create table if not exists" adds no column to a ledger from before
	// it; on every later open the duplicate is the expected no-op.
	if _, err := db.Exec(`alter table annotations add column expires integer not null default 0`); err != nil && !strings.Contains(err.Error(), "duplicate column") {
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

// Round is one review round, recorded once its worker is running: tick's,
// or a click's (trigger clickTrigger).
type Round struct {
	ID        ids.RoundID
	PR        string
	Head      string
	Trigger   string
	StartedAt time.Time
	Retries   int
	// PostRefused: a post on the PR was refused over its limit since the
	// round started.
	PostRefused bool
}

func (l *Ledger) StartRound(pr PR, head, trigger string, at time.Time) (ids.RoundID, error) {
	var id ids.RoundID
	if err := l.db.QueryRow(`insert into rounds (pr, head, trigger, started_at) values (?, ?, ?, ?) returning id`,
		pr.URL(), head, trigger, at.Unix()).Scan(&id); err != nil {
		return id, fmt.Errorf("start round: %w", err)
	}
	return id, nil
}

// RanAt reports whether a round already started for pr at head for trigger.
func (l *Ledger) RanAt(pr PR, head, trigger string, since time.Time) (bool, error) {
	var n int
	err := l.db.QueryRow(`select count(*) from rounds where pr = ? and head = ? and (trigger = ? or started_at >= ?)`,
		pr.URL(), head, trigger, since.Unix()).Scan(&n)
	return n > 0, err
}

// RoundsSince counts pr's rounds tick started at or after since, for the
// daily cap. A click's round is the user's own and does not count.
func (l *Ledger) RoundsSince(pr PR, since time.Time) (int, error) {
	var n int
	err := l.db.QueryRow(`select count(*) from rounds where pr = ? and started_at >= ? and trigger not like 'click@%'`, pr.URL(), since.Unix()).Scan(&n)
	return n, err
}

// SpendPost takes one post from pr's budget in slot at head, and reports
// false, recording the refusal, when limit posts were already made there.
// One conditional update, so two posts at once cannot both take the last.
func (l *Ledger) SpendPost(pr PR, head, slot string, limit int, at time.Time) (bool, error) {
	if _, err := l.db.Exec(`insert or ignore into posts (pr, head, slot, n) values (?, ?, ?, 0)`, pr.URL(), head, slot); err != nil {
		return false, err
	}
	res, err := l.db.Exec(`update posts set n = n + 1 where pr = ? and head = ? and slot = ? and n < ?`, pr.URL(), head, slot, limit)
	if err != nil {
		return false, err
	}
	if n, err := res.RowsAffected(); err != nil || n == 1 {
		return n == 1, err
	}
	_, err = l.db.Exec(`insert into post_refusals (pr, at) values (?, ?)`, pr.URL(), at.Unix())
	return false, err
}

// DiffBase is the commit a later round diffs from: the head of the newest
// round tick has read for pr, else the head the user accepted.
func (l *Ledger) DiffBase(pr PR) (string, bool, error) {
	var head string
	err := l.db.QueryRow(`select head from rounds where pr = ? and read_at is not null order by id desc limit 1`, pr.URL()).Scan(&head)
	if errors.Is(err, sql.ErrNoRows) {
		a, ok, err := l.Acceptance(pr)
		return a.Head, ok, err
	}
	return head, err == nil, err
}

// Launches counts tick's round starts and retries since since, across all
// PRs, for the breaker. Starts before the last reset do not count; a
// click's round counts only through its retries, which tick starts.
func (l *Ledger) Launches(since time.Time) (int, error) {
	var n int
	err := l.db.QueryRow(`select coalesce(sum(retries + (trigger not like 'click@%')), 0) from rounds
		where started_at >= max(?, coalesce((select reset_at from breaker), 0))`, since.Unix()).Scan(&n)
	return n, err
}

// Trip latches the breaker: no round starts until Reset.
func (l *Ledger) Trip(reason string) error {
	_, err := l.db.Exec(`insert into breaker (id, tripped) values (1, ?) on conflict (id) do update set tripped = excluded.tripped`, reason)
	return err
}

// Tripped is the reason the breaker tripped, or "" while it is closed.
func (l *Ledger) Tripped() (string, error) {
	var reason sql.NullString
	err := l.db.QueryRow(`select tripped from breaker`).Scan(&reason)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	return reason.String, err
}

// Reset closes the breaker and starts its count afresh at.
func (l *Ledger) Reset(at time.Time) error {
	_, err := l.db.Exec(`insert into breaker (id, tripped, reset_at) values (1, null, ?) on conflict (id) do update set tripped = null, reset_at = excluded.reset_at`, at.Unix())
	return err
}

// PRs are the URLs of every PR the ledger knows: accepted, or with a round.
func (l *Ledger) PRs() ([]string, error) {
	rows, err := l.db.Query(`select pr from acceptance union select pr from rounds order by pr`)
	if err != nil {
		return nil, fmt.Errorf("ledger prs: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var out []string
	for rows.Next() {
		var pr string
		if err := rows.Scan(&pr); err != nil {
			return nil, err
		}
		out = append(out, pr)
	}
	return out, rows.Err()
}

// UnreadRounds are rounds whose outcome has not been processed yet.
func (l *Ledger) UnreadRounds() ([]Round, error) {
	rows, err := l.db.Query(`select id, pr, head, trigger, started_at, retries,
		exists (select 1 from post_refusals f where f.pr = rounds.pr and f.at >= rounds.started_at)
		from rounds where read_at is null order by id`)
	if err != nil {
		return nil, fmt.Errorf("unread rounds: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var out []Round
	for rows.Next() {
		var r Round
		var started int64
		if err := rows.Scan(&r.ID, &r.PR, &r.Head, &r.Trigger, &started, &r.Retries, &r.PostRefused); err != nil {
			return nil, err
		}
		r.StartedAt = time.Unix(started, 0).UTC()
		out = append(out, r)
	}
	return out, rows.Err()
}

func (l *Ledger) MarkRead(id ids.RoundID) error {
	_, err := l.db.Exec(`update rounds set read_at = ? where id = ?`, time.Now().Unix(), id)
	return err
}

func (l *Ledger) MarkRetried(id ids.RoundID) error {
	_, err := l.db.Exec(`update rounds set retries = retries + 1 where id = ?`, id)
	return err
}

// MintToken makes the one-time token an Approve link carries for pr at sha.
func (l *Ledger) MintToken(pr PR, sha string) (ids.Token, error) {
	tok, err := ids.NewToken()
	if err != nil {
		return tok, err
	}
	_, err = l.db.Exec(`insert into tokens (token, pr, sha) values (?, ?, ?)`, tok, pr.URL(), sha)
	return tok, err
}

// UseToken spends tok if it was minted for pr at sha and never used.
func (l *Ledger) UseToken(pr PR, sha string, tok ids.Token) (bool, error) {
	var stored ids.Token
	err := l.db.QueryRow(`select token from tokens where pr = ? and sha = ? and used = 0 and token = ?`, pr.URL(), sha, tok).Scan(&stored)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil || subtle.ConstantTimeCompare([]byte(stored.String()), []byte(tok.String())) != 1 {
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
	// Expires is when elly stops showing it. Zero means refresh keeps it
	// alive: every tick re-sends it for another annotationTTL.
	Expires time.Time
}

type Link struct {
	Label string `json:"label"`
	URL   string `json:"url"`
	// Primary makes elly show the link as the row's action (the Start
	// offer); elly honours it only on an info annotation naming the PR.
	Primary bool `json:"primary,omitempty"`
}

const (
	// stateInfo carries only links: the Start offer, never a round state.
	stateInfo     = "info"
	stateHandled  = "handled"
	stateNeedsYou = "needs_you"
	stateRunning  = "running"
	// stateStuck is needs_you that nothing but the user will move. Only an
	// elly that knows it accepts it.
	stateStuck = "stuck"
)

// needsUser: the state asks the user to act, so the desktop hears of it.
func needsUser(state string) bool { return state == stateNeedsYou || state == stateStuck }

// Handover is a PR's history with the runner before a click: when its first
// round started, how many ran, and the note it last showed. Zero Rounds is a
// first handover.
type Handover struct {
	First    time.Time
	Rounds   int
	LastNote string
}

// Handover reads pr's history; call it before the click records its round.
func (l *Ledger) Handover(pr PR) (Handover, error) {
	var h Handover
	var first sql.NullInt64
	if err := l.db.QueryRow(`select count(*), min(started_at) from rounds where pr = ?`, pr.URL()).Scan(&h.Rounds, &first); err != nil {
		return Handover{}, err
	}
	if first.Valid {
		h.First = time.Unix(first.Int64, 0).UTC()
	}
	a, ok, err := l.Annotation(pr)
	if ok {
		h.LastNote = a.Note
	}
	return h, err
}

func (l *Ledger) SetAnnotation(pr PR, a Annotation) error {
	links, err := json.Marshal(a.Links)
	if err != nil {
		return err
	}
	var expires int64
	if !a.Expires.IsZero() {
		expires = a.Expires.Unix()
	}
	_, err = l.db.Exec(`replace into annotations (pr, state, note, links, expires) values (?, ?, ?, ?, ?)`, pr.URL(), a.State, a.Note, string(links), expires)
	return err
}

// Annotation is pr's stored annotation, and whether it has one.
func (l *Ledger) Annotation(pr PR) (Annotation, bool, error) {
	all, err := l.Annotations()
	if err != nil {
		return Annotation{}, false, err
	}
	a, ok := all[pr.URL()]
	return a, ok, nil
}

func (l *Ledger) ClearAnnotation(pr PR) error {
	_, err := l.db.Exec(`delete from annotations where pr = ?`, pr.URL())
	return err
}

// Annotations are the live annotations by PR URL, re-sent on every tick.
func (l *Ledger) Annotations() (map[string]Annotation, error) {
	rows, err := l.db.Query(`select pr, state, note, links, expires from annotations`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	out := map[string]Annotation{}
	for rows.Next() {
		var pr, links string
		var expires int64
		var a Annotation
		if err := rows.Scan(&pr, &a.State, &a.Note, &links, &expires); err != nil {
			return nil, err
		}
		if expires != 0 {
			a.Expires = time.Unix(expires, 0).UTC()
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

// RoundSources counts the rounds started since since by where they came
// from: a click by its link's ?from= ("unknown" for an older click or an
// untagged link), every other round as "tick". For `claude-review stats`.
func (l *Ledger) RoundSources(since time.Time) (map[string]int, error) {
	rows, err := l.db.Query(`select trigger from rounds where started_at >= ?`, since.Unix())
	if err != nil {
		return nil, fmt.Errorf("round sources: %w", err)
	}
	defer func() { _ = rows.Close() }()
	out := map[string]int{}
	for rows.Next() {
		var trigger string
		if err := rows.Scan(&trigger); err != nil {
			return nil, fmt.Errorf("round sources: %w", err)
		}
		out[triggerSource(trigger)]++
	}
	return out, rows.Err()
}

// triggerSource is the source clickTrigger wrote into trigger.
func triggerSource(trigger string) string {
	if !strings.HasPrefix(trigger, "click@") {
		return "tick"
	}
	if _, from, ok := strings.Cut(trigger, "?from="); ok && from != "" {
		return from
	}
	return "unknown"
}

// clickTrigger is the trigger a click's round is recorded under, with where
// the link was clicked (EntrySource) for `claude-review stats`.
func clickTrigger(at time.Time, from string) string {
	return "click@" + at.UTC().Format(time.RFC3339) + "?from=" + from
}

// metaClaudeVersion is the Claude Code version the last passing self-check ran on.
const metaClaudeVersion = "claude_version"

// Meta is a value the ledger keeps for the runner itself, "" when unset.
func (l *Ledger) Meta(key string) (string, error) {
	var v string
	err := l.db.QueryRow(`select value from meta where key = ?`, key).Scan(&v)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	return v, err
}

func (l *Ledger) SetMeta(key, value string) error {
	_, err := l.db.Exec(`replace into meta (key, value) values (?, ?)`, key, value)
	return err
}
