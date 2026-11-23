package main

import (
	"crypto/subtle"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	_ "modernc.org/sqlite" // the ledger's driver, pure Go

	"github.com/chelmertz/dotfiles/claude-review/ids"
	"github.com/chelmertz/dotfiles/claude-review/prlock"
)

// Ledger is the runner's own state, apart from Claude Code's session store
// and from GitHub: which PRs the user accepted, which are ignored, which
// rounds ran, the approve tokens, and the annotation each PR currently has.
type Ledger struct {
	db *sql.DB
	// Unparsed counts rounds whose old trigger text no parser reads; tick
	// says so. They count toward the cap.
	Unparsed int
}

const ledgerSchema = `
create table if not exists acceptance (pr text primary key, head text not null, base text not null, at integer not null);
drop table if exists ignored;
create table if not exists rounds (
	id integer primary key, pr text not null, head text not null, trigger text not null,
	started_at integer not null, read_at integer, retries integer not null default 0,
	session text not null default '');
create table if not exists tokens (token text primary key, pr text not null, sha text not null, used integer not null default 0);
create table if not exists annotations (pr text primary key, state text not null, note text not null, links text not null, expires integer not null default 0);
create table if not exists meta (key text primary key, value text not null);
create table if not exists pr_state (pr text primary key, state text not null);
create table if not exists dismissals (pr text not null, review text not null, at integer not null, notified integer not null default 0, primary key (pr, review));
create table if not exists mention_gate (pr text primary key, readable integer not null, open text not null);
create table if not exists breaker (id integer primary key check (id = 1), tripped text, reset_at integer not null default 0);
` + budgetSchema

// LedgerPath is where the ledger lives under stateDir, the XDG state home
// (Deps.StateDir), beside nothing else.
func LedgerPath(stateDir string) string {
	return filepath.Join(stateDir, "claude-review", "ledger.db")
}

// OpenLedger opens the ledger read-write, creating it and its schema. It
// adds missing columns and fills derived ones (backfillTriggers), and spends
// pre-C7 tokens once; it does not touch any PR's state. The pr_state
// migrations run from tick alone, under each PR's lock (MigrateStatesOnce).
func OpenLedger(path string) (*Ledger, error) {
	db, err := openLedgerDB(path)
	if err != nil {
		return nil, err
	}
	if _, err := db.Exec(ledgerSchema); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("ledger schema: %w", err)
	}
	// "create table if not exists" adds no column to a ledger from before
	// it; on every later open the duplicate is the expected no-op.
	for _, alter := range []string{
		`alter table annotations add column expires integer not null default 0`,
		`alter table rounds add column session text not null default ''`,
		`alter table rounds add column trigger_kind text not null default ''`,
		`alter table rounds add column trigger_key text not null default ''`,
		`alter table rounds add column trigger_from text`,
		`alter table rounds add column transcript_from integer not null default 0`,
		`alter table rounds add column unreadable integer not null default 0`,
	} {
		if _, err := db.Exec(alter); err != nil && !strings.Contains(err.Error(), "duplicate column") {
			_ = db.Close()
			return nil, fmt.Errorf("ledger schema: %w", err)
		}
	}
	l := &Ledger{db: db}
	// Both stay on every open: backfillTriggers fills a round row's derived
	// columns from that row alone, the same values whichever process writes
	// them, and a rolled-back binary keeps writing rows without them;
	// revokePreC7Tokens runs once by its own flag, and a token it spends is
	// only re-minted on the next show. Neither creates or changes a state.
	if l.Unparsed, err = l.backfillTriggers(); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("ledger triggers: %w", err)
	}
	if err := l.revokePreC7Tokens(); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("ledger tokens: %w", err)
	}
	return l, nil
}

// openLedgerDB opens path read-write in WAL mode, creating its directory.
func openLedgerDB(path string) (*sql.DB, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, fmt.Errorf("ledger dir: %w", err)
	}
	// _txlock=immediate: every transaction takes the write lock at BEGIN, so
	// one that reads and then writes waits for another writer (tick beside a
	// click) instead of failing with SQLITE_BUSY_SNAPSHOT, which busy_timeout
	// does not retry.
	db, err := sql.Open("sqlite", "file:"+path+"?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)&_txlock=immediate")
	if err != nil {
		return nil, fmt.Errorf("open ledger: %w", err)
	}
	if err := assertWAL(db); err != nil {
		_ = db.Close()
		return nil, err
	}
	return db, nil
}

