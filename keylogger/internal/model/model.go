// Package model holds the shared domain types passed between the capture,
// aggregation, storage, metrics, rules and report layers. Keeping them in one
// dependency-free package avoids import cycles.
package model

// Modifier bitmask bits, stamped onto every KeyEvent by the capture layer.
const (
	ModShift = 1 << iota
	ModCtrl
	ModAlt
	ModSuper
)

// KeyEvent is one physical keydown, already cleaned by the capture layer:
// releases and auto-repeats are dropped, and Modmask reflects the modifiers
// held at the moment of the press. The aggregator never sees raw evdev. Device
// identifies which keyboard emitted it (the "laptop vs Keychron" axis).
type KeyEvent struct {
	Keycode int
	Modmask int
	Device  string
	TsMs    int64 // monotonic-ish milliseconds; only deltas are used
}

// Context is the "where was I" stamped onto each counted event. Any field may
// be empty (e.g. filetype outside nvim). A change in any field is treated as a
// discontinuity that resets bigram/skipgram chaining.
type Context struct {
	Device    string
	App       string // i3 window class
	Workspace string
	Filetype  string
}

// Session is one capture run.
type Session struct {
	ID        int64
	StartedAt int64
	EndedAt   int64 // 0 while running
	Host      string
	Note      string
}

// Unigram is one (context, key, modifier-combo) count.
type Unigram struct {
	Context
	Keycode int
	Modmask int
	Count   int64
}

// Bigram counts ordered adjacent key pairs plus summed inter-key gap, so the
// mean interval is IntervalSumMs/Count without storing any raw timing.
type Bigram struct {
	Context
	KC1, KC2      int
	Count         int64
	IntervalSumMs int64
}

// Skipgram counts key pairs one apart (skip-1), for same-finger-skipgram.
type Skipgram struct {
	Context
	KC1, KC2 int
	Count    int64
}

// Counts is the aggregator's flushable snapshot for one session.
type Counts struct {
	Unigrams  []Unigram
	Bigrams   []Bigram
	Skipgrams []Skipgram
}
