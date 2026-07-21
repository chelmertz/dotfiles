// Package aggregate turns a stream of keydowns into per-context counts, holding
// only the previous key(s) in memory — never the ordered sequence. It is the
// heart of the privacy posture: nothing reconstructable is retained.
package aggregate

import (
	"github.com/chelmertz/dotfiles/keylogger/internal/keys"
	"github.com/chelmertz/dotfiles/keylogger/internal/model"
)

// DefaultIdleMs is the gap after which the bigram/skipgram chain is broken, so
// a pause (lunch, thinking, reading) can't invent a pair across it.
const DefaultIdleMs = 1000

// editBufMax bounds the reconstructed-text buffer used by mistype tracking.
const editBufMax = 128

type uniKey struct {
	ctx     model.Context
	keycode int
	modmask int
}

type pairKey struct {
	ctx      model.Context
	kc1, kc2 int
}

type corrKey struct {
	ctx          model.Context
	wrong, right int
}

type bigramVal struct {
	count      int64
	intervalMs int64
}

type keyState struct {
	keycode int
	tsMs    int64
}

// Aggregator is not safe for concurrent use; a single goroutine owns it and
// serializes SetContext/Add calls (the daemon funnels everything through one
// channel).
type Aggregator struct {
	idleMs int64
	focus  model.Context // app/workspace/filetype from feeders; Device unused here

	prev     *keyState // last key in the current unbroken chain
	prevPrev *keyState // the one before prev, for skip-1

	// mistype tracking: a reconstructed-text buffer and a stack of chars
	// deleted by Backspace but not yet replaced.
	buf     []int
	pending []int

	unigrams    map[uniKey]int64
	bigrams     map[pairKey]*bigramVal
	skipgrams   map[pairKey]int64
	corrections map[corrKey]int64
}

func New() *Aggregator {
	return &Aggregator{
		idleMs:      DefaultIdleMs,
		unigrams:    map[uniKey]int64{},
		bigrams:     map[pairKey]*bigramVal{},
		skipgrams:   map[pairKey]int64{},
		corrections: map[corrKey]int64{},
	}
}

// SetContext updates the focus "where am I" (app/workspace/filetype). Any change
// breaks the chain, so no bigram/skipgram ever spans a focus switch. Device is
// not part of focus — it rides on each KeyEvent instead.
func (a *Aggregator) SetContext(ctx model.Context) {
	if ctx == a.focus {
		return
	}
	a.focus = ctx
	a.breakChain()
}

func (a *Aggregator) breakChain() {
	a.prev = nil
	a.prevPrev = nil
	// a focus switch means a different text buffer; drop edit state too.
	a.buf = a.buf[:0]
	a.pending = a.pending[:0]
}

// Add records one keydown (already cleaned: no releases/repeats). The full
// context is the current focus with the event's device layered on.
func (a *Aggregator) Add(ev model.KeyEvent) {
	ctx := a.focus
	ctx.Device = ev.Device
	a.unigrams[uniKey{ctx, ev.Keycode, ev.Modmask}]++
	a.trackEdit(ev, ctx)

	if a.prev != nil && ev.TsMs-a.prev.tsMs <= a.idleMs {
		gap := ev.TsMs - a.prev.tsMs
		bk := pairKey{ctx, a.prev.keycode, ev.Keycode}
		v := a.bigrams[bk]
		if v == nil {
			v = &bigramVal{}
			a.bigrams[bk] = v
		}
		v.count++
		v.intervalMs += gap

		if a.prevPrev != nil {
			a.skipgrams[pairKey{ctx, a.prevPrev.keycode, ev.Keycode}]++
		}
		a.prevPrev = a.prev
		a.prev = &keyState{ev.Keycode, ev.TsMs}
		return
	}

	// discontinuity (first key, idle gap, or just-broken chain): start fresh
	a.prevPrev = nil
	a.prev = &keyState{ev.Keycode, ev.TsMs}
}

// trackEdit maintains a reconstructed-text buffer to detect mistypes: a char
// deleted by Backspace and then replaced by a *different* char was a mistype.
// Deleting and retyping the same char (collateral in a multi-backspace run) is
// not counted. Command chords (Ctrl/Super) and nav keys reset the buffer, since
// their effect on the text can't be reconstructed.
func (a *Aggregator) trackEdit(ev model.KeyEvent, ctx model.Context) {
	kc := ev.Keycode
	switch {
	case keys.IsModifier(kc):
		// a modifier press alone doesn't change the buffer
	case ev.Modmask&(model.ModCtrl|model.ModSuper) != 0:
		a.resetEdit() // command chord (Ctrl+w, etc.): unknown edit
	case kc == keys.Backspace:
		if n := len(a.buf); n > 0 {
			a.pending = append(a.pending, a.buf[n-1])
			a.buf = a.buf[:n-1]
		}
	case keys.IsText(kc):
		if n := len(a.pending); n > 0 {
			d := a.pending[n-1]
			a.pending = a.pending[:n-1]
			if d != kc { // replaced with a different key → the deleted one was wrong
				a.corrections[corrKey{ctx, d, kc}]++
			}
		}
		a.buf = append(a.buf, kc)
		if len(a.buf) > editBufMax {
			a.buf = a.buf[len(a.buf)-editBufMax:]
		}
	default:
		a.resetEdit() // Enter/Tab/Esc/arrows: cursor moved, buffer no longer valid
	}
}

func (a *Aggregator) resetEdit() {
	a.buf = a.buf[:0]
	a.pending = a.pending[:0]
}

// Snapshot materializes the current counts as flushable model rows. It does not
// clear state — the daemon flushes cumulatively and relies on upsert.
func (a *Aggregator) Snapshot() model.Counts {
	c := model.Counts{}
	for k, n := range a.unigrams {
		c.Unigrams = append(c.Unigrams, model.Unigram{
			Context: k.ctx, Keycode: k.keycode, Modmask: k.modmask, Count: n,
		})
	}
	for k, v := range a.bigrams {
		c.Bigrams = append(c.Bigrams, model.Bigram{
			Context: k.ctx, KC1: k.kc1, KC2: k.kc2, Count: v.count, IntervalSumMs: v.intervalMs,
		})
	}
	for k, n := range a.skipgrams {
		c.Skipgrams = append(c.Skipgrams, model.Skipgram{
			Context: k.ctx, KC1: k.kc1, KC2: k.kc2, Count: n,
		})
	}
	for k, n := range a.corrections {
		c.Corrections = append(c.Corrections, model.Correction{
			Context: k.ctx, WrongKC: k.wrong, RightKC: k.right, Count: n,
		})
	}
	return c
}
