package main

import (
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// ledgerDump is every table's schema and rows but skip's, as text.
func ledgerDump(t *testing.T, path string, skip ...string) string {
	t.Helper()
	l, err := OpenLedgerReadOnly(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = l.Close() }()
	var tables []string
	var b strings.Builder
	rows, err := l.db.Query(`select name, coalesce(sql, '') from sqlite_master order by name`)
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var name, sql string
		if err := rows.Scan(&name, &sql); err != nil {
			t.Fatal(err)
		}
		fmt.Fprintln(&b, sql)
		if !slices.Contains(skip, name) && !strings.HasPrefix(name, "sqlite_") {
			tables = append(tables, name)
		}
	}
	_ = rows.Close()
	for _, name := range tables {
		rows, err := l.db.Query(`select * from ` + name + ` order by 1`) //nolint:gosec // a table name sqlite_master gave
		if err != nil {
			t.Fatal(err)
		}
		cols, _ := rows.Columns()
		for rows.Next() {
			vals := make([]any, len(cols))
			ptrs := make([]any, len(cols))
			for i := range vals {
				ptrs[i] = &vals[i]
			}
			if err := rows.Scan(ptrs...); err != nil {
				t.Fatal(err)
			}
			fmt.Fprintln(&b, name, vals)
		}
		_ = rows.Close()
	}
	return b.String()
}

// A round's post runs beside tick, outside any PR's lock, so it writes
// posts and post_refusals and nothing else: no migration, backfill, token
// spend or schema change. The ledger is baited with work for each of them
// (an accepted PR without a state, a round without its typed trigger, an
// unspent pre-C7 token), and every other table and the schema must come
// through a spent budget and a refusal unchanged.
func TestARoundsPostWritesOnlyTheBudget(t *testing.T) {
	h := newHost(t, "", true, false)
	h.d.Home = t.TempDir()
	h.d.StateDir = h.d.Home + "/.local/state" // the ledger stays in the test's dir
	path := LedgerPath(h.d.StateDir)
	l, err := OpenLedger(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := l.Accept(widgetsPR, "h0", "main", t0); err != nil {
		t.Fatal(err)
	}
	if err := l.SetAnnotation(widgetsPR, Annotation{State: stateHandled, Note: legacyPosted}); err != nil {
		t.Fatal(err)
	}
	if _, err := l.db.Exec(`insert into rounds (pr, head, trigger, started_at) values (?, 'h0', 'an old trigger', 1)`, widgetsPR.URL()); err != nil {
		t.Fatal(err)
	}
	if err := l.SetMeta(metaTokensRevoked, ""); err != nil {
		t.Fatal(err)
	}
	if _, err := l.MintToken(widgetsPR, "h0"); err != nil {
		t.Fatal(err)
	}
	_ = l.Close()
	before := ledgerDump(t, path, "posts", "post_refusals")

	for i := range newPostLimit + 1 {
		run([]string{"post", target, kindComment, bodyFlag, fmt.Sprint("finding ", i)}, h.d)
	}
	if !strings.Contains(h.stderr.String(), "limit of 12 posts") {
		t.Fatalf("the budget was not spent to its refusal: %s", h.stderr)
	}
	if after := ledgerDump(t, path, "posts", "post_refusals"); after != before {
		t.Errorf("post wrote beyond its budget:\nbefore\n%s\nafter\n%s", before, after)
	}
}

// On a ledger nothing else has opened, the budget's open creates its two
// tables and nothing more, and reads no round.
func TestOpenLedgerBudgetCreatesOnlyTheBudget(t *testing.T) {
	path := filepath.Join(t.TempDir(), "ledger.db")
	b, err := OpenLedgerBudget(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = b.Close() }()
	if tr, err := b.NewestTrigger(widgetsPR); err != nil || tr != nil {
		t.Errorf("a ledger without rounds: trigger %v, err %v", tr, err)
	}
	if ok, err := b.SpendPost(widgetsPR, "h0", postSlotNew, 1, t0); err != nil || !ok {
		t.Errorf("spend: %v %v", ok, err)
	}
	var names []string
	rows, err := b.db.Query(`select name from sqlite_master where type = 'table' order by name`)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var n string
		if err := rows.Scan(&n); err != nil {
			t.Fatal(err)
		}
		names = append(names, n)
	}
	if got := strings.Join(names, ","); got != "post_refusals,posts" {
		t.Errorf("tables %s, want post_refusals,posts", got)
	}
}
