package main

import (
	"cmp"
	"flag"
	"fmt"
	"maps"
	"slices"
	"time"
)

// runStats prints how many rounds started in the last -days days, per
// source: where a click's link was clicked, or tick. Only started rounds
// count; a click that started nothing is in the journal's click timing
// lines.
func runStats(args []string, d Deps) int {
	fs := flag.NewFlagSet("claude-review stats", flag.ContinueOnError)
	fs.SetOutput(d.Stderr)
	days := fs.Int("days", 30, "how many days back")
	if err := fs.Parse(args); err != nil || fs.NArg() != 0 || *days < 1 {
		fmt.Fprintln(d.Stderr, "usage: claude-review stats [-days N]")
		return 2
	}
	l, err := OpenLedger(LedgerPath(d.StateDir))
	if err != nil {
		return fail(d, err)
	}
	defer func() { _ = l.Close() }()
	counts, err := l.RoundSources(time.Now().AddDate(0, 0, -*days))
	if err != nil {
		return fail(d, err)
	}
	sources := slices.SortedFunc(maps.Keys(counts), func(a, b string) int {
		return cmp.Or(counts[b]-counts[a], cmp.Compare(a, b))
	})
	for _, s := range sources {
		fmt.Fprintf(d.Stdout, "%-12s %d\n", s, counts[s])
	}
	return 0
}
