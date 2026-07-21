package rules

import (
	"sort"
	"testing"

	"github.com/chelmertz/dotfiles/keylogger/internal/metrics"
	"github.com/chelmertz/dotfiles/keylogger/internal/model"
)

func find(fs []Finding, cat string) *Finding {
	for i := range fs {
		if fs[i].Category == cat {
			return &fs[i]
		}
	}
	return nil
}

func TestFiringAndRanking(t *testing.T) {
	c := model.Counts{
		Unigrams: []model.Unigram{
			{Keycode: 14, Count: 200}, // Backspace R pinky (overload + corrections)
			{Keycode: 26, Count: 50},  // å R pinky
			{Keycode: 40, Count: 50},  // ä R pinky
			{Keycode: 39, Count: 2},   // ö R pinky -> dead + language guard
			{Keycode: 30, Count: 300}, // a L pinky
			{Keycode: 18, Count: 100}, // e L middle
			{Keycode: 32, Count: 100}, // d L middle
			{Keycode: 57, Count: 200}, // space R thumb
		},
		Bigrams: []model.Bigram{
			{KC1: 18, KC2: 32, Count: 50, IntervalSumMs: 50 * 200}, // e->d SFB
		},
	}
	m := metrics.Compute(c, metrics.Config{})
	fs := Evaluate(m, DefaultThresholds())

	if len(fs) == 0 {
		t.Fatal("expected findings")
	}
	if fs[0].Severity != Critical {
		t.Errorf("first finding severity = %v, want Critical (pinky overload)", fs[0].Severity)
	}
	if find(fs, "finger-load") == nil {
		t.Error("expected pinky overload finding")
	}
	if find(fs, "same-finger-bigram") == nil {
		t.Error("expected SFB finding")
	}
	if find(fs, "corrections") == nil {
		t.Error("expected correction-rate finding")
	}
	lg := find(fs, "language-letters")
	if lg == nil || lg.Severity != NoAction {
		t.Errorf("expected å/ä/ö language guard as NoAction, got %+v", lg)
	}

	// ranking is non-decreasing severity
	if !sort.SliceIsSorted(fs, func(i, j int) bool { return fs[i].Severity < fs[j].Severity }) {
		t.Error("findings not ranked by severity")
	}
}

func TestQuietMetricsProduceNoAlarms(t *testing.T) {
	// balanced load spread across fingers -> no overload, no lopsidedness
	c := model.Counts{Unigrams: []model.Unigram{
		{Keycode: 33, Count: 100}, // f L index
		{Keycode: 36, Count: 100}, // j R index
		{Keycode: 32, Count: 100}, // d L middle
		{Keycode: 37, Count: 100}, // k R middle
		{Keycode: 31, Count: 100}, // s L ring
		{Keycode: 38, Count: 100}, // l R ring
		{Keycode: 30, Count: 30},  // a L pinky (low)
		{Keycode: 39, Count: 30},  // ö R pinky (low)
		{Keycode: 57, Count: 100}, // space thumb
	}}
	m := metrics.Compute(c, metrics.Config{})
	fs := Evaluate(m, DefaultThresholds())
	if f := find(fs, "finger-load"); f != nil {
		t.Errorf("did not expect pinky overload, got %+v", f)
	}
	// hand balance should be the reassuring NoAction one
	hb := find(fs, "hand-balance")
	if hb == nil || hb.Severity != NoAction {
		t.Errorf("expected balanced-hands NoAction, got %+v", hb)
	}
}
