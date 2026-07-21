package aggregate

import (
	"testing"

	"github.com/chelmertz/dotfiles/keylogger/internal/model"
)

// helper: build a keydown at a given ms with no modifiers
func k(code int, ms int64) model.KeyEvent { return model.KeyEvent{Keycode: code, TsMs: ms} }

func uni(a *Aggregator, code, mod int, ctx model.Context) int64 {
	return a.unigrams[uniKey{ctx, code, mod}]
}

func bi(a *Aggregator, kc1, kc2 int, ctx model.Context) *bigramVal {
	return a.bigrams[pairKey{ctx, kc1, kc2}]
}

func TestUnigramCountsAndContext(t *testing.T) {
	a := New()
	a.SetContext(model.Context{App: "nvim"})
	a.Add(k(30, 0))  // a
	a.Add(k(30, 10)) // a
	a.SetContext(model.Context{App: "firefox"})
	a.Add(k(30, 20)) // a in a different context

	if got := uni(a, 30, 0, model.Context{App: "nvim"}); got != 2 {
		t.Errorf("nvim a count = %d, want 2", got)
	}
	if got := uni(a, 30, 0, model.Context{App: "firefox"}); got != 1 {
		t.Errorf("firefox a count = %d, want 1", got)
	}
}

func TestModmaskDistinguishesUnigrams(t *testing.T) {
	a := New()
	a.Add(model.KeyEvent{Keycode: 28, Modmask: model.ModSuper, TsMs: 0}) // Mod+Enter
	a.Add(model.KeyEvent{Keycode: 28, Modmask: 0, TsMs: 10})             // plain Enter
	if got := uni(a, 28, model.ModSuper, model.Context{}); got != 1 {
		t.Errorf("Mod+Enter = %d, want 1", got)
	}
	if got := uni(a, 28, 0, model.Context{}); got != 1 {
		t.Errorf("Enter = %d, want 1", got)
	}
}

func TestBigramFormsWithinIdleWindow(t *testing.T) {
	a := New()
	a.Add(k(30, 0))   // a
	a.Add(k(31, 100)) // s, 100ms later -> bigram a,s
	v := bi(a, 30, 31, model.Context{})
	if v == nil || v.count != 1 {
		t.Fatalf("bigram a,s = %+v, want count 1", v)
	}
	if v.intervalMs != 100 {
		t.Errorf("interval = %d, want 100", v.intervalMs)
	}
}

func TestIdleGapBreaksBigram(t *testing.T) {
	a := New()
	a.Add(k(30, 0))    // a
	a.Add(k(31, 5000)) // s, 5s later -> gap > idle, no bigram
	if v := bi(a, 30, 31, model.Context{}); v != nil {
		t.Errorf("expected no bigram across idle gap, got %+v", v)
	}
}

func TestFocusChangeBreaksBigram(t *testing.T) {
	a := New()
	a.SetContext(model.Context{App: "nvim"})
	a.Add(k(30, 0)) // a in nvim
	a.SetContext(model.Context{App: "firefox"})
	a.Add(k(31, 50)) // s in firefox, close in time but context switched
	if v := bi(a, 30, 31, model.Context{App: "firefox"}); v != nil {
		t.Errorf("expected no bigram across focus change, got %+v", v)
	}
	if v := bi(a, 30, 31, model.Context{App: "nvim"}); v != nil {
		t.Errorf("expected no bigram in old context either, got %+v", v)
	}
}

func TestSkipgramSkipsMiddleKey(t *testing.T) {
	a := New()
	a.Add(k(30, 0))   // a
	a.Add(k(31, 100)) // s
	a.Add(k(32, 200)) // d -> skipgram a,d (skipping s)
	if got := a.skipgrams[pairKey{model.Context{}, 30, 32}]; got != 1 {
		t.Errorf("skipgram a,d = %d, want 1", got)
	}
	// and the adjacent bigrams exist
	if bi(a, 30, 31, model.Context{}) == nil || bi(a, 31, 32, model.Context{}) == nil {
		t.Errorf("expected bigrams a,s and s,d to exist")
	}
}

func TestIdleGapClearsSkipgramChain(t *testing.T) {
	a := New()
	a.Add(k(30, 0))    // a
	a.Add(k(31, 100))  // s
	a.Add(k(32, 5000)) // d after idle -> no bigram s,d AND no skipgram a,d
	if got := a.skipgrams[pairKey{model.Context{}, 30, 32}]; got != 0 {
		t.Errorf("skipgram a,d = %d, want 0 across idle", got)
	}
}

func TestRepeatedKeyIsSameFingerBigram(t *testing.T) {
	a := New()
	a.Add(k(30, 0))
	a.Add(k(30, 50))
	if v := bi(a, 30, 30, model.Context{}); v == nil || v.count != 1 {
		t.Errorf("expected a,a bigram, got %+v", v)
	}
}

// keycode names, so test sequences read as text rather than magic numbers.
const (
	kA  = 30
	kB  = 48
	kC  = 46
	kW  = 17
	kX  = 45
	kY  = 21
	kBS = 14
)

func corr(a *Aggregator, wrong, right int) int64 {
	return a.corrections[corrKey{model.Context{}, wrong, right}]
}

func addSeq(a *Aggregator, codes ...int) {
	for i, kc := range codes {
		a.Add(k(kc, int64(i*100)))
	}
}

func TestMistypeReplaceCatchesTheRealError(t *testing.T) {
	a := New()
	// type "abc", backspace twice, retype "cb" — the b→c fix is the real mistype
	addSeq(a, kA, kB, kC, kBS, kBS, kC, kB)
	if got := corr(a, kB, kC); got != 1 { // b mistyped, replaced by c
		t.Errorf("b→c correction = %d, want 1", got)
	}
	if got := corr(a, kC, kB); got != 1 { // c mistyped, replaced by b (transposition)
		t.Errorf("c→b correction = %d, want 1", got)
	}
}

func TestMistypeSingleTypo(t *testing.T) {
	a := New()
	addSeq(a, kX, kBS, kY) // type x, delete, retype y
	if got := corr(a, kX, kY); got != 1 {
		t.Errorf("x→y correction = %d, want 1", got)
	}
}

func TestDeleteAndRetypeSameCharIsNotAMistype(t *testing.T) {
	a := New()
	addSeq(a, kA, kBS, kA) // a, delete, retype the same a (pure collateral)
	if len(a.corrections) != 0 {
		t.Errorf("expected no corrections for delete-and-retype-same, got %v", a.corrections)
	}
}

func TestCommandChordResetsEditBuffer(t *testing.T) {
	a := New()
	a.Add(k(kA, 0))                                                      // a
	a.Add(k(kBS, 100))                                                   // BS -> pending=[a]
	a.Add(model.KeyEvent{Keycode: kW, Modmask: model.ModCtrl, TsMs: 200}) // Ctrl+w: resets
	a.Add(k(kB, 300))                                                    // b: pending empty -> no correction
	if len(a.corrections) != 0 {
		t.Errorf("command chord should have reset edit buffer, got %v", a.corrections)
	}
}

func TestSnapshotRoundTrips(t *testing.T) {
	a := New()
	a.Add(k(30, 0))
	a.Add(k(31, 50))
	snap := a.Snapshot()
	if len(snap.Unigrams) != 2 {
		t.Errorf("unigrams = %d, want 2", len(snap.Unigrams))
	}
	if len(snap.Bigrams) != 1 {
		t.Errorf("bigrams = %d, want 1", len(snap.Bigrams))
	}
}
