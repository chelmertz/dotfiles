package metrics

import (
	"math"
	"testing"

	"github.com/chelmertz/dotfiles/keylogger/internal/model"
)

func approx(a, b float64) bool { return math.Abs(a-b) < 0.001 }

func TestCoreDerivations(t *testing.T) {
	c := model.Counts{
		Unigrams: []model.Unigram{
			{Keycode: 30, Count: 500}, // a, L pinky
			{Keycode: 14, Count: 100}, // Backspace, R pinky
			{Keycode: 39, Count: 3},   // ö, R pinky -> near-dead (3/603 = 0.49%)
		},
		Bigrams: []model.Bigram{
			{KC1: 18, KC2: 32, Count: 40, IntervalSumMs: 40 * 250}, // e->d, both L middle = SFB
			{KC1: 30, KC2: 31, Count: 60, IntervalSumMs: 60 * 120}, // a->s, different fingers
		},
	}
	m := Compute(c, Config{})

	if m.TotalKeydowns != 603 {
		t.Errorf("total = %d, want 603", m.TotalKeydowns)
	}
	if !approx(m.CorrectionRate, 100.0/603.0) {
		t.Errorf("correction = %f, want %f", m.CorrectionRate, 100.0/603.0)
	}
	if !approx(m.SFBPercent, 0.4) {
		t.Errorf("SFB = %f, want 0.4", m.SFBPercent)
	}
	if len(m.SFBTop) != 1 || m.SFBTop[0].KC1 != 18 || m.SFBTop[0].KC2 != 32 {
		t.Errorf("SFBTop = %+v, want single e->d pair", m.SFBTop)
	}
	if !approx(m.HandBalanceL, 500.0/603.0) {
		t.Errorf("hand balance L = %f, want %f", m.HandBalanceL, 500.0/603.0)
	}

	// ö is near-dead and typeable -> flagged
	foundDeadO := false
	for _, d := range m.DeadKeys {
		if d.Keycode == 39 {
			foundDeadO = true
		}
	}
	if !foundDeadO {
		t.Errorf("expected ö (39) in dead keys, got %+v", m.DeadKeys)
	}

	// Backspace must NOT be dead (100/603 is high) and lives on R pinky
	for _, d := range m.DeadKeys {
		if d.Keycode == 14 {
			t.Errorf("Backspace should not be dead")
		}
	}
}

func TestFingerLoadAggregates(t *testing.T) {
	c := model.Counts{Unigrams: []model.Unigram{
		{Keycode: 14, Count: 200}, // Backspace R pinky
		{Keycode: 39, Count: 100}, // ö R pinky
		{Keycode: 30, Count: 100}, // a L pinky
	}}
	m := Compute(c, Config{})
	var rPinky float64
	for _, f := range m.Fingers {
		if f.Hand == "R" && f.Finger == "pinky" {
			rPinky = f.Share
		}
	}
	// R pinky = (200+100)/400 = 0.75
	if !approx(rPinky, 0.75) {
		t.Errorf("R pinky share = %f, want 0.75", rPinky)
	}
}

func TestBindingsFiredAndDead(t *testing.T) {
	c := model.Counts{Unigrams: []model.Unigram{
		{Keycode: 28, Modmask: model.ModSuper, Count: 50}, // Mod+Enter fired
	}}
	cfg := Config{I3Bindings: []Binding{
		{Keycode: 28, Modmask: model.ModSuper, Combo: "Mod+Enter"},
		{Keycode: 9, Modmask: model.ModSuper, Combo: "Mod+8"}, // never fired
	}}
	m := Compute(c, cfg)
	var enter, ws8 *BindingStat
	for i := range m.Bindings {
		switch m.Bindings[i].Keycode {
		case 28:
			enter = &m.Bindings[i]
		case 9:
			ws8 = &m.Bindings[i]
		}
	}
	if enter == nil || enter.Count != 50 || !enter.Fired {
		t.Errorf("Mod+Enter = %+v, want fired count 50", enter)
	}
	if ws8 == nil || ws8.Fired {
		t.Errorf("Mod+8 = %+v, want present but not fired", ws8)
	}
}
