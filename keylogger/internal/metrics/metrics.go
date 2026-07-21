// Package metrics turns raw counts into the derived numbers the report and the
// rules consume. Everything here is a pure function of model.Counts — no I/O, no
// clock, no randomness — so it is exhaustively table-testable.
package metrics

import (
	"sort"

	"github.com/chelmertz/dotfiles/keylogger/internal/keys"
	"github.com/chelmertz/dotfiles/keylogger/internal/model"
)

// basePunct: punctuation reachable without a modifier on Swedish QWERTY.
var basePunct = map[int]bool{12: true, 13: true, 43: true, 51: true, 52: true, 53: true}

type KeyStat struct {
	Keycode int
	Char    string
	Count   int64
	Share   float64 // of total keydowns
	Hand    string
	Finger  string
}

type PairStat struct {
	KC1, KC2     int
	Char1, Char2 string
	Count        int64
	Share        float64 // of total bigrams/skipgrams
	MeanMs       int64   // bigrams only
	Finger       string  // "L pinky" for same-finger pairs
}

type FingerStat struct {
	Hand   string
	Finger string
	Count  int64
	Share  float64
}

type ContextStat struct {
	Label       string
	Count       int64
	Share       float64
	SymbolShare float64 // coding-symbol keystrokes / keystrokes in this context
	TopKeys     []KeyStat
}

type BindingStat struct {
	Keycode int
	Modmask int
	Combo   string // "Mod+Enter"
	Count   int64
	InConfig bool
	Fired    bool
}

type DeviceStat struct {
	Device string
	Count  int64
	Share  float64
}

type Metrics struct {
	TotalKeydowns int64
	TotalBigrams  int64

	Keys     []KeyStat // desc by count
	DeadKeys []KeyStat // asc by count, typeable keys under DeadThreshold

	Fingers      []FingerStat // desc by share
	HandBalanceL float64      // share of non-thumb load on left, 0..1
	HandBalanceR float64

	SFBPercent float64
	SFBTop     []PairStat
	SFSPercent float64
	SFSTop     []PairStat
	SlowBigrams []PairStat

	CorrectionRate float64 // backspace / total
	ModifierLoad   float64
	NumberRowShare float64
	SymbolShare    float64 // global

	Contexts []ContextStat
	Bindings []BindingStat
	Devices  []DeviceStat
}

// DeadThreshold: keys below this share of keydowns are "near-dead".
const DeadThreshold = 0.005

func isCodingSymbol(u model.Unigram) bool {
	return u.Modmask&model.ModAlt != 0 || basePunct[u.Keycode] // AltGr layer on sv covers []{}()@$…
}

func fingerLabel(kc int) string {
	if f, ok := keys.FingerOf(kc); ok {
		return f.Hand + " " + f.Finger
	}
	return ""
}

