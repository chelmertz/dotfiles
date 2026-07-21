package metrics

import (
	"sort"
	"strings"

	"github.com/chelmertz/dotfiles/keylogger/internal/keys"
	"github.com/chelmertz/dotfiles/keylogger/internal/model"
)

// MinSlowCount: ignore rare pairs when ranking "slowest" bigrams — a single
// fumbled pair isn't a pattern.
const MinSlowCount = 20

// TopN caps the offender/slow lists in the report.
const TopN = 8

// groupCtx collapses a full context to the (app, filetype) grouping the context
// card cares about — device/workspace are dropped from that view.
func groupCtx(c model.Context) model.Context {
	return model.Context{App: c.App, Filetype: c.Filetype}
}

func contextLabel(c model.Context) string {
	app := c.App
	if app == "" {
		app = "(unknown)"
	}
	if c.Filetype != "" {
		return app + " · " + c.Filetype
	}
	return app
}

type pairAgg struct {
	count    int64
	interval int64
}

func bigramPairs(bs []model.Bigram) (map[[2]int]pairAgg, int64) {
	out := map[[2]int]pairAgg{}
	var total int64
	for _, b := range bs {
		k := [2]int{b.KC1, b.KC2}
		a := out[k]
		a.count += b.Count
		a.interval += b.IntervalSumMs
		out[k] = a
		total += b.Count
	}
	return out, total
}

func skipgramPairs(sks []model.Skipgram) (map[[2]int]pairAgg, int64) {
	out := map[[2]int]pairAgg{}
	var total int64
	for _, s := range sks {
		k := [2]int{s.KC1, s.KC2}
		a := out[k]
		a.count += s.Count
		out[k] = a
		total += s.Count
	}
	return out, total
}

// sameFingerPairs returns the same-finger percentage of all pairs plus the top
// offenders, and echoes the total (so the bigram total lands on Metrics).
func sameFingerPairs(pairs map[[2]int]pairAgg, total int64) (float64, []PairStat, int64) {
	var sfCount int64
	var top []PairStat
	for k, a := range pairs {
		if k[0] == k[1] || !keys.SameFinger(k[0], k[1]) {
			continue
		}
		sfCount += a.count
		top = append(top, PairStat{
			KC1: k[0], KC2: k[1],
			Char1: keys.Char(k[0]), Char2: keys.Char(k[1]),
			Count: a.count, Share: float64(a.count) / float64(nonZero(total)),
			Finger: fingerLabel(k[0]),
		})
	}
	sort.Slice(top, func(i, j int) bool { return top[i].Count > top[j].Count })
	if len(top) > TopN {
		top = top[:TopN]
	}
	return float64(sfCount) / float64(nonZero(total)), top, total
}

func slowBigrams(bs []model.Bigram) []PairStat {
	pairs, _ := bigramPairs(bs)
	var out []PairStat
	for k, a := range pairs {
		if a.count < MinSlowCount {
			continue
		}
		out = append(out, PairStat{
			KC1: k[0], KC2: k[1],
			Char1: keys.Char(k[0]), Char2: keys.Char(k[1]),
			Count: a.count, MeanMs: a.interval / a.count,
			Finger: fingerLabel(k[0]),
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].MeanMs > out[j].MeanMs })
	if len(out) > TopN {
		out = out[:TopN]
	}
	return out
}

// computeMistypes aggregates corrections per wrong key: how often it was
// mistyped, its rate relative to how often it's typed, and the key it was most
// often corrected to.
func computeMistypes(cs []model.Correction, byKey map[int]int64) []MistypeStat {
	type agg struct {
		total int64
		subs  map[int]int64
	}
	byWrong := map[int]*agg{}
	for _, c := range cs {
		a := byWrong[c.WrongKC]
		if a == nil {
			a = &agg{subs: map[int]int64{}}
			byWrong[c.WrongKC] = a
		}
		a.total += c.Count
		a.subs[c.RightKC] += c.Count
	}
	var out []MistypeStat
	for kc, a := range byWrong {
		var topKC int
		var topN int64
		for rkc, n := range a.subs {
			if n > topN {
				topN, topKC = n, rkc
			}
		}
		st := MistypeStat{
			Keycode: kc, Char: keys.Char(kc), Count: a.total, Typed: byKey[kc],
			TopSub: keys.Char(topKC), TopSubCount: topN,
		}
		if st.Typed > 0 {
			st.Rate = float64(a.total) / float64(st.Typed)
		}
		out = append(out, st)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Count > out[j].Count })
	return out
}

func comboString(keycode, modmask int) string {
	var parts []string
	if modmask&model.ModSuper != 0 {
		parts = append(parts, "Mod")
	}
	if modmask&model.ModCtrl != 0 {
		parts = append(parts, "Ctrl")
	}
	if modmask&model.ModAlt != 0 {
		parts = append(parts, "Alt")
	}
	if modmask&model.ModShift != 0 {
		parts = append(parts, "Shift")
	}
	parts = append(parts, keys.Char(keycode))
	return strings.Join(parts, "+")
}

// computeBindings unions the fired Super-combos (from unigrams) with the parsed
// i3 config, so both hot bindings and never-fired ones surface.
func computeBindings(unis []model.Unigram, cfg []Binding) []BindingStat {
	type bkey struct{ kc, mod int }
	fired := map[bkey]int64{}
	for _, u := range unis {
		if u.Modmask&model.ModSuper == 0 {
			continue
		}
		fired[bkey{u.Keycode, u.Modmask}] += u.Count
	}

	seen := map[bkey]bool{}
	var out []BindingStat
	for _, b := range cfg {
		k := bkey{b.Keycode, b.Modmask}
		seen[k] = true
		n := fired[k]
		out = append(out, BindingStat{
			Keycode: b.Keycode, Modmask: b.Modmask, Combo: b.Combo,
			Count: n, InConfig: true, Fired: n > 0,
		})
	}
	for k, n := range fired {
		if seen[k] {
			continue
		}
		out = append(out, BindingStat{
			Keycode: k.kc, Modmask: k.mod, Combo: comboString(k.kc, k.mod),
			Count: n, InConfig: false, Fired: true,
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Count > out[j].Count })
	return out
}
