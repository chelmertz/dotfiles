// Package seed fabricates a realistic session so the report/rules can be
// exercised end-to-end without evdev capture (which needs the input group).
// The numbers are invented but shaped to trigger a spread of findings.
package seed

import (
	"github.com/chelmertz/dotfiles/keylogger/internal/metrics"
	"github.com/chelmertz/dotfiles/keylogger/internal/model"
)

// perContext key→count profiles for a coding-heavy Swedish dev.
var profiles = []struct {
	ctx  model.Context
	dev  string
	keys map[int]int64
	// altSymbols: keycodes pressed with AltGr (coding brackets) count
	altSymbols map[int]int64
}{
	{
		ctx: model.Context{App: "nvim", Filetype: "go"}, dev: "keychron",
		keys: map[int]int64{
			57: 2600, 18: 2100, 30: 1500, 20: 1400, 49: 1300, 19: 1250, 31: 1200,
			23: 1150, 24: 1050, 38: 980, 32: 900, 14: 1500, 28: 900, 1: 800,
			36: 700, 37: 640, 39: 120, 40: 240, 26: 330, 53: 210, 25: 480, 44: 60, 17: 45,
		},
		altSymbols: map[int]int64{9: 420, 10: 400, 8: 360, 11: 340, 7: 220}, // AltGr layer: ( ) [ ] { }
	},
	{
		ctx: model.Context{App: "alacritty", Filetype: "zsh"}, dev: "keychron",
		keys: map[int]int64{
			57: 1400, 28: 900, 53: 600, 30: 400, 38: 380, 34: 360, 31: 320, 18: 300, 14: 500,
		},
		altSymbols: map[int]int64{},
	},
	{
		ctx: model.Context{App: "firefox"}, dev: "laptop",
		keys: map[int]int64{
			57: 1700, 14: 800, 28: 450, 15: 380, 53: 260, 33: 210, 30: 500, 18: 480,
		},
		altSymbols: map[int]int64{},
	},
	{
		ctx: model.Context{App: "nvim", Filetype: "md"}, dev: "laptop",
		keys: map[int]int64{
			57: 1000, 18: 700, 30: 400, 14: 400, 28: 300, 53: 200, 20: 260, 49: 240,
		},
		altSymbols: map[int]int64{},
	},
}

// Counts returns the fabricated session counts.
func Counts() model.Counts {
	var c model.Counts
	for _, p := range profiles {
		for kc, n := range p.keys {
			ctx := p.ctx
			ctx.Device = p.dev
			c.Unigrams = append(c.Unigrams, model.Unigram{Context: ctx, Keycode: kc, Count: n})
		}
		for kc, n := range p.altSymbols {
			ctx := p.ctx
			ctx.Device = p.dev
			c.Unigrams = append(c.Unigrams, model.Unigram{Context: ctx, Keycode: kc, Modmask: model.ModAlt, Count: n})
		}
	}
	// a couple i3 bindings that fired
	c.Unigrams = append(c.Unigrams,
		model.Unigram{Context: model.Context{Device: "keychron"}, Keycode: 28, Modmask: model.ModSuper, Count: 320}, // Mod+Enter
		model.Unigram{Context: model.Context{Device: "keychron"}, Keycode: 36, Modmask: model.ModSuper, Count: 210}, // Mod+j
		model.Unigram{Context: model.Context{Device: "keychron"}, Keycode: 37, Modmask: model.ModSuper, Count: 190}, // Mod+k
	)

	// bigrams: mix of same-finger (friction) and normal, with timing
	bg := model.Context{App: "nvim", Filetype: "go", Device: "keychron"}
	c.Bigrams = []model.Bigram{
		{Context: bg, KC1: 18, KC2: 32, Count: 420, IntervalSumMs: 420 * 230}, // e->d SFB, slow
		{Context: bg, KC1: 24, KC2: 38, Count: 300, IntervalSumMs: 300 * 210}, // o->l SFB
		{Context: bg, KC1: 22, KC2: 49, Count: 260, IntervalSumMs: 260 * 150}, // u->n SFB
		{Context: bg, KC1: 20, KC2: 35, Count: 900, IntervalSumMs: 900 * 95},  // t->h (different fingers)
		{Context: bg, KC1: 30, KC2: 31, Count: 1200, IntervalSumMs: 1200 * 88},
		{Context: bg, KC1: 18, KC2: 19, Count: 800, IntervalSumMs: 800 * 92},
	}
	c.Skipgrams = []model.Skipgram{
		{Context: bg, KC1: 18, KC2: 32, Count: 180}, // SFS
		{Context: bg, KC1: 30, KC2: 31, Count: 220},
	}
	return c
}

// I3Bindings is a sample parsed config: two fired, several never-fired.
func I3Bindings() []metrics.Binding {
	return []metrics.Binding{
		{Keycode: 28, Modmask: model.ModSuper, Combo: "Mod+Enter"},
		{Keycode: 36, Modmask: model.ModSuper, Combo: "Mod+j"},
		{Keycode: 37, Modmask: model.ModSuper, Combo: "Mod+k"},
		{Keycode: 9, Modmask: model.ModSuper, Combo: "Mod+8"},   // never fired
		{Keycode: 10, Modmask: model.ModSuper, Combo: "Mod+9"},  // never fired
		{Keycode: 11, Modmask: model.ModSuper, Combo: "Mod+0"},  // never fired
		{Keycode: 19, Modmask: model.ModSuper, Combo: "Mod+r"},  // never fired (resize)
	}
}
