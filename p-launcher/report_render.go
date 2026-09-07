package main

import (
	_ "embed"
	"fmt"
	"html/template"
	"io"
	"math"
	"strconv"
	"strings"
)

//go:embed report.html.tmpl
var reportTmplSrc string

// renderReport writes the self-contained HTML report. Template funcs close
// over the theme and a per-render counter for unique SVG pattern ids.
func renderReport(w io.Writer, r Report) error {
	T := r.Theme
	stripID := 0
	funcs := template.FuncMap{
		"css": func(s string) template.CSS { return template.CSS(s) },
		"icon": func(name string, size int) template.HTML {
			return iconSVG(name, T.iconColor(name), size)
		},
		"iconc": func(name, color string, size int) template.HTML { return iconSVG(name, color, size) },
		"sparkline": func(vals []int, w, h int, color string) template.HTML {
			return sparkline(vals, w, h, color, T)
		},
		"kneeChart": func(b []Bucket, knee, w, h int) template.HTML { return kneeChart(b, knee, w, h, T) },
		"splitBars": func(rows []CompRow, w, rowH int) template.HTML { return splitBars(rows, w, rowH, T) },
		"strip": func(segs []StripSeg, w, h int) template.HTML {
			stripID++
			return stripSVG(segs, w, h, T, stripID)
		},
		"awaySwatch": func() template.HTML {
			stripID++
			return awaySwatch(T, stripID)
		},
		"fmtDur": fmtDur,
		"f1":     func(v float64) string { return strconv.FormatFloat(v, 'f', 1, 64) },
		"f2":     func(v float64) string { return strconv.FormatFloat(v, 'f', 2, 64) },
		"dash": func(v float64, suffix string) string {
			if math.IsNaN(v) {
				return "—"
			}
			return strconv.FormatFloat(v, 'f', 1, 64) + suffix
		},
		"pct": func(a, b int) string {
			if b == 0 {
				return "0%"
			}
			return fmt.Sprintf("%d%%", int(math.Round(float64(a)/float64(b)*100)))
		},
		"signed": func(n int) string {
			if n > 0 {
				return "+" + strconv.Itoa(n)
			}
			return strconv.Itoa(n)
		},
		"sub":   func(a, b int) int { return a - b },
		"lower": strings.ToLower,
		"time":  func(layout string, t interface{ Format(string) string }) string { return t.Format(layout) },
		"bucket": func(r Report) Bucket {
			if r.Knee >= 0 && r.Knee < len(r.Buckets) {
				return r.Buckets[r.Knee]
			}
			return Bucket{Label: "—"}
		},
		"rangeLink": func(cur, rng string) template.HTML {
			cls := "rng"
			if cur == rng {
				cls = "rng on"
			}
			return template.HTML(fmt.Sprintf(`<a class="%s" href="report-%s.html">%s</a>`, cls, rng, rng))
		},
		"stripTitle": func(r Report) string {
			return "strip " + r.StripFrom.In(r.GeneratedAt.Location()).Format("15:04") + " → " + r.StripTo.In(r.GeneratedAt.Location()).Format("15:04")
		},
		"stateLabelStyle": func(state string) template.CSS {
			switch state {
			case "you", "review":
				return template.CSS("color:" + T.You + ";font-weight:700;")
			case "claude":
				return template.CSS("color:" + T.Ink + ";font-weight:700;")
			}
			return template.CSS("color:" + T.Ink3 + ";font-weight:600;")
		},
		"weekEnd": func(ws []Week) string {
			if len(ws) == 0 {
				return ""
			}
			return ws[0].Label + "–" + ws[len(ws)-1].Label
		},
		"reportCSS": func() template.CSS { return reportCSS(T) },
	}
	t, err := template.New("report").Funcs(funcs).Parse(reportTmplSrc)
	if err != nil {
		return fmt.Errorf("parse report template: %w", err)
	}
	return t.Execute(w, r)
}

