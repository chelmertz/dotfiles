package main

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"
	"unicode"
)

// runLog is `claude-review log <pr>`: the PR's decision rows, oldest first,
// one line each. It is a human verb: it refuses itself inside any Claude
// Code session, rounds and forks included (a session asks to run it, then
// it refuses). It opens
// the ledger read-only, so it neither creates nor prunes anything.
func runLog(args []string, d Deps) int {
	if inClaudeSession(d.Environ) {
		fmt.Fprintln(d.Stderr, "claude-review log: refused inside a Claude Code session; run it in a terminal of your own")
		return 1
	}
	if len(args) != 1 {
		fmt.Fprintln(d.Stderr, "usage: claude-review log <pr>")
		return 2
	}
	pr, err := ParseTarget(args[0])
	if err != nil {
		return fail(d, err)
	}
	l, err := OpenLedgerReadOnly(LedgerPath(d.StateDir))
	if err != nil {
		return fail(d, err)
	}
	defer func() { _ = l.Close() }()
	rows, err := l.Decisions(pr)
	if err != nil {
		return fail(d, fmt.Errorf("claude-review log: %s: %w", pr.URL(), err))
	}
	for _, r := range rows {
		fmt.Fprintln(d.Stdout, escapeControl(r.line()))
	}
	return 0
}

// inClaudeSession: environ is a Claude Code session's, or a child's of
// one (sessionMarkers), the `!` prefix in a session's window included.
func inClaudeSession(environ []string) bool {
	for _, kv := range environ {
		if k, _, _ := strings.Cut(kv, "="); sessionMarkers[k] {
			return true
		}
	}
	return false
}

// decisionRow is one decisions row as log prints it; the nullable columns
// are empty when null.
type decisionRow struct {
	at                        int64
	origin, step              string
	event, from, to           string
	effects, factsKind, facts string
}

// Decisions is pr's decision rows, oldest first.
func (l *Ledger) Decisions(pr PR) ([]decisionRow, error) {
	rows, err := l.db.Query(`select at_ms, origin, step, coalesce(event, ''), coalesce(from_state, ''), coalesce(to_state, ''),
		effects, facts_kind, facts from decisions where pr = ? order by at_ms, id`, pr.URL())
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []decisionRow
	for rows.Next() {
		var r decisionRow
		if err := rows.Scan(&r.at, &r.origin, &r.step, &r.event, &r.from, &r.to, &r.effects, &r.factsKind, &r.facts); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// line is r as one line: time, origin, step, event, from → to, effects,
// facts kind and facts. A column that does not parse prints as stored.
func (r decisionRow) line() string {
	event := "-"
	if r.event != "" {
		var le loggedEvent
		event = r.event
		if json.Unmarshal([]byte(r.event), &le) == nil {
			event = string(le.Kind)
		}
	}
	var effects []string
	effectsText := r.effects
	if json.Unmarshal([]byte(r.effects), &effects) == nil {
		effectsText = "[" + strings.Join(effects, ",") + "]"
	}
	return strings.Join([]string{
		time.UnixMilli(r.at).UTC().Format("2006-01-02T15:04:05.000Z07:00"),
		r.origin, r.step, event, loggedStateText(r.from), "→", loggedStateText(r.to),
		effectsText, r.factsKind, r.facts,
	}, " ")
}

// loggedStateText is a logged state as its pair, "-" when null.
func loggedStateText(raw string) string {
	if raw == "" {
		return "-"
	}
	var s PRState
	if json.Unmarshal([]byte(raw), &s) != nil {
		return raw
	}
	if deleted(s) {
		return "deleted"
	}
	return describe(s)
}

// escapeControl writes every control or format character (escape
// sequences, newlines, bidi overrides) as a Go escape, so a row printed to
// a terminal is read, never acted on.
func escapeControl(s string) string {
	var b strings.Builder
	for _, r := range s {
		switch {
		case r == ' ' || unicode.IsGraphic(r):
			b.WriteRune(r)
		case r < 0x100:
			fmt.Fprintf(&b, `\x%02x`, r)
		default:
			fmt.Fprintf(&b, `\u%04x`, r)
		}
	}
	return b.String()
}

// pruneDecisions drops the decision rows past decisionRetention. Tick
// alone calls it; a dry run prunes nothing.
func (h tickHost) pruneDecisions() error {
	if h.dry {
		return nil
	}
	n, err := h.ledger.PruneDecisions(h.now())
	if err != nil {
		return fmt.Errorf("pruning the decision log: %w", err)
	}
	if n > 0 && h.out != nil {
		fmt.Fprintf(h.out, "claude-review tick: pruned %d decision rows older than %d days\n", n, int(decisionRetention.Hours()/24))
	}
	return nil
}