// BudgetLedger is the ledger as a round's post verb sees it: it spends and
// refuses posts and reads the newest round's trigger, and has no other
// method, so the verb a round runs beside tick writes nothing but posts and
// post_refusals.
type BudgetLedger struct{ db *sql.DB }

// budgetSchema is the part of ledgerSchema the post budget writes.
const budgetSchema = `
create table if not exists posts (pr text not null, head text not null, slot text not null, n integer not null, primary key (pr, head, slot));
create table if not exists post_refusals (pr text not null, at integer not null);
`

// OpenLedgerBudget opens the ledger for the post verb inside a round: no
// migration, backfill or token spend, and no schema beyond the budget's two
// tables, which a ledger tick has not yet created may lack.
func OpenLedgerBudget(path string) (*BudgetLedger, error) {
	db, err := openLedgerDB(path)
	if err != nil {
		return nil, err
	}
	if _, err := db.Exec(budgetSchema); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("ledger schema: %w", err)
	}
	return &BudgetLedger{db: db}, nil
}

func (b *BudgetLedger) Close() error { return b.db.Close() }

// SpendPost is Ledger.SpendPost.
func (b *BudgetLedger) SpendPost(pr PR, head, slot string, limit int, at time.Time) (bool, error) {
	return spendPost(b.db, pr, head, slot, limit, at)
}

// NewestTrigger is Ledger.NewestTrigger; a ledger without rounds, which
// tick or a click has not opened yet, has no round.
func (b *BudgetLedger) NewestTrigger(pr PR) (Trigger, error) {
	var n int
	if err := b.db.QueryRow(`select count(*) from sqlite_master where type = 'table' and name = 'rounds'`).Scan(&n); err != nil || n == 0 {
		return nil, err
	}
	return newestTrigger(b.db, pr)
}

// OpenLedgerReadOnly opens an existing ledger for reading only, as what a
// session or a page reaches does (diff's base, the states page): no schema,
// migration, backfill or token spend runs, and SQLite refuses any write. A
// missing ledger is an error, never created.
func OpenLedgerReadOnly(path string) (*Ledger, error) {
	if _, err := os.Stat(path); err != nil {
		return nil, fmt.Errorf("open ledger: %w", err)
	}
	db, err := sql.Open("sqlite", "file:"+path+"?mode=ro&_pragma=busy_timeout(5000)")
	if err != nil {
		return nil, fmt.Errorf("open ledger: %w", err)
	}
	if err := db.Ping(); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("open ledger: %w", err)
	}
	return &Ledger{db: db}, nil
}

func (l *Ledger) Close() error { return l.db.Close() }

// assertWAL refuses a ledger whose journal mode is not WAL: the pragma in the
// DSN is a request SQLite may decline (a filesystem without shared memory),
// and a rollback journal turns a reader beside a writer into SQLITE_BUSY.
func assertWAL(db *sql.DB) error {
	var mode string
	if err := db.QueryRow(`pragma journal_mode`).Scan(&mode); err != nil {
		return fmt.Errorf("ledger journal mode: %w", err)
	}
	if !strings.EqualFold(mode, "wal") {
		return fmt.Errorf("ledger journal mode is %q, want wal", mode)
	}
	return nil
}

// Acceptance is the user's first-look click: the head and base it saw.
type Acceptance struct {
	Head string
	Base string
}

// Accept records a click on pr: the head and base the user looked at.
func (l *Ledger) Accept(pr PR, head, base string, at time.Time) error {
	if _, err := l.db.Exec(`replace into acceptance (pr, head, base, at) values (?, ?, ?, ?)`, pr.URL(), head, base, at.Unix()); err != nil {
		return fmt.Errorf("accept: %w", err)
	}
	return nil
}

func (l *Ledger) Acceptance(pr PR) (Acceptance, bool, error) {
	var a Acceptance
	err := l.db.QueryRow(`select head, base from acceptance where pr = ?`, pr.URL()).Scan(&a.Head, &a.Base)
	if errors.Is(err, sql.ErrNoRows) {
		return Acceptance{}, false, nil
	}
	return a, err == nil, err
}

