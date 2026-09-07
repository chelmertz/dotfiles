package main

import (
	"fmt"
	"html/template"
	"math"
	"strconv"
	"strings"
)

// Inline SVG helpers, ported 1:1 from report-design/gen.mjs so the Go report
// matches the mockup. Only line, path, rect, circle and text are used.

func itoa(n int) string { return strconv.Itoa(n) }

// f2 formats like JS Math.round(n*100)/100 rendered by String(): no trailing
// zeros, no exponent.
func f2(n float64) string { return strconv.FormatFloat(math.Round(n*100)/100, 'f', -1, 64) }

type textOpt struct {
	size         int
	fill, anchor string
	weight       int
}

func txt(x, y float64, s string, T Theme, o textOpt) string {
	if o.size == 0 {
		o.size = 10
	}
	if o.fill == "" {
		o.fill = T.Ink3
	}
	var b strings.Builder
	fmt.Fprintf(&b, `<text x="%s" y="%s" font-size="%d" fill="%s"`, f2(x), f2(y), o.size, o.fill)
	if o.anchor != "" {
		fmt.Fprintf(&b, ` text-anchor="%s"`, o.anchor)
	}
	if o.weight != 0 {
		fmt.Fprintf(&b, ` font-weight="%d"`, o.weight)
	}
	b.WriteString(">" + template.HTMLEscapeString(s) + "</text>")
	return b.String()
}

func hline(x1, x2, y float64, c string) string {
	return fmt.Sprintf(`<line x1="%s" x2="%s" y1="%s" y2="%s" stroke="%s" stroke-width="1"></line>`, f2(x1), f2(x2), f2(y), f2(y), c)
}

func roundBar(x0, yTop, w, yBase float64, color string) string {
	r := math.Min(3, yBase-yTop)
	return fmt.Sprintf(`<path d="M%s %s V%s Q%s %s %s %s H%s Q%s %s %s %s V%s Z" fill="%s"></path>`,
		f2(x0), f2(yBase), f2(yTop+r), f2(x0), f2(yTop), f2(x0+r), f2(yTop), f2(x0+w-r), f2(x0+w), f2(yTop), f2(x0+w), f2(yTop+r), f2(yBase), color)
}

func svgOpen(w, h int, style string) string {
	return fmt.Sprintf(`<svg width="%d" height="%d" viewBox="0 0 %d %d" style="%s">`, w, h, w, h, style)
}

// sparkline is a small trend line with a colored end dot.
func sparkline(vals []int, w, h int, color string, T Theme) template.HTML {
	if len(vals) == 0 {
		vals = []int{0, 0}
	} else if len(vals) == 1 {
		vals = []int{vals[0], vals[0]}
	}
	mx := 0
	for _, v := range vals {
		if v > mx {
			mx = v
		}
	}
	if mx == 0 {
		mx = 1
	}
	step := float64(w) / float64(len(vals)-1)
	var d strings.Builder
	var lx, ly string
	for i, v := range vals {
		x, y := float64(i)*step, float64(h)-2-float64(v)/float64(mx)*float64(h-4)
		lx, ly = f2(x), f2(y)
		if i == 0 {
			d.WriteString("M")
		} else {
			d.WriteString(" L")
		}
		d.WriteString(lx + " " + ly)
	}
	return template.HTML(svgOpen(w, h, "display:block;overflow:visible") +
		fmt.Sprintf(`<path d="%s" fill="none" stroke="%s" stroke-width="1.5" stroke-linejoin="round" stroke-linecap="round"></path><circle cx="%s" cy="%s" r="2.5" fill="%s" stroke="%s" stroke-width="1.5"></circle></svg>`,
			d.String(), T.Ink3, lx, ly, color, T.Panel))
}

