package report

import (
	"fmt"
	"io"

	"github.com/chelmertz/dotfiles/keylogger/internal/metrics"
	"github.com/chelmertz/dotfiles/keylogger/internal/model"
	"github.com/chelmertz/dotfiles/keylogger/internal/rules"
)

type barView struct {
	Label    string
	WidthPct string
	Val      string
	Dead     bool
}

type fingerView struct {
	Finger    string
	HeightPct string
	Val       string
	Right     bool
}

type pairView struct {
	Pair   string
	Finger string
	Count  int64
	Pct    string
}

type ctxView struct {
	Label string
	Pct   string
	Keys  []barView
}

type findView struct {
	Tag       string
	RailClass string
	Title     string
	Body      string
}

type bindView struct {
	Combo string
	Count int64
	Dead  bool
}

type tileView struct {
	K, V, N, Class string
}

type mistypeView struct {
	Char     string
	Count    int64
	Rate     string
	TopSub   string
	WidthPct string
}

type htmlView struct {
	SessionID    int64
	Heading      string
	Host         string
	Dur          string
	Note         string
	Total        int64
	DeviceSplit  []barView
	Tiles        []tileView
	Findings     []findView
	Keys         []barView
	Fingers      []fingerView
	SFBPct       string
	SFB          []pairView
	Contexts     []ctxView
	Bindings     []bindView
	DeadBindings int
	Mistypes     []mistypeView
}

// HTML renders a self-contained report page.
func HTML(w io.Writer, s model.Session, m metrics.Metrics, fs []rules.Finding) error {
	return htmlTmpl.Execute(w, buildView(s, m, fs))
}

func widthPct(share, max float64) string {
	if max <= 0 {
		return "0"
	}
	return fmt.Sprintf("%.1f", share/max*100)
}

func buildView(s model.Session, m metrics.Metrics, fs []rules.Finding) htmlView {
	v := htmlView{SessionID: s.ID, Host: s.Host, Total: m.TotalKeydowns}
	if s.ID == 0 { // --all lifetime view
		v.Heading = "All sessions"
		v.Note = s.Note // e.g. "3 sessions"
	} else {
		v.Heading = fmt.Sprintf("Session #%d", s.ID)
		v.Dur = dur(s)
	}

	for _, d := range m.Devices {
		v.DeviceSplit = append(v.DeviceSplit, barView{Label: nameOr(d.Device, "?"), WidthPct: fmt.Sprintf("%.0f", d.Share*100), Val: pct(d.Share)})
	}

	// stat tiles
	var rPinky float64
	for _, f := range m.Fingers {
		if f.Hand == "R" && f.Finger == "pinky" {
			rPinky = f.Share
		}
	}
	corr := "—"
	if m.CorrectionRate > 0 {
		corr = fmt.Sprintf("1 in %.0f", 1/m.CorrectionRate)
	}
	v.Tiles = []tileView{
		{K: "same-finger bigrams", V: pct(m.SFBPercent), N: "target <1%", Class: cls(m.SFBPercent > 0.01)},
		{K: "right pinky load", V: pct(rPinky), N: "comfortable ≈ 10%", Class: cls(rPinky > 0.14)},
		{K: "correction rate", V: corr, N: "Backspace / keystroke", Class: cls(m.CorrectionRate > 0.08)},
		{K: "left / right", V: fmt.Sprintf("%.0f / %.0f", m.HandBalanceL*100, m.HandBalanceR*100), N: "hand load", Class: "good"},
	}

	// findings
	for _, f := range fs {
		tag, rail := sevHTML(f.Severity)
		v.Findings = append(v.Findings, findView{Tag: tag, RailClass: rail, Title: f.Title, Body: f.Body})
	}

	// key frequency (top 24)
	var kmax float64
	if len(m.Keys) > 0 {
		kmax = m.Keys[0].Share
	}
	for i, k := range m.Keys {
		if i == 24 {
			break
		}
		v.Keys = append(v.Keys, barView{Label: k.Char, WidthPct: widthPct(k.Share, kmax), Val: pct(k.Share), Dead: k.Share < metrics.DeadThreshold})
	}

	// fingers in physical order
	order := []struct {
		hand, finger string
	}{
		{"L", "pinky"}, {"L", "ring"}, {"L", "middle"}, {"L", "index"}, {"L", "thumb"},
		{"R", "thumb"}, {"R", "index"}, {"R", "middle"}, {"R", "ring"}, {"R", "pinky"},
	}
	shareOf := map[[2]string]float64{}
	var fmax float64
	for _, f := range m.Fingers {
		shareOf[[2]string{f.Hand, f.Finger}] = f.Share
		if f.Share > fmax {
			fmax = f.Share
		}
	}
	for _, o := range order {
		sh := shareOf[[2]string{o.hand, o.finger}]
		v.Fingers = append(v.Fingers, fingerView{
			Finger: o.finger, HeightPct: widthPct(sh, fmax), Val: fmt.Sprintf("%.0f", sh*100), Right: o.hand == "R",
		})
	}

	// SFB
	v.SFBPct = pct(m.SFBPercent)
	for _, p := range m.SFBTop {
		v.SFB = append(v.SFB, pairView{Pair: p.Char1 + p.Char2, Finger: p.Finger, Count: p.Count, Pct: pct(p.Share)})
	}

	// contexts
	for _, c := range m.Contexts {
		cv := ctxView{Label: c.Label, Pct: pct(c.Share)}
		var cmax float64
		for _, k := range c.TopKeys {
			if k.Share > cmax {
				cmax = k.Share
			}
		}
		for _, k := range c.TopKeys {
			cv.Keys = append(cv.Keys, barView{Label: k.Char, WidthPct: widthPct(k.Share, cmax), Val: pct(k.Share)})
		}
		v.Contexts = append(v.Contexts, cv)
	}

	// mistypes
	var mmax int64
	for _, mk := range m.Mistypes {
		if mk.Count > mmax {
			mmax = mk.Count
		}
	}
	for i, mk := range m.Mistypes {
		if i == 12 {
			break
		}
		w := "0"
		if mmax > 0 {
			w = fmt.Sprintf("%.1f", float64(mk.Count)/float64(mmax)*100)
		}
		v.Mistypes = append(v.Mistypes, mistypeView{
			Char: mk.Char, Count: mk.Count, Rate: pct(mk.Rate), TopSub: mk.TopSub, WidthPct: w,
		})
	}

	// bindings
	for _, b := range m.Bindings {
		if b.InConfig && !b.Fired {
			v.DeadBindings++
			continue
		}
		v.Bindings = append(v.Bindings, bindView{Combo: b.Combo, Count: b.Count})
	}

	return v
}

func cls(bad bool) string {
	if bad {
		return "warn"
	}
	return "good"
}

func sevHTML(s rules.Severity) (tag, rail string) {
	switch s {
	case rules.Critical:
		return "▲ you're doing this wrong", "crit"
	case rules.Optimise:
		return "◆ optimise", "warn"
	case rules.NoAction:
		return "● no action", "good"
	default:
		return "· info", "none"
	}
}
