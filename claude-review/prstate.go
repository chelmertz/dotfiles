package main

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/chelmertz/dotfiles/claude-review/prlock"
)

// The pr_state table holds each accepted PR's PRState as JSON, Prev chain
// included: it is read and written whole, never queried by field.

// PRState is pr's stored state, and whether it has one.
func (l *Ledger) PRState(pr PR) (PRState, bool, error) { return prStateIn(l.db, pr) }

// prStateIn is pr's stored state read through q, a transaction's or the
// ledger's.
func prStateIn(q querier, pr PR) (PRState, bool, error) {
	var raw string
	err := q.QueryRow(`select state from pr_state where pr = ?`, pr.URL()).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return PRState{}, false, nil
	}
	if err != nil {
		return PRState{}, false, err
	}
	var s PRState
	if err := json.Unmarshal([]byte(raw), &s); err != nil {
		return s, true, err
	}
	return normalized(s), true, nil
}

func (l *Ledger) SetPRState(pr PR, s PRState) error { return setPRState(l.db, pr, s) }

func setPRState(q querier, pr PR, s PRState) error {
	if deleted(s) {
		return errors.New("a closed PR's state is deleted, never stored")
	}
	raw, err := json.Marshal(s)
	if err != nil {
		return err
	}
	_, err = q.Exec(`replace into pr_state (pr, state) values (?, ?)`, pr.URL(), string(raw))
	return err
}

// DeletePRState deletes pr's state and what it kept beside it: the fork's
// session, the update time GitHub last had nothing new at, and the mark of
// a row elly refused.
func (l *Ledger) DeletePRState(pr PR) error {
	tx, err := l.db.Begin()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if err := deletePRState(tx, pr); err != nil {
		return err
	}
	return tx.Commit()
}

// deletePRState is DeletePRState inside the caller's transaction. The
// state goes last, as in every Commit.
func deletePRState(q querier, pr PR) error {
	if _, err := q.Exec(`delete from meta where key in (?, ?, ?, ?)`, metaForkPrefix+pr.URL(), metaStandingPrefix+pr.URL(), metaHiddenPrefix+pr.URL(), metaNotifiedPrefix+pr.URL()); err != nil {
		return err
	}
	_, err := q.Exec(`delete from pr_state where pr = ?`, pr.URL())
	return err
}

// The pr_state migrations below exist for ledgers written before pr_state
// (2026-11-16) and before the cap counted a refused round as finished
// (2026-11-17). Tick runs them once, under each PR's lock, and then sets
// metaStatesMigrated. Delete MigrateStatesOnce, migrateOne, migratedFor,
// migratedState and the legacy texts in the release after the one that
// added the flag: by then every ledger has it, and nothing calls them.

// metaStatesMigrated is set once a tick ran the pr_state migrations over
// every PR, skipping none for its lock. After it they never run again, so a
// closed PR, which keeps its acceptance and loses its state, is never given
// a state back from its old row.
const metaStatesMigrated = "pr-state-migrated"

// MigrateStatesOnce runs the pr_state migrations, unless the flag says they
// ran, over every accepted PR and every PR with a state, each under its
// lock taken through take. A PR whose lock a click holds is skipped and
// counted; the flag is set only on a pass that skipped none.
func (l *Ledger) MigrateStatesOnce(take prlock.Taker) (skipped int, err error) {
	done, err := l.Meta(metaStatesMigrated)
	if err != nil || done != "" {
		return 0, err
	}
	urls, err := l.migrationURLs()
	if err != nil {
		return 0, err
	}
	rows, err := l.Annotations()
	if err != nil {
		return 0, err
	}
	for _, url := range urls {
		pr, err := ParseTarget(url)
		if err != nil {
			return skipped, fmt.Errorf("migrating %s: %w", url, err)
		}
		lk, err := lockPR(pr, take)
		if errors.Is(err, errLocked) {
			skipped++
			continue
		}
		if err != nil {
			return skipped, err
		}
		err = l.migrateOne(lk, pr, rows[url])
		lk.Unlock()
		if err != nil {
			return skipped, fmt.Errorf("migrating %s: %w", url, err)
		}
	}
	if skipped > 0 {
		return skipped, nil
	}
	return 0, l.SetMeta(metaStatesMigrated, "1")
}

// migrateOne migrates pr under its lock. A PR without a state gets one: an
// unread round is running; otherwise the stored row a maps by its state
// and note (migratedState), and anything unmapped is idle. A state is never
// overwritten, except a stuck{post_refused} whose round still posted its
// summary (Review set): decide now calls that round handled, and the stored
// state would otherwise hold the PR back for good.
func (l *Ledger) migrateOne(lk prlock.Locked, pr PR, a Annotation) error {
	if err := lk.Check(prKey(pr)); err != nil {
		return err
	}
	s, ok, err := l.PRState(pr)
	if err != nil {
		return err
	}
	if !ok {
		if s, err = l.migratedFor(pr.URL(), a); err != nil {
			return err
		}
	}
	uncap := s.State == stStuck && s.Kind == kindRefused && s.Review != ""
	if uncap {
		s.State, s.Kind, s.Detail = stHandled, kindPosted, detailCapped
	}
	if ok && !uncap {
		return nil
	}
	raw, err := json.Marshal(s)
	if err != nil {
		return err
	}
	_, err = l.db.Exec(`replace into pr_state (pr, state) values (?, ?)`, pr.URL(), string(raw))
	return err
}