// Round is one review round, recorded once its worker is running: tick's,
// or a click's.
type Round struct {
	ID        ids.RoundID
	PR        string
	Head      string
	Trigger   Trigger // nil for a row whose old text no parser reads
	StartedAt time.Time
	Retries   int
	// PostRefused: a post on the PR was refused over its limit since the
	// round started.
	PostRefused bool
	// Session is the session the round started in, empty for one recorded
	// before rounds stored it.
	Session ids.SessionID
	// TranscriptFrom is the session transcript's byte size at the launch
	// of the round, or of its retry: where its own lines start. 0, for a
	// round recorded before rounds stored it, reads the whole transcript.
	TranscriptFrom int64
	// Unreadable counts the passes whose read of the round's transcript
	// failed.
	Unreadable int
}

// SessionFor is the session r runs in: the one it started in, which a
// policyVersion bump since does not change. A round from before rounds
// stored it falls back to the current round session.
func (r Round) SessionFor(pr PR) ids.SessionID {
	if r.Session.IsZero() {
		return pr.RoundSessionID()
	}
	return r.Session
}

// StartRoundFrom records a round whose session transcript was from bytes
// long at its launch. The old trigger text is still written, for an older
// binary's counts and RanAt.
func (l *Ledger) StartRoundFrom(pr PR, head string, trigger Trigger, at time.Time, from int64) (ids.RoundID, error) {
	var id ids.RoundID
	if err := l.db.QueryRow(`insert into rounds (pr, head, trigger, trigger_kind, trigger_key, trigger_from, started_at, session, transcript_from)
		values (?, ?, ?, ?, ?, nullif(?, ''), ?, ?, ?) returning id`,
		pr.URL(), head, trigger.Text(), trigger.Kind(), trigger.DedupeKey(), triggerFrom(trigger), at.Unix(), pr.RoundSessionID().String(), from).Scan(&id); err != nil {
		return id, fmt.Errorf("start round: %w", err)
	}
	return id, nil
}

// RanAt reports whether a round already started for pr at head for
// trigger, or any round at head since since.
func (l *Ledger) RanAt(pr PR, head string, trigger Trigger, since time.Time) (bool, error) {
	var n int
	err := l.db.QueryRow(`select count(*) from rounds where pr = ? and head = ? and ((trigger_kind = ? and trigger_key = ?) or started_at >= ?)`,
		pr.URL(), head, trigger.Kind(), trigger.DedupeKey(), since.Unix()).Scan(&n)
	return n > 0, err
}

// RoundsSince counts pr's rounds tick started at or after since, for the
// daily cap. A click's round is the user's own and does not count.
func (l *Ledger) RoundsSince(pr PR, since time.Time) (int, error) {
	var n int
	err := l.db.QueryRow(`select count(*) from rounds where pr = ? and started_at >= ? and trigger_kind not in (`+freeKindsSQL()+`)`, pr.URL(), since.Unix()).Scan(&n)
	return n, err
}

// SpendPost takes one post from pr's budget in slot at head, and reports
// false, recording the refusal, when limit posts were already made there.
// One conditional update, so two posts at once cannot both take the last.
func (l *Ledger) SpendPost(pr PR, head, slot string, limit int, at time.Time) (bool, error) {
	return spendPost(l.db, pr, head, slot, limit, at)
}