// reportCSS is the page stylesheet with the theme tokens substituted. Ported
// from gen.mjs page(); kept in Go rather than the template so the token
// values (rgba(...) included) never pass through the CSS escaper.
func reportCSS(T Theme) template.CSS {
	mono := `ui-monospace, "JetBrains Mono", "SF Mono", Menlo, "DejaVu Sans Mono", Consolas, monospace`
	css := `
    body { margin: 0; background: %[1]s; }
    .root { width: 1920px; height: 1080px; box-sizing: border-box; padding: 10px; background: %[1]s; color: %[2]s; font-family: %[3]s; font-size: 12px; line-height: 1.3; display: flex; flex-direction: column; gap: 8px; overflow: hidden; font-variant-numeric: tabular-nums; }
    a { color: %[4]s; text-decoration: none; } a:hover { color: %[2]s; }
    .panel { background: %[5]s; border: 1px solid %[6]s; border-radius: 3px; display: flex; flex-direction: column; min-width: 0; min-height: 0; flex: 1; }
    .ph { display: flex; align-items: center; justify-content: space-between; height: 26px; padding: 0 8px; border-bottom: 1px solid %[6]s; flex: none; }
    .pt { font-size: 13px; font-weight: 700; color: %[2]s; }
    .pt::before { content: "Q"; display: inline-block; font-size: 10px; font-weight: 700; letter-spacing: 0.06em; color: %[7]s; margin-right: 8px; }
    .pr { font-size: 11px; color: %[7]s; display: flex; gap: 12px; align-items: center; }
    .pr b { color: %[2]s; font-weight: 600; }
    .pb { padding: 6px 8px 8px; flex: 1; min-height: 0; display: flex; flex-direction: column; }
    .sub { font-size: 11px; font-weight: 700; letter-spacing: 0.08em; text-transform: uppercase; color: %[8]s; }
    table { border-collapse: collapse; width: 100%%; }
    th { font-size: 10px; font-weight: 600; letter-spacing: 0.06em; text-transform: uppercase; color: %[7]s; text-align: left; padding: 2px 6px; border-bottom: 1px solid %[6]s; white-space: nowrap; }
    th .plain { text-transform: none; letter-spacing: 0; }
    td { padding: 0 6px; height: 22px; border-bottom: 1px solid %[9]s; white-space: nowrap; color: %[2]s; }
    tr:last-child td { border-bottom: none; }
    .num { text-align: right; } th.num { text-align: right; }
    .dim { color: %[7]s; } .sec { color: %[8]s; } .you { color: %[10]s; } .ok { color: %[11]s; }
    .kv { display: flex; align-items: center; gap: 6px; }
    .legend { display: flex; gap: 12px; font-size: 11px; color: %[8]s; align-items: center; }
    .legend span { display: inline-flex; align-items: center; gap: 5px; }
    .sw { display: inline-block; width: 8px; height: 8px; border-radius: 1.5px; }
    .rule { color: %[2]s; background: %[12]s; border: 1px solid %[9]s; border-radius: 2px; padding: 1px 5px; font-size: 11px; }
    .chip { position: relative; display: inline-flex; align-items: center; gap: 4px; height: 16px; padding: 0 5px; border-radius: 2px; font-size: 10px; line-height: 16px; text-decoration: none; white-space: nowrap; color: %[8]s; border: 1px solid %[6]s; }
    .chip.other { border-style: dashed; border-color: %[7]s; }
    .chip.muted { color: %[7]s; }
    .chip .card { display: none; position: absolute; left: 0; top: 20px; z-index: 10; width: 340px; padding: 8px 10px; border-radius: 3px; white-space: normal; box-shadow: 0 6px 18px rgba(0,0,0,0.35); text-align: left; background: %[12]s; border: 1px solid %[6]s; color: %[2]s; }
    .chip:hover .card, .chip.demo .card { display: block; }
    .chip:hover { color: %[2]s; }
    .card .t { display: block; font-size: 11px; font-weight: 700; line-height: 1.3; }
    .card .d { display: -webkit-box; font-size: 10px; color: %[8]s; line-height: 1.35; margin-top: 3px; -webkit-line-clamp: 2; -webkit-box-orient: vertical; overflow: hidden; }
    .card .m { display: flex; gap: 10px; font-size: 10px; margin-top: 6px; align-items: center; }
    .card .st { margin-left: auto; font-weight: 700; letter-spacing: 0.06em; text-transform: uppercase; }
    .card .st.merged { color: %[11]s; } .card .st.open { color: %[4]s; } .card .st.closed { color: %[7]s; }
    .badge { font-size: 10px; font-weight: 700; letter-spacing: 0.08em; text-transform: uppercase; padding: 2px 6px; border-radius: 2px; border: 1px solid %[10]s; color: %[10]s; }
    .tile { padding: 5px 8px; border: 1px solid %[9]s; border-radius: 3px; background: %[12]s; min-width: 0; }
    .tile .l { font-size: 10px; letter-spacing: 0.06em; text-transform: uppercase; color: %[7]s; white-space: nowrap; }
    .tile .v { font-size: 20px; font-weight: 700; line-height: 1.1; font-variant-numeric: normal; }
    .tile .v small { font-size: 11px; font-weight: 400; color: %[8]s; }
    .tile .s { font-size: 10px; color: %[7]s; white-space: nowrap; }
    .rng { padding: 3px 10px; color: %[7]s; } .rng.on { background: %[2]s; color: %[1]s; font-weight: 700; }
    .rngbox { display: flex; border: 1px solid %[6]s; border-radius: 3px; overflow: hidden; font-size: 11px; }
    .sect { display: flex; flex-direction: column; }
    .empty { color: %[7]s; font-size: 11px; padding: 12px 0; }
    .yrow td { background: %[13]s; }
  `
	return template.CSS(fmt.Sprintf(css, T.BG, T.Ink, mono, T.Claude, T.Panel, T.Border, T.Ink3, T.Ink2, T.Grid, T.You, T.Friction, T.Panel2, T.YouTint))
}