// kneeChart: one figure, shared x (concurrency bucket): bars = merged PRs per
// active hour, line = median blocked-wait. Two stacked plots on one x-axis
// instead of two y-scales on one plot, so neither scale is arbitrary.
func kneeChart(rows []Bucket, knee, W, H int, T Theme) template.HTML {
	padL, padR, padT, gapY, padB := 74.0, 16.0, 14.0, 26.0, 34.0
	fH := float64(H)
	topH := math.Round((fH - padT - gapY - padB) * 0.55)
	botH := fH - padT - gapY - padB - topH
	pw := float64(W) - padL - padR
	n := len(rows)
	if n == 0 {
		n = 1
	}
	slot := pw / float64(n)
	bw := math.Min(24, slot*0.35)
	var g strings.Builder
	if knee >= 0 && knee < len(rows) {
		g.WriteString(fmt.Sprintf(`<rect x="%s" y="%s" width="%s" height="%s" fill="%s"></rect>`, f2(padL+slot*float64(knee)), f2(padT-4), f2(slot), f2(fH-padB-padT+4), T.ClaudeTint))
	}
	// top plot: bars
	tMax, maxPR := 0.6, 0.0
	maxWait := 0
	for _, r := range rows {
		maxPR = math.Max(maxPR, r.PRsPerHour)
		if r.WaitSec > maxWait {
			maxWait = r.WaitSec
		}
	}
	if maxPR > tMax {
		tMax = math.Ceil(maxPR*10) / 10
	}
	ty := func(v float64) float64 { return padT + topH - v/tMax*topH }
	for i := 0; i <= 3; i++ {
		t := tMax / 3 * float64(i)
		g.WriteString(hline(padL, float64(W)-padR, ty(t), T.Grid) + txt(padL-8, ty(t)+3.5, strconv.FormatFloat(t, 'f', 1, 64), T, textOpt{anchor: "end"}))
	}
	g.WriteString(txt(padL-8, padT-4, "finished PRs per hour", T, textOpt{anchor: "start", fill: T.Ink2}))
	for i, r := range rows {
		cx := padL + slot*float64(i) + slot/2
		g.WriteString(roundBar(cx-bw/2, ty(r.PRsPerHour), bw, ty(0), T.Claude))
		o := textOpt{anchor: "middle", fill: T.Ink2}
		if i == knee {
			o.fill, o.weight = T.Ink, 700
		}
		if r.Hours > 0 {
			g.WriteString(txt(cx, ty(r.PRsPerHour)-5, strconv.FormatFloat(r.PRsPerHour, 'f', 2, 64), T, o))
		}
	}
	g.WriteString(hline(padL, float64(W)-padR, ty(0), T.Axis))
	// bottom plot: line
	by0 := padT + topH + gapY
	bMax := 900.0
	if float64(maxWait) > bMax {
		bMax = math.Ceil(float64(maxWait)/300) * 300
	}
	by := func(v float64) float64 { return by0 + botH - v/bMax*botH }
	for i := 0; i <= 3; i++ {
		t := bMax / 3 * float64(i)
		lbl := "0"
		if i > 0 {
			lbl = f2(t/60) + "m"
		}
		g.WriteString(hline(padL, float64(W)-padR, by(t), T.Grid) + txt(padL-8, by(t)+3.5, lbl, T, textOpt{anchor: "end"}))
	}
	g.WriteString(txt(padL-8, by0-6, "agents waiting for me (median, away time excluded)", T, textOpt{anchor: "start", fill: T.Ink2}))
	var d strings.Builder
	type pt struct{ x, y float64 }
	pts := make([]pt, len(rows))
	for i, r := range rows {
		pts[i] = pt{padL + slot*float64(i) + slot/2, by(float64(r.WaitSec))}
		if i == 0 {
			d.WriteString("M")
		} else {
			d.WriteString(" L")
		}
		d.WriteString(f2(pts[i].x) + " " + f2(pts[i].y))
	}
	if len(rows) > 0 {
		g.WriteString(fmt.Sprintf(`<path d="%s" fill="none" stroke="%s" stroke-width="2" stroke-linejoin="round" stroke-linecap="round"></path>`, d.String(), T.You))
	}
	for i, p := range pts {
		g.WriteString(fmt.Sprintf(`<circle cx="%s" cy="%s" r="4" fill="%s" stroke="%s" stroke-width="2"></circle>`, f2(p.x), f2(p.y), T.You, T.Panel))
		if rows[i].Hours > 0 && (i == knee || i == len(rows)-1) {
			dx, anchor := 8.0, "start"
			if i == len(rows)-1 {
				dx, anchor = -8, "end"
			}
			g.WriteString(txt(p.x+dx, p.y-7, fmtDur(rows[i].WaitSec), T, textOpt{anchor: anchor, fill: T.Ink}))
		}
	}
	g.WriteString(hline(padL, float64(W)-padR, by(0), T.Axis))
	// shared x labels
	for i, r := range rows {
		cx := padL + slot*float64(i) + slot/2
		o := textOpt{anchor: "middle", size: 11, fill: T.Ink2}
		if i == knee {
			o.fill, o.weight = T.Ink, 700
		}
		g.WriteString(txt(cx, fH-padB+14, r.Label, T, o))
		g.WriteString(txt(cx, fH-padB+27, fmt.Sprintf("%d h", r.Hours), T, textOpt{anchor: "middle"}))
	}
	g.WriteString(txt(padL-8, fH-padB+14, "concurrent", T, textOpt{anchor: "end", fill: T.Ink2}) + txt(padL-8, fH-padB+27, "hours seen", T, textOpt{anchor: "end"}))
	return template.HTML(svgOpen(W, H, "display:block;font-family:inherit") + g.String() + "</svg>")
}