func spendPost(db *sql.DB, pr PR, head, slot string, limit int, at time.Time) (bool, error) {
	if _, err := db.Exec(`insert or ignore into posts (pr, head, slot, n) values (?, ?, ?, 0)`, pr.URL(), head, slot); err != nil {
		return false, err
	}
	res, err := db.Exec(`update posts set n = n + 1 where pr = ? and head = ? and slot = ? and n < ?`, pr.URL(), head, slot, limit)
	if err != nil {
		return false, err
	}
	if n, err := res.RowsAffected(); err != nil || n == 1 {
		return n == 1, err
	}
	_, err = db.Exec(`insert into post_refusals (pr, at) values (?, ?)`, pr.URL(), at.Unix())
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
	err := l.db.QueryRow(`select coalesce(sum(retries + (trigger_kind not in (`+freeKindsSQL()+`))), 0) from rounds
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
	rows, err := l.db.Query(`select id, pr, head, trigger_kind, trigger_key, coalesce(trigger_from, ''), started_at, retries, session,
		transcript_from, unreadable, exists (select 1 from post_refusals f where f.pr = rounds.pr and f.at >= rounds.started_at)
		from rounds where read_at is null order by id`)
	if err != nil {
		return nil, fmt.Errorf("unread rounds: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var out []Round
	for rows.Next() {
		var r Round
		var started int64
		var session, kind, key, from string
		if err := rows.Scan(&r.ID, &r.PR, &r.Head, &kind, &key, &from, &started, &r.Retries, &session, &r.TranscriptFrom, &r.Unreadable, &r.PostRefused); err != nil {
			return nil, err
		}
		r.Trigger = triggerOf(kind, key, from)
		if session != "" {
			s, err := ids.ParseSession(session)
			if err != nil {
				return nil, fmt.Errorf("round %s: %w", r.ID, err)
			}
			r.Session = s
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

// LastSettled is when pr's last settled round started and settled, and
// whether one was.
func (l *Ledger) LastSettled(pr PR) (started, read time.Time, ok bool, err error) {
	var s, r int64
	err = l.db.QueryRow(`select started_at, read_at from rounds where pr = ? and read_at is not null order by read_at desc, id desc limit 1`, pr.URL()).Scan(&s, &r)
	if errors.Is(err, sql.ErrNoRows) {
		return time.Time{}, time.Time{}, false, nil
	}
	return time.Unix(s, 0).UTC(), time.Unix(r, 0).UTC(), err == nil, err
}

// MarkRetried counts a retry of round id, whose session transcript was
// from bytes long at the retry's launch: the retry reads from there, and
// its reads start a fresh unreadable count.
func (l *Ledger) MarkRetried(id ids.RoundID, from int64) error {
	_, err := l.db.Exec(`update rounds set retries = retries + 1, transcript_from = ?, unreadable = 0 where id = ?`, from, id)
	return err
}

// MarkUnreadable counts a pass that could not read round id's transcript.
func (l *Ledger) MarkUnreadable(id ids.RoundID) error {
	_, err := l.db.Exec(`update rounds set unreadable = unreadable + 1 where id = ?`, id)
	return err
}

// MintToken makes the one-time token an Approve link carries for pr at sha.
// Precondition: approveAllowed; otherwise errMentionOpen and no token.
func (l *Ledger) MintToken(pr PR, sha string) (ids.Token, error) {
	if err := l.approveAllowed(pr); err != nil {
		return ids.Token{}, err
	}
	tok, err := ids.NewToken()
	if err != nil {
		return tok, err
	}
	_, err = l.db.Exec(`insert into tokens (token, pr, sha) values (?, ?, ?)`, tok, pr.URL(), sha)
	return tok, err
}

// TokenFor is pr's newest unused token at sha, for re-rendering its
// Approve link; ok is false when none is left.
func (l *Ledger) TokenFor(pr PR, sha string) (ids.Token, bool, error) {
	var tok ids.Token
	err := l.db.QueryRow(`select token from tokens where pr = ? and sha = ? and used = 0 order by rowid desc limit 1`, pr.URL(), sha).Scan(&tok)
	if errors.Is(err, sql.ErrNoRows) {
		return tok, false, nil
	}
	return tok, err == nil, err
}

// SetForkSession records the session id the last fork of pr runs under.
func (l *Ledger) SetForkSession(pr PR, id ids.SessionID) error {
	return l.SetMeta(metaForkPrefix+pr.URL(), id.String())
}

// ForkSession is the id SetForkSession recorded for pr.
func (l *Ledger) ForkSession(pr PR) (ids.SessionID, bool, error) {
	v, err := l.Meta(metaForkPrefix + pr.URL())
	if err != nil || v == "" {
		return ids.SessionID{}, false, err
	}
	id, err := ids.ParseSession(v)
	return id, err == nil, err
}

const metaForkPrefix = "fork-session "

// HasLiveToken says whether any Approve token for pr is unused.
func (l *Ledger) HasLiveToken(pr PR) (bool, error) {
	var n int
	err := l.db.QueryRow(`select count(*) from tokens where pr = ? and used = 0`, pr.URL()).Scan(&n)
	return n > 0, err
}

// metaTokensRevoked is set once every token minted before errors and notes
// stopped carrying them has been spent.
const metaTokensRevoked = "tokens-revoked-c7"

// revokePreC7Tokens spends every unspent token once: one may sit in an old
// log line or journal. An Approve row gets a fresh token when next shown.
func (l *Ledger) revokePreC7Tokens() error {
	done, err := l.Meta(metaTokensRevoked)
	if err != nil || done != "" {
		return err
	}
	if _, err := l.db.Exec(`update tokens set used = 1 where used = 0`); err != nil {
		return err
	}
	return l.SetMeta(metaTokensRevoked, "1")
}

// Dismissal is a dismissal of the user's approval that apply asked for and
// nobody has yet seen land. It outlives the PR's state, so a closed PR's
// dismissal is still retried.
type Dismissal struct {
	PR       string
	Review   ids.ReviewID
	Notified bool // a refusal was notified once
}

// AddDismissal records that review on pr is to be dismissed.
func (l *Ledger) AddDismissal(pr PR, review ids.ReviewID, at time.Time) error {
	_, err := l.db.Exec(`insert into dismissals (pr, review, at) values (?, ?, ?) on conflict do nothing`, pr.URL(), review.String(), at.Unix())
	return err
}

// DismissalPending: a dismissal of pr's approval is recorded and not seen.
func (l *Ledger) DismissalPending(pr PR) (bool, error) {
	var n int
	err := l.db.QueryRow(`select count(*) from dismissals where pr = ?`, pr.URL()).Scan(&n)
	return n > 0, err
}

// PendingDismissals are every recorded dismissal, oldest first.
func (l *Ledger) PendingDismissals() ([]Dismissal, error) {
	rows, err := l.db.Query(`select pr, review, notified from dismissals order by at, pr`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []Dismissal
	for rows.Next() {
		var d Dismissal
		var review string
		if err := rows.Scan(&d.PR, &review, &d.Notified); err != nil {
			return nil, err
		}
		if err := d.Review.UnmarshalJSON([]byte(review)); err != nil {
			return nil, fmt.Errorf("dismissal of %s: review %w", d.PR, err)
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

// MarkDismissalNotified stores that d's refusal was notified.
func (l *Ledger) MarkDismissalNotified(d Dismissal) error {
	_, err := l.db.Exec(`update dismissals set notified = 1 where pr = ? and review = ?`, d.PR, d.Review.String())
	return err
}

// SettleDismissal forgets d, seen dismissed or dropped, under its PR's
// lock lk: minting reads it.
func (l *Ledger) SettleDismissal(lk prlock.Locked, d Dismissal) error {
	pr, err := ParseTarget(d.PR)
	if err != nil {
		return err
	}
	if err := lk.Check(prKey(pr)); err != nil {
		return err
	}
	_, err = l.db.Exec(`delete from dismissals where pr = ? and review = ?`, d.PR, d.Review.String())
	return err
}

// RevokeTokens spends every token minted for pr.
func (l *Ledger) RevokeTokens(pr PR) error {
	_, err := l.db.Exec(`update tokens set used = 1 where pr = ?`, pr.URL())
	return err
}

// MarkPRRead marks every unread round of pr read.
func (l *Ledger) MarkPRRead(pr PR, at time.Time) error {
	_, err := l.db.Exec(`update rounds set read_at = ? where pr = ? and read_at is null`, at.Unix(), pr.URL())
	return err
}

// UseToken spends tok if it was minted for pr at sha and never used.
func (l *Ledger) UseToken(pr PR, sha string, tok ids.Token) (bool, error) {
	if err := l.approveAllowed(pr); err != nil {
		return false, err
	}
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
	// alive: every tick re-sends it for another rowTTL.
	Expires time.Time
}

type Link struct {
	Label string `json:"label"`
	URL   string `json:"url"`
	// Primary makes elly show the link as the row's action (the Start
	// offer); elly honours it only on an info annotation naming the PR.
	Primary bool `json:"primary,omitempty"`
	// Action is what the link does, one of actionVerbs' keys; elly reads it
	// and never the label.
	Action string `json:"action,omitempty"`
}

// actionVerbs is the action table: each action and the link verb it names,
// "" for the bare PR link. elly's types.ActionVerbs names the same pairs;
// each side pins them in a test.
var actionVerbs = map[string]string{actionApprove: "approve", actionOpen: "attach", actionStart: "", actionHelp: "states"}

const (
	actionApprove = "approve"
	actionOpen    = "open"
	actionStart   = "start"
	actionHelp    = "help"
)

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

// NewestTrigger is the trigger of pr's newest round, nil with none or
// for a row no parser reads.
func (l *Ledger) NewestTrigger(pr PR) (Trigger, error) {
	return newestTrigger(l.db, pr)
}

func newestTrigger(db *sql.DB, pr PR) (Trigger, error) {
	var kind, key string
	var from sql.NullString
	err := db.QueryRow(`select trigger_kind, trigger_key, trigger_from from rounds where pr = ? order by id desc limit 1`, pr.URL()).Scan(&kind, &key, &from)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return triggerOf(kind, key, from.String), nil
}

// LastRoundStart is when pr's newest round started; zero with none.
func (l *Ledger) LastRoundStart(pr PR) (time.Time, error) {
	var at sql.NullInt64
	err := l.db.QueryRow(`select max(started_at) from rounds where pr = ?`, pr.URL()).Scan(&at)
	if err != nil || !at.Valid {
		return time.Time{}, err
	}
	return time.Unix(at.Int64, 0).UTC(), nil
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
	rows, err := l.db.Query(`select trigger_kind, coalesce(trigger_from, '') from rounds where started_at >= ?`, since.Unix())
	if err != nil {
		return nil, fmt.Errorf("round sources: %w", err)
	}
	defer func() { _ = rows.Close() }()
	out := map[string]int{}
	for rows.Next() {
		var kind, from string
		if err := rows.Scan(&kind, &from); err != nil {
			return nil, fmt.Errorf("round sources: %w", err)
		}
		out[triggerSource(kind, from)]++
	}
	return out, rows.Err()
}

// triggerSource is where a round came from: a click's link tag, "unknown"
// for an untagged click, "tick" for any other.
func triggerSource(kind, from string) string {
	switch {
	case kind != trigClick:
		return "tick"
	case from != "":
		return from
	}
	return "unknown"
}

// backfillTriggers fills the typed columns of rows that have only the old
// text: from before them, or written by a rolled-back binary. It runs on
// every read-write open but the post budget's; a row no parser reads gets
// kind trigUnparsed (counted toward the cap, never deduplicated) and is
// counted.
func (l *Ledger) backfillTriggers() (int, error) {
	rows, err := l.db.Query(`select id, trigger, head from rounds where trigger_kind = ''`)
	if err != nil {
		return 0, err
	}
	type fill struct {
		id              int64
		kind, key, from string
	}
	var fills []fill
	unparsed := 0
	for rows.Next() {
		var id int64
		var text, head string
		if err := rows.Scan(&id, &text, &head); err != nil {
			_ = rows.Close()
			return 0, err
		}
		t, err := parseTrigger(text, head)
		if err != nil {
			unparsed++
			fills = append(fills, fill{id, trigUnparsed, strconv.FormatInt(id, 10), ""})
			continue
		}
		fills = append(fills, fill{id, t.Kind(), t.DedupeKey(), triggerFrom(t)})
	}
	if err := errors.Join(rows.Err(), rows.Close()); err != nil {
		return 0, err
	}
	for _, f := range fills {
		if _, err := l.db.Exec(`update rounds set trigger_kind = ?, trigger_key = ?, trigger_from = nullif(?, '') where id = ?`, f.kind, f.key, f.from, f.id); err != nil {
			return 0, err
		}
	}
	return unparsed, nil
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

// RoundSession is the session pr's rounds run in, as the round's row
// recorded it: the newest unread round's, else the newest round's. With no
// round, or only rounds from before the session was recorded, it is
// today's id. Readers name a round's session through this, never through
// RoundSessionID, which a policy version bump changes under a live worker.
func (l *Ledger) RoundSession(pr PR) (ids.SessionID, error) {
	var s string
	err := l.db.QueryRow(`select session from rounds where pr = ? order by read_at is not null, id desc limit 1`, pr.URL()).Scan(&s)
	if errors.Is(err, sql.ErrNoRows) || err == nil && s == "" {
		return pr.RoundSessionID(), nil
	}
	if err != nil {
		return ids.SessionID{}, err
	}
	return ids.ParseSession(s)
}

// RoundWindow is when round id started and, if read, when it was read.
func (l *Ledger) RoundWindow(id ids.RoundID) (started, read time.Time, wasRead bool, err error) {
	var s int64
	var r sql.NullInt64
	if err := l.db.QueryRow(`select started_at, read_at from rounds where id = ?`, id).Scan(&s, &r); err != nil {
		return time.Time{}, time.Time{}, false, fmt.Errorf("round %s: %w", id, err)
	}
	started = time.Unix(s, 0).UTC()
	if r.Valid {
		read, wasRead = time.Unix(r.Int64, 0).UTC(), true
	}
	return started, read, wasRead, nil
}

// DeleteMeta removes key; a key not there is no error.
func (l *Ledger) DeleteMeta(key string) error {
	_, err := l.db.Exec(`delete from meta where key = ?`, key)
	return err
}
