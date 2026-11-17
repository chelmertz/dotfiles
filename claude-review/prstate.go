package main

import (
	"database/sql"
	"encoding/json"
	"errors"
	"regexp"
	"strings"
	"time"
)

// The pr_state table holds each accepted PR's PRState as JSON, Prev chain
// included: it is read and written whole, never queried by field.

// PRState is pr's stored state, and whether it has one.
func (l *Ledger) PRState(pr PR) (PRState, bool, error) {
	var raw string
	err := l.db.QueryRow(`select state from pr_state where pr = ?`, pr.URL()).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return PRState{}, false, nil
	}
	if err != nil {
		return PRState{}, false, err
	}
	var s PRState
	return s, true, json.Unmarshal([]byte(raw), &s)
}

func (l *Ledger) SetPRState(pr PR, s PRState) error {
	raw, err := json.Marshal(s)
	if err != nil {
		return err
	}
	_, err = l.db.Exec(`replace into pr_state (pr, state) values (?, ?)`, pr.URL(), string(raw))
	return err
}

func (l *Ledger) DeletePRState(pr PR) error {
	_, err := l.db.Exec(`delete from pr_state where pr = ?`, pr.URL())
	return err
}

// migratePRState gives every accepted PR without a state one, on every
// open, so a PR a build without pr_state accepted is caught up later; it
// never overwrites a state. An unread round is
// running; otherwise the stored row maps by its state and note
// (migratedState), and anything unmapped is idle. Ignored PRs are idle:
// Ignore is gone.
func (l *Ledger) migratePRState() error {
	prs, err := l.acceptedURLs()
	if err != nil {
		return err
	}
	rows, err := l.Annotations()
	if err != nil {
		return err
	}
	tx, err := l.db.Begin()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	for _, url := range prs {
		s, err := l.migratedFor(url, rows[url])
		if err != nil {
			return err
		}
		raw, err := json.Marshal(s)
		if err != nil {
			return err
		}
		if _, err := tx.Exec(`insert or ignore into pr_state (pr, state) values (?, ?)`, url, string(raw)); err != nil {
			return err
		}
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	return l.uncapStuck()
}

// uncapStuck rewrites a stuck{post_refused} stored before 2026-11-17 whose
// round still posted its summary (Review set): decide now calls that round
// handled, and the stored row would otherwise hold the PR back for good.
func (l *Ledger) uncapStuck() error {
	all, err := l.PRStates()
	if err != nil {
		return err
	}
	for url, s := range all {
		if s.State == stStuck && s.Kind == kindRefused && s.Review != "" {
			s.State, s.Kind, s.Detail = stHandled, kindPosted, detailCapped
			raw, err := json.Marshal(s)
			if err != nil {
				return err
			}
			if _, err := l.db.Exec(`update pr_state set state = ? where pr = ?`, string(raw), url); err != nil {
				return err
			}
		}
	}
	return nil
}

func (l *Ledger) acceptedURLs() ([]string, error) {
	rows, err := l.db.Query(`select pr from acceptance where pr not in (select pr from pr_state)`)
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
	var trigger sql.NullString
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
	err = l.db.QueryRow(`select trigger, started_at from rounds where pr = ? and read_at is null order by id desc limit 1`, url).Scan(&trigger, &started)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return PRState{}, err
	}
	var s PRState
	switch {
	case trigger.Valid && strings.HasPrefix(trigger.String, "click"):
		s = PRState{State: stRunning, Kind: kindClick}
	case trigger.Valid:
		s = PRState{State: stRunning, Kind: kindTick}
	default:
		s = migratedState(a)
	}
	s.Head = head
	if started.Valid {
		s.Since = time.Unix(started.Int64, 0).UTC()
	}
	for _, link := range a.Links {
		if link.Label == labelRoundSummary {
			s.Review = link.URL
		}
	}
	return s, nil
}

var draftsNote = regexp.MustCompile(`^the round left (\d+) unsent comments`)

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
		case n == windowNote:
			return PRState{State: stHeld, Kind: kindWindow}
		}
	case stateStuck:
		if strings.HasPrefix(n, "a post was refused") {
			return PRState{State: stStuck, Kind: kindRefused}
		}
		return PRState{State: stStuck, Kind: kindFailed, Detail: n}
	case stateHandled:
		switch n {
		case noteRoundPosted:
			return PRState{State: stHandled, Kind: kindPosted}
		case noteNothing:
			return PRState{State: stHandled, Kind: kindNothing}
		case noteApprovalStands:
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
		out[url] = s
	}
	return out, rows.Err()
}