// Compute derives all metrics from the session counts. cfg carries optional
// context (parsed i3 bindings) that pure counts can't supply.
func Compute(c model.Counts, cfg Config) Metrics {
	var m Metrics

	// --- unigram-derived ---
	byKey := map[int]int64{}                 // keycode -> count (over ctx+mod)
	byFinger := map[keys.Finger]int64{}      // finger -> count
	perCtx := map[model.Context]*ctxAccum{}  // grouping for the context card
	var backspace, modifiers, numberRow, symbols int64

	for _, u := range c.Unigrams {
		m.TotalKeydowns += u.Count
		byKey[u.Keycode] += u.Count
		if f, ok := keys.FingerOf(u.Keycode); ok {
			byFinger[f] += u.Count
		}
		if u.Keycode == keys.Backspace {
			backspace += u.Count
		}
		if keys.IsModifier(u.Keycode) {
			modifiers += u.Count
		}
		if u.Keycode >= 2 && u.Keycode <= 11 {
			numberRow += u.Count
		}
		if isCodingSymbol(u) {
			symbols += u.Count
		}
		grp := groupCtx(u.Context)
		ca := perCtx[grp]
		if ca == nil {
			ca = &ctxAccum{byKey: map[int]int64{}}
			perCtx[grp] = ca
		}
		ca.total += u.Count
		ca.byKey[u.Keycode] += u.Count
		if isCodingSymbol(u) {
			ca.symbols += u.Count
		}
	}

	total := float64(nonZero(m.TotalKeydowns))
	for kc, n := range byKey {
		ks := KeyStat{Keycode: kc, Char: keys.Char(kc), Count: n, Share: float64(n) / total}
		if f, ok := keys.FingerOf(kc); ok {
			ks.Hand, ks.Finger = f.Hand, f.Finger
		}
		m.Keys = append(m.Keys, ks)
	}
	sort.Slice(m.Keys, func(i, j int) bool { return m.Keys[i].Count > m.Keys[j].Count })

	for _, ks := range m.Keys {
		// dead = typeable (mapped, not a modifier), rarely used
		if ks.Share < DeadThreshold && ks.Finger != "" && !keys.IsModifier(ks.Keycode) {
			m.DeadKeys = append(m.DeadKeys, ks)
		}
	}
	sort.Slice(m.DeadKeys, func(i, j int) bool { return m.DeadKeys[i].Count < m.DeadKeys[j].Count })

	var leftNonThumb, rightNonThumb int64
	for f, n := range byFinger {
		m.Fingers = append(m.Fingers, FingerStat{Hand: f.Hand, Finger: f.Finger, Count: n, Share: float64(n) / total})
		if f.Finger != "thumb" {
			if f.Hand == "L" {
				leftNonThumb += n
			} else {
				rightNonThumb += n
			}
		}
	}
	sort.Slice(m.Fingers, func(i, j int) bool { return m.Fingers[i].Share > m.Fingers[j].Share })
	if hb := float64(nonZero(leftNonThumb + rightNonThumb)); hb > 0 {
		m.HandBalanceL = float64(leftNonThumb) / hb
		m.HandBalanceR = float64(rightNonThumb) / hb
	}

	m.CorrectionRate = float64(backspace) / total
	m.ModifierLoad = float64(modifiers) / total
	m.NumberRowShare = float64(numberRow) / total
	m.SymbolShare = float64(symbols) / total

	// --- context card ---
	for ctx, ca := range perCtx {
		cs := ContextStat{
			Label:       contextLabel(ctx),
			Count:       ca.total,
			Share:       float64(ca.total) / total,
			SymbolShare: float64(ca.symbols) / float64(nonZero(ca.total)),
		}
		for kc, n := range ca.byKey {
			cs.TopKeys = append(cs.TopKeys, KeyStat{Keycode: kc, Char: keys.Char(kc), Count: n, Share: float64(n) / float64(nonZero(ca.total))})
		}
		sort.Slice(cs.TopKeys, func(i, j int) bool { return cs.TopKeys[i].Count > cs.TopKeys[j].Count })
		if len(cs.TopKeys) > 6 {
			cs.TopKeys = cs.TopKeys[:6]
		}
		m.Contexts = append(m.Contexts, cs)
	}
	sort.Slice(m.Contexts, func(i, j int) bool { return m.Contexts[i].Count > m.Contexts[j].Count })

	// --- i3 bindings (unigrams with a Super bit) ---
	m.Bindings = computeBindings(c.Unigrams, cfg.I3Bindings)

	// --- devices ---
	byDev := map[string]int64{}
	for _, u := range c.Unigrams {
		byDev[u.Device] += u.Count
	}
	for d, n := range byDev {
		m.Devices = append(m.Devices, DeviceStat{Device: d, Count: n, Share: float64(n) / total})
	}
	sort.Slice(m.Devices, func(i, j int) bool { return m.Devices[i].Count > m.Devices[j].Count })

	// --- bigrams: SFB + slow ---
	m.SFBPercent, m.SFBTop, m.TotalBigrams = sameFingerPairs(bigramPairs(c.Bigrams))
	m.SlowBigrams = slowBigrams(c.Bigrams)

	// --- skipgrams: SFS ---
	sfsPct, sfsTop, _ := sameFingerPairs(skipgramPairs(c.Skipgrams))
	m.SFSPercent, m.SFSTop = sfsPct, sfsTop

	return m
}

type ctxAccum struct {
	total   int64
	symbols int64
	byKey   map[int]int64
}

// Config carries non-count inputs the metrics need.
type Config struct {
	I3Bindings []Binding // parsed from i3 config; may be nil
}

// Binding is one bindsym from the i3 config.
type Binding struct {
	Keycode int
	Modmask int
	Combo   string
	Action  string
}

func nonZero(n int64) int64 {
	if n == 0 {
		return 1
	}
	return n
}
