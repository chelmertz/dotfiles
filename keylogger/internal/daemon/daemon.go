// Package daemon runs the capture loop: one goroutine owns the aggregator and
// serializes key events, context updates, periodic flushes, and shutdown.
package daemon

import (
	"time"

	"github.com/chelmertz/dotfiles/keylogger/internal/aggregate"
	"github.com/chelmertz/dotfiles/keylogger/internal/capture"
	"github.com/chelmertz/dotfiles/keylogger/internal/feeder"
	"github.com/chelmertz/dotfiles/keylogger/internal/model"
	"github.com/chelmertz/dotfiles/keylogger/internal/store"
)

type Options struct {
	FlushInterval time.Duration
	MaxDuration   time.Duration // 0 = no auto-stop
}

// Run consumes events and context updates until the source closes, MaxDuration
// elapses, or stop is signalled. Counts are flushed every FlushInterval and once
// more on exit, so a crash loses at most one interval of counts.
func Run(st *store.Store, sessionID int64, src capture.Source, ctx <-chan feeder.ContextMsg, stop <-chan struct{}, opt Options) error {
	agg := aggregate.New()
	focus := model.Context{}

	ticker := time.NewTicker(opt.FlushInterval)
	defer ticker.Stop()
	var timeout <-chan time.Time
	if opt.MaxDuration > 0 {
		timeout = time.After(opt.MaxDuration)
	}
	events := src.Events()
	flush := func() error { return st.Flush(sessionID, agg.Snapshot()) }

	for {
		select {
		case ev, ok := <-events:
			if !ok {
				return flush() // source drained (all keyboards gone)
			}
			agg.Add(ev)
		case m, ok := <-ctx:
			if !ok {
				ctx = nil // feeder gone; stop selecting on it
				continue
			}
			applyFocus(&focus, m)
			agg.SetContext(focus)
		case <-ticker.C:
			_ = flush() // best-effort; next tick retries
		case <-timeout:
			return flush()
		case <-stop:
			return flush()
		}
	}
}

// applyFocus merges a feeder update into the focus context: i3 owns app/workspace,
// nvim owns filetype. This is where the "where was I" gets assembled.
func applyFocus(f *model.Context, m feeder.ContextMsg) {
	switch m.Source {
	case "i3":
		f.App = m.App
		f.Workspace = m.Workspace
	case "nvim":
		f.Filetype = m.Filetype
	}
}
