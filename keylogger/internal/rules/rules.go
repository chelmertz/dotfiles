// Package rules turns metrics into ranked, human-readable findings. Every rule
// is a pure threshold check plus a templated sentence — no AI, no randomness, so
// the same metrics always yield the same verdicts, and each is table-testable.
package rules

import (
	"fmt"
	"sort"
	"strings"

	"github.com/chelmertz/dotfiles/keylogger/internal/keys"
	"github.com/chelmertz/dotfiles/keylogger/internal/metrics"
)

type Severity int

const (
	Critical Severity = iota // "you're doing this wrong"
	Optimise                 // "you could optimise this"
	Info                     // neutral observation
	NoAction                 // data-backed "leave it alone"
)

func (s Severity) String() string {
	switch s {
	case Critical:
		return "critical"
	case Optimise:
		return "optimise"
	case NoAction:
		return "no-action"
	default:
		return "info"
	}
}

type Finding struct {
	Severity Severity
	Category string
	Title    string
	Body     string
}

// Thresholds are the tunable knobs. Defaults are defensible starting points from
// the ergonomics/layout community; calibrate against real sessions.
type Thresholds struct {
	PinkyCeiling      float64 // finger load above which a pinky is "overloaded"
	WeakKeyShare      float64 // key share that's "high" when on a weak finger
	SFBTarget         float64 // same-finger-bigram rate target
	SFSTarget         float64
	CorrectionHigh    float64 // backspace/keystroke considered high
	CodingSymbolRatio float64 // code-context symbol share vs global to flag a coding layer
	HandImbalance     float64 // |L-R| beyond this is lopsided
	NumberRowLow      float64
	ModifierHigh      float64
	SlowBigramMs      int64
}

func DefaultThresholds() Thresholds {
	return Thresholds{
		PinkyCeiling:      0.14,
		WeakKeyShare:      0.02,
		SFBTarget:         0.01,
		SFSTarget:         0.01,
		CorrectionHigh:    0.08,
		CodingSymbolRatio: 1.5,
		HandImbalance:     0.15,
		NumberRowLow:      0.04,
		ModifierHigh:      0.15,
		SlowBigramMs:      280,
	}
}

// languageLetters: keycodes that are real Swedish letters — never demote to a
// layer even when rare.
var languageLetters = map[int]bool{26: true, 40: true, 39: true} // å ä ö

// Evaluate runs every rule and returns findings ranked most-severe first.
func Evaluate(m metrics.Metrics, t Thresholds) []Finding {
	rules := []func(metrics.Metrics, Thresholds) *Finding{
		rulePinkyOverload,
		ruleWeakKeyHighFreq,
		ruleSFB,
		ruleSFS,
		ruleCorrection,
		ruleCodingLayer,
		ruleSlowBigrams,
		ruleDeadKeys,
		ruleLanguageGuard,
		ruleI3DeadBindings,
		ruleModifierLoad,
		ruleHandBalance,
		ruleNumberRow,
	}
	var out []Finding
	for _, r := range rules {
		if f := r(m, t); f != nil {
			out = append(out, *f)
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Severity < out[j].Severity })
	return out
}

func pct(f float64) string { return fmt.Sprintf("%.1f%%", f*100) }

// topKeysOnFinger returns up to n display chars of the heaviest keys on a finger.
func topKeysOnFinger(m metrics.Metrics, hand, finger string, n int) []string {
	var out []string
	for _, k := range m.Keys { // already desc by count
		if k.Hand == hand && k.Finger == finger {
			out = append(out, k.Char)
			if len(out) == n {
				break
			}
		}
	}
	return out
}

func rulePinkyOverload(m metrics.Metrics, t Thresholds) *Finding {
	var worst *metrics.FingerStat
	for i := range m.Fingers {
		if m.Fingers[i].Finger != "pinky" {
			continue
		}
		if worst == nil || m.Fingers[i].Share > worst.Share {
			worst = &m.Fingers[i]
		}
	}
	if worst == nil || worst.Share <= t.PinkyCeiling {
		return nil
	}
	keysOn := topKeysOnFinger(m, worst.Hand, "pinky", 4)
	return &Finding{
		Severity: Critical,
		Category: "finger-load",
		Title:    "You're overloading a weak finger",
		Body: fmt.Sprintf(
			"%s pinky carries %s of keystrokes (comfort ≈ %s) — it's absorbing %s. "+
				"On the Glove80, move the movable ones (Backspace, Enter) to thumbs to shed several points.",
			handName(worst.Hand), pct(worst.Share), pct(t.PinkyCeiling), strings.Join(keysOn, " ")),
	}
}