// migrationURLs is every accepted PR and every PR with a state.
func (l *Ledger) migrationURLs() ([]string, error) {
	rows, err := l.db.Query(`select pr from acceptance union select pr from pr_state`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []string
	for rows.Next() {
		var u string
		if err := rows.Scan(&u); err != nil {
			return nil, err
		}
		out = append(out, u)
	}
	return out, rows.Err()
}

func (l *Ledger) migratedFor(url string, a Annotation) (PRState, error) {
	var head, accepted string
	var kind sql.NullString
	var started sql.NullInt64
	if err := l.db.QueryRow(`select head from acceptance where pr = ?`, url).Scan(&accepted); err != nil {
		return PRState{}, err
	}
	err := l.db.QueryRow(`select head from rounds where pr = ? order by id desc limit 1`, url).Scan(&head)
	if errors.Is(err, sql.ErrNoRows) {
		head, err = accepted, nil
	}
	if err != nil {
		return PRState{}, err
	}
	err = l.db.QueryRow(`select trigger_kind, started_at from rounds where pr = ? and read_at is null order by id desc limit 1`, url).Scan(&kind, &started)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return PRState{}, err
	}
	var s PRState
	switch {
	case kind.Valid && kind.String == trigClick:
		s = PRState{State: stRunning, Kind: kindClick}
	case kind.Valid:
		s = PRState{State: stRunning, Kind: kindTick}
	default:
		s = migratedState(a)
	}
	s.Head = head
	if started.Valid {
		s.Since = time.Unix(started.Int64, 0).UTC()
	}
	for _, link := range a.Links {
		if link.Label == legacySummaryLabel {
			s.Review = link.URL
		}
	}
	return s, nil
}

var draftsNote = regexp.MustCompile(`^the round left (\d+) unsent comments`)

// The old code's texts, frozen: rows show other words now, and only rows
// written before pr_state existed carry these.
const (
	legacySummaryLabel = "Round summary"
	legacyWindowNote   = "review window open; close it to let rounds run"
	legacyPosted       = "round posted its review"
	legacyNothing      = "round found nothing to post"
	legacyApproval     = "clean round; your approval stands"
)

// migratedState maps a row the old code wrote to the state it meant. The
// notes are the old code's exact texts; a running row without an unread
// round, a paused row and anything unknown are idle, recomputed next tick.
func migratedState(a Annotation) PRState {
	n := a.Note
	switch a.State {
	case stateNeedsYou:
		switch {
		case strings.HasPrefix(n, "the round has a question for you"):
			return PRState{State: stAwaiting, Kind: kindQuestion, Detail: strings.TrimPrefix(strings.TrimPrefix(n, "the round has a question for you"), ": ")}
		case strings.HasPrefix(n, "LGTM at "):
			return PRState{State: stAwaiting, Kind: kindApprove}
		case strings.HasPrefix(n, "new findings after your approval"):
			return PRState{State: stAwaiting, Kind: kindDismiss}
		case draftsNote.MatchString(n):
			return PRState{State: stAwaiting, Kind: kindDrafts, Detail: draftsNote.FindStringSubmatch(n)[1]}
		case strings.HasPrefix(n, "the PR changed under its first look"):
			return PRState{State: stHeld, Kind: kindFresh}
		case n == legacyWindowNote:
			return PRState{State: stHeld, Kind: kindWindow}
		}
	case stateStuck:
		if strings.HasPrefix(n, "a post was refused") {
			return PRState{State: stStuck, Kind: kindRefused}
		}
		return PRState{State: stStuck, Kind: kindFailed, Detail: n}
	case stateHandled:
		switch n {
		case legacyPosted:
			return PRState{State: stHandled, Kind: kindPosted}
		case legacyNothing:
			return PRState{State: stHandled, Kind: kindNothing}
		case legacyApproval:
			return PRState{State: stHandled, Kind: kindApproved}
		}
	}
	return PRState{State: stIdle}
}

// PRStates is every stored state by PR URL.
func (l *Ledger) PRStates() (map[string]PRState, error) {
	rows, err := l.db.Query(`select pr, state from pr_state`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	out := map[string]PRState{}
	for rows.Next() {
		var url, raw string
		if err := rows.Scan(&url, &raw); err != nil {
			return nil, err
		}
		var s PRState
		if err := json.Unmarshal([]byte(raw), &s); err != nil {
			return nil, err
		}
		out[url] = normalized(s)
	}
	return out, rows.Err()
}