// splitBars: horizontal stacked bars, auto (solid) + manual (45% opacity),
// one row per project.
func splitBars(rows []CompRow, W, rowH int, T Theme) template.HTML {
	labelW, valW := 150.0, 44.0
	mx := 0.6
	for _, r := range rows {
		mx = math.Max(mx, r.Auto+r.Manual)
	}
	bx := labelW
	bwid := float64(W) - labelW - valW
	var g strings.Builder
	for i, r := range rows {
		y := float64(i * rowH)
		a, m := r.Auto/mx*bwid, r.Manual/mx*bwid
		fill := T.Ink
		if strings.HasPrefix(r.Path, "other") {
			fill = T.Ink3
		}
		g.WriteString(txt(labelW-8, y+float64(rowH)/2+3.5, r.Path, T, textOpt{anchor: "end", size: 11, fill: fill}))
		yb := f2(y + float64(rowH)/2 - 4)
		g.WriteString(fmt.Sprintf(`<rect x="%s" y="%s" width="%s" height="8" rx="1" fill="%s"></rect>`, f2(bx), yb, f2(bwid), T.BarTrack))
		g.WriteString(fmt.Sprintf(`<rect x="%s" y="%s" width="%s" height="8" rx="1" fill="%s"></rect>`, f2(bx), yb, f2(a), T.Friction))
		if m > 0 {
			g.WriteString(fmt.Sprintf(`<rect x="%s" y="%s" width="%s" height="8" rx="1" fill="%s" fill-opacity="0.45"></rect>`, f2(bx+a+2), yb, f2(math.Max(0, m-2)), T.Friction))
		}
		g.WriteString(txt(float64(W), y+float64(rowH)/2+3.5, strconv.FormatFloat(r.Auto+r.Manual, 'f', 2, 64), T, textOpt{anchor: "end", size: 11, fill: T.Ink}))
	}
	h := len(rows) * rowH
	return template.HTML(svgOpen(W, h, "display:block;font-family:inherit") + g.String() + "</svg>")
}

// stripSVG draws 60 minutes of states; away is a hatched pattern whose id
// must be unique per strip on the page (id).
func stripSVG(segs []StripSeg, W, H int, T Theme, id int) template.HTML {
	pid := fmt.Sprintf("away-%d", id)
	col := map[string]string{"claude": T.Claude, "you": T.You, "idle": T.Idle, "away": T.Ink3}
	var g strings.Builder
	x := 0.0
	for _, s := range segs {
		w := float64(s.Mins) / float64(stripMinutes) * float64(W)
		ww := f2(math.Max(0, w-1.5))
		if s.State == "away" {
			g.WriteString(fmt.Sprintf(`<rect x="%s" y="0" width="%s" height="%d" rx="1" fill="url(#%s)"></rect><rect x="%s" y="0.5" width="%s" height="%d" rx="1" fill="none" stroke="%s" stroke-width="1" stroke-opacity="0.8"></rect>`, f2(x), ww, H, pid, f2(x), ww, H-1, T.Ink2))
		} else {
			op := "0.9"
			if s.State == "idle" {
				op = "0.45"
			}
			g.WriteString(fmt.Sprintf(`<rect x="%s" y="0" width="%s" height="%d" rx="1" fill="%s" fill-opacity="%s"></rect>`, f2(x), ww, H, col[s.State], op))
		}
		x += w
	}
	pat := fmt.Sprintf(`<defs><pattern id="%s" width="4" height="4" patternUnits="userSpaceOnUse" patternTransform="rotate(45)"><line x1="0" y1="0" x2="0" y2="4" stroke="%s" stroke-width="1.4" stroke-opacity="0.9"></line></pattern></defs>`, pid, T.Ink2)
	return template.HTML(svgOpen(W, H, "display:block") + pat + g.String() + "</svg>")
}

// awaySwatch is the legend swatch for the hatched away pattern.
func awaySwatch(T Theme, id int) template.HTML {
	pid := fmt.Sprintf("away-leg-%d", id)
	return template.HTML(fmt.Sprintf(`<svg width="10" height="8" viewBox="0 0 10 8" style="display:block;flex:none"><defs><pattern id="%s" width="4" height="4" patternUnits="userSpaceOnUse" patternTransform="rotate(45)"><line x1="0" y1="0" x2="0" y2="4" stroke="%s" stroke-width="1.2" stroke-opacity="0.7"></line></pattern></defs><rect width="10" height="8" rx="1" fill="url(#%s)" stroke="%s" stroke-opacity="0.6"></rect></svg>`, pid, T.Ink3, pid, T.Ink3))
}