func ruleWeakKeyHighFreq(m metrics.Metrics, t Thresholds) *Finding {
	var flagged []string
	for _, k := range m.Keys {
		if k.Finger == "pinky" && k.Share >= t.WeakKeyShare && !keys.IsModifier(k.Keycode) && !languageLetters[k.Keycode] {
			flagged = append(flagged, fmt.Sprintf("%s (%s)", k.Char, pct(k.Share)))
		}
	}
	if len(flagged) == 0 {
		return nil
	}
	return &Finding{
		Severity: Optimise,
		Category: "key-placement",
		Title:    "High-frequency keys on a weak finger",
		Body: fmt.Sprintf(
			"These frequent keys sit on a pinky: %s. Prime candidates to move to thumb keys or a stronger finger on the Glove80.",
			strings.Join(flagged, ", ")),
	}
}

func ruleSFB(m metrics.Metrics, t Thresholds) *Finding {
	if m.SFBPercent <= t.SFBTarget {
		return nil
	}
	return &Finding{
		Severity: Optimise,
		Category: "same-finger-bigram",
		Title:    "Same-finger bigrams above target",
		Body: fmt.Sprintf(
			"%s of your bigrams are same-finger (target <%s). Worst offenders: %s. "+
				"Much of this is inherent to QWERTY, not you — this is the data that justifies trying an alternative layout.",
			pct(m.SFBPercent), pct(t.SFBTarget), pairList(m.SFBTop, 3)),
	}
}

func ruleSFS(m metrics.Metrics, t Thresholds) *Finding {
	if m.SFSPercent <= t.SFSTarget {
		return nil
	}
	return &Finding{
		Severity: Optimise,
		Category: "same-finger-skipgram",
		Title:    "Same-finger skipgrams above target",
		Body: fmt.Sprintf("%s of skip-1 pairs land on the same finger (target <%s): %s.",
			pct(m.SFSPercent), pct(t.SFSTarget), pairList(m.SFSTop, 3)),
	}
}

func ruleCorrection(m metrics.Metrics, t Thresholds) *Finding {
	if m.CorrectionRate <= t.CorrectionHigh {
		return nil
	}
	return &Finding{
		Severity: Optimise,
		Category: "corrections",
		Title:    "High correction rate",
		Body: fmt.Sprintf(
			"Backspace is %s of keystrokes (1 in %.0f). High correction load points at typos or awkward key positions — worth watching whether a new layout lowers it.",
			pct(m.CorrectionRate), 1/nonZeroF(m.CorrectionRate)),
	}
}

func ruleCodingLayer(m metrics.Metrics, t Thresholds) *Finding {
	var codeCtx []string
	for _, c := range m.Contexts {
		if !isCodeContext(c.Label) {
			continue
		}
		if m.SymbolShare > 0 && c.SymbolShare >= m.SymbolShare*t.CodingSymbolRatio {
			codeCtx = append(codeCtx, fmt.Sprintf("%s (%s symbols)", c.Label, pct(c.SymbolShare)))
		}
	}
	if len(codeCtx) == 0 {
		return nil
	}
	return &Finding{
		Severity: Optimise,
		Category: "coding-layer",
		Title:    "A coding layer would earn its keep",
		Body: fmt.Sprintf(
			"Symbol/AltGr keystrokes spike in code contexts vs your %s global average: %s. "+
				"A layer putting brackets and symbols on the home row would pay off there (a prose/Markdown symbol layer would not).",
			pct(m.SymbolShare), strings.Join(codeCtx, ", ")),
	}
}

func ruleSlowBigrams(m metrics.Metrics, t Thresholds) *Finding {
	var slow []string
	for _, p := range m.SlowBigrams {
		if p.MeanMs >= t.SlowBigramMs {
			slow = append(slow, fmt.Sprintf("%s%s (%dms)", p.Char1, p.Char2, p.MeanMs))
		}
	}
	if len(slow) == 0 {
		return nil
	}
	if len(slow) > 4 {
		slow = slow[:4]
	}
	return &Finding{
		Severity: Info,
		Category: "timing",
		Title:    "Fumbly (slow) key pairs",
		Body: fmt.Sprintf("Slowest frequent pairs by mean inter-key gap: %s. Candidates for easier positions.",
			strings.Join(slow, ", ")),
	}
}

