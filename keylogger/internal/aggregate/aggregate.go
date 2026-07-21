// Package aggregate turns a stream of keydowns into per-context counts, holding
// only the previous key(s) in memory — never the ordered sequence. It is the
// heart of the privacy posture: nothing reconstructable is retained.
package aggregate

import (
	"github.com/chelmertz/dotfiles/keylogger/internal/model"
)

// DefaultIdleMs is the gap after which the bigram/skipgram chain is broken, so
// a pause (lunch, thinking, reading) can't invent a pair across it.
const DefaultIdleMs = 1000

type uniKey struct {
	ctx     model.Context
	keycode int
	modmask int
}

type pairKey struct {
	ctx      model.Context
	kc1, kc2 int
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

	unigrams  map[uniKey]int64
	bigrams   map[pairKey]*bigramVal
	skipgrams map[pairKey]int64
}

func New() *Aggregator {
	return &Aggregator{
		idleMs:    DefaultIdleMs,
		unigrams:  map[uniKey]int64{},
		bigrams:   map[pairKey]*bigramVal{},
		skipgrams: map[pairKey]int64{},
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
}

// Add records one keydown (already cleaned: no releases/repeats). The full
// context is the current focus with the event's device layered on.
func (a *Aggregator) Add(ev model.KeyEvent) {
	ctx := a.focus
	ctx.Device = ev.Device
	a.unigrams[uniKey{ctx, ev.Keycode, ev.Modmask}]++

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
	return c
}