func ruleDeadKeys(m metrics.Metrics, t Thresholds) *Finding {
	var dead []string
	for _, k := range m.DeadKeys {
		if languageLetters[k.Keycode] {
			continue // handled by the language guard
		}
		dead = append(dead, fmt.Sprintf("%s (%s)", k.Char, pct(k.Share)))
	}
	if len(dead) == 0 {
		return nil
	}
	return &Finding{
		Severity: Info,
		Category: "dead-keys",
		Title:    "Near-dead keys",
		Body: fmt.Sprintf("Barely used: %s. Candidates to demote to a layer or combo, freeing base positions.",
			strings.Join(dead, ", ")),
	}
}

func ruleLanguageGuard(m metrics.Metrics, t Thresholds) *Finding {
	var rare []string
	for _, k := range m.DeadKeys {
		if languageLetters[k.Keycode] {
			rare = append(rare, k.Char)
		}
	}
	if len(rare) == 0 {
		return nil
	}
	return &Finding{
		Severity: NoAction,
		Category: "language-letters",
		Title:    "Keep å/ä/ö on the base layer",
		Body: fmt.Sprintf("%s are rare this session but are real Swedish letters — don't demote them to a layer just for frequency; prose needs them at hand.",
			strings.Join(rare, " ")),
	}
}

func ruleI3DeadBindings(m metrics.Metrics, t Thresholds) *Finding {
	var dead []string
	haveConfig := false
	for _, b := range m.Bindings {
		if b.InConfig {
			haveConfig = true
			if !b.Fired {
				dead = append(dead, b.Combo)
			}
		}
	}
	if !haveConfig || len(dead) == 0 {
		return nil
	}
	return &Finding{
		Severity: Info,
		Category: "i3-bindings",
		Title:    "Unused i3 bindings",
		Body: fmt.Sprintf("%d configured bindings never fired this session: %s. Don't spend prime Glove80 keys on cold bindings.",
			len(dead), strings.Join(dead, ", ")),
	}
}

func ruleModifierLoad(m metrics.Metrics, t Thresholds) *Finding {
	if m.ModifierLoad <= t.ModifierHigh {
		return nil
	}
	return &Finding{
		Severity: Info,
		Category: "modifiers",
		Title:    "High modifier load",
		Body: fmt.Sprintf("Modifiers are %s of keystrokes — home-row-mods on the Glove80 could reduce the reach.", pct(m.ModifierLoad)),
	}
}

func ruleHandBalance(m metrics.Metrics, t Thresholds) *Finding {
	diff := m.HandBalanceL - m.HandBalanceR
	if diff < 0 {
		diff = -diff
	}
	if diff > t.HandImbalance {
		return &Finding{
			Severity: Info,
			Category: "hand-balance",
			Title:    "Lopsided hand load",
			Body:     fmt.Sprintf("Hand load is %s left / %s right — one hand is doing notably more work.", pct(m.HandBalanceL), pct(m.HandBalanceR)),
		}
	}
	return &Finding{
		Severity: NoAction,
		Category: "hand-balance",
		Title:    "Hand balance is fine",
		Body:     fmt.Sprintf("Load is %s / %s left/right — no lopsidedness to correct.", pct(m.HandBalanceL), pct(m.HandBalanceR)),
	}
}

func ruleNumberRow(m metrics.Metrics, t Thresholds) *Finding {
	if m.NumberRowShare >= t.NumberRowLow || m.NumberRowShare == 0 {
		return nil
	}
	return &Finding{
		Severity: NoAction,
		Category: "number-row",
		Title:    "Number row is low but bursty",
		Body:     fmt.Sprintf("Number row is only %s of keystrokes, but it's used in bursts — a number layer would likely cost more than it saves. Leave it.", pct(m.NumberRowShare)),
	}
}

// --- small helpers ---

func handName(h string) string {
	if h == "L" {
		return "Left"
	}
	return "Right"
}

func nonZeroF(f float64) float64 {
	if f == 0 {
		return 1
	}
	return f
}

func pairList(ps []metrics.PairStat, n int) string {
	var out []string
	for i, p := range ps {
		if i == n {
			break
		}
		out = append(out, p.Char1+p.Char2)
	}
	return strings.Join(out, ", ")
}

var codeExts = map[string]bool{
	"go": true, "rs": true, "js": true, "ts": true, "py": true, "c": true, "cpp": true,
	"h": true, "java": true, "rb": true, "lua": true, "sh": true, "zsh": true, "nix": true,
}

func isCodeContext(label string) bool {
	// label looks like "nvim · go"
	parts := strings.Split(label, "·")
	if len(parts) < 2 {
		return false
	}
	ft := strings.TrimSpace(parts[len(parts)-1])
	ft = strings.TrimPrefix(ft, ".")
	return codeExts[ft]
}
