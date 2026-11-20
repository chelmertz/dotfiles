package main

import (
	"bytes"
	"fmt"
	"html/template"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

// The states page draws rules, so it cannot show a state or a move the code
// lacks: `claude-review states [<pr>]`, and the `?` on every elly row.

// diagramEdge is one drawn move: a rule that applies from a state.
type diagramEdge struct {
	from stateKey
	r    rule
}

// broad: a rule that applies from most states is listed once on the page,
// not drawn from each of them.
func (r rule) broad() bool {
	n := 0
	for _, k := range allStates {
		if r.from.matches(k.State, k.Kind) {
			n++
		}
	}
	return n > len(allStates)/2
}

// edgesFrom are the rules in rs that can fire from k, in order: a rule
// after an unguarded one for the same event never fires from k, so it is
// left out.
func edgesFrom(rs []rule, k stateKey) []diagramEdge {
	var out []diagramEdge
	done := map[EventKind]bool{}
	for _, r := range rs {
		if !r.from.matches(k.State, k.Kind) || done[r.event] {
			continue
		}
		if r.when.ok == nil {
			done[r.event] = true
		}
		out = append(out, diagramEdge{k, r})
	}
	return out
}

func (k stateKey) name() string {
	if k.Kind == "" {
		return string(k.State)
	}
	return string(k.State) + "{" + k.Kind + "}"
}

func (r rule) label() string {
	l := string(r.event)
	if r.when.name != "" {
		l += " (" + r.when.name + ")"
	}
	return l
}

func (r rule) targetName() string {
	switch {
	case r.to.prev:
		return "back to the state before"
	case r.to.decided:
		return "the round's result"
	case r.to.dropWindow:
		return "the same, without the window"
	case r.to.deletes:
		return "deleted, with its row"
	}
	return r.to.to.name()
}

func effectsText(e []Effect) string {
	if len(e) == 0 {
		return ""
	}
	s := make([]string, len(e))
	for i, x := range e {
		s[i] = string(x)
	}
	return strings.Join(s, ", ")
}

type svgBox struct {
	Name, Title string
	Kinds       []svgKind
	X, Y, H     int
	Current     bool
	Pseudo      bool
}

type svgKind struct {
	Name    string
	X, Y    int
	Current bool
}

type svgArrow struct {
	Path, Label string
	LX, LY      int
}

type pageMove struct{ Event, To, Effects string }

type pageState struct {
	Name, Row, Note, Links string
	Current                bool
	Moves                  []pageMove
}

type statesData struct {
	PR            string
	Current       string
	Width, Height int
	Boxes         []svgBox
	Arrows        []svgArrow
	Anywhere      []pageMove
	States        []pageState
}

const (
	boxW, colW = 200, 330
	lineH, gap = 16, 34
	pad        = 30
	decideNode = "the round's result"
)

// columns is the diagram's layout, left to right: one box per state, its
// kinds inside. TestStatesColumnsHoldEveryState fails on a state left out.
var columns = [][]string{
	{string(stIdle)},
	{string(stStarting), string(stRunning)},
	{decideNode},
	{string(stAwaiting), string(stHeld), string(stStuck), string(stHandled)},
}

func kindsOf(st string) []string {
	var out []string
	for _, k := range allStates {
		if string(k.State) == st && k.Kind != "" {
			out = append(out, k.Kind)
		}
	}
	return out
}

// boxes places each box; a box is as tall as its kinds.
func boxes(cur stateKey) (map[string]svgBox, int, int) {
	out := map[string]svgBox{}
	height := 0
	for c, col := range columns {
		y := pad
		for _, n := range col {
			b := svgBox{Name: n, Title: n, X: pad + c*colW, Y: y, Current: n == string(cur.State)}
			b.H = 28 + lineH*len(kindsOf(n))
			if n == decideNode {
				b.Pseudo, b.Title = true, "decide reads the finished round on GitHub"
			}
			for i, kind := range kindsOf(n) {
				b.Kinds = append(b.Kinds, svgKind{Name: kind, X: b.X + 18, Y: y + 38 + i*lineH, Current: b.Current && kind == cur.Kind})
			}
			out[n] = b
			y += b.H + gap
		}
		height = max(height, y)
	}
	return out, pad + len(columns)*colW - (colW - boxW), height
}

func arrow(a, b svgBox, label string, nth int) svgArrow {
	x1, y1 := a.X+boxW, a.Y+a.H/2
	x2, y2 := b.X, b.Y+b.H/2
	c := 80
	switch {
	case a.Name == b.Name: // a loop on the box's right side
		x1, y1, x2, y2, c = a.X+boxW, a.Y+10, a.X+boxW, a.Y+a.H-10, -60
		return svgArrow{Path: fmt.Sprintf("M%d %d C%d %d %d %d %d %d", x1, y1, x1-c, y1, x2-c, y2, x2, y2), Label: label, LX: x1 + 50, LY: (y1 + y2) / 2}
	case b.X < a.X: // backwards: from the left side, around below
		x1, x2 = a.X, b.X+boxW
		c = -80
	case b.X == a.X: // same column: right side to right side
		x2 = b.X + boxW
		c = -80
		return svgArrow{Path: fmt.Sprintf("M%d %d C%d %d %d %d %d %d", x1, y1, x1-c, y1, x2-c, y2, x2, y2), Label: label, LX: x1 + 70, LY: (y1 + y2) / 2}
	}
	path := fmt.Sprintf("M%d %d C%d %d %d %d %d %d", x1, y1, x1+c, y1, x2-c, y2, x2, y2)
	if c < 0 { // a backward label sits by its start, clear of the forward ones
		return svgArrow{Path: path, Label: label, LX: x1 - 40, LY: y1 - 6 + 14*nth}
	}
	return svgArrow{Path: path, Label: label, LX: (x1 + x2) / 2, LY: (y1+y2)/2 - 4}
}

// stateArrows is one arrow per pair of boxes, labelled with every event
// that moves a PR between them; moves back to the state before draw none.
func stateArrows(pos map[string]svgBox) []svgArrow {
	type pair struct{ from, to string }
	var order []pair
	events := map[pair][]string{}
	add := func(p pair, ev string) {
		if _, ok := events[p]; !ok {
			order = append(order, p)
		}
		if !slices.Contains(events[p], ev) {
			events[p] = append(events[p], ev)
		}
	}
	for _, k := range allStates {
		for _, e := range edgesFrom(rules, k) {
			if e.r.broad() || e.r.to.prev {
				continue
			}
			to := string(e.r.to.to.State)
			if e.r.to.decided {
				to = decideNode
			}
			add(pair{string(k.State), to}, string(e.r.event))
		}
	}
	for _, k := range decidable {
		add(pair{decideNode, string(k.State)}, "")
	}
	var out []svgArrow
	back := map[string]int{} // backward arrows already drawn from a box
	for _, p := range order {
		label := strings.Trim(strings.Join(events[p], ", "), ", ")
		out = append(out, arrow(pos[p.from], pos[p.to], label, back[p.from]))
		if pos[p.to].X < pos[p.from].X {
			back[p.from]++
		}
	}
	return out
}

// stateRow is k's line in the table: its elly row as render writes it,
// and its moves.
func stateRow(k stateKey, pr PR, cur string) pageState {
	a := render(pr, PRState{State: k.State, Kind: k.Kind, Detail: "…", Head: "0123456789abcdef"}, View{})
	ps := pageState{Name: k.name(), Row: string(a.State), Note: a.Note, Current: k.name() == cur}
	if ps.Row == "" {
		ps.Row = "no row"
	}
	labels := make([]string, len(a.Links))
	for i, l := range a.Links {
		labels[i] = l.Label
		if l.Primary {
			labels[i] += " (button)"
		}
	}
	ps.Links = strings.Join(labels, " · ")
	for _, e := range edgesFrom(rules, k) {
		if !e.r.broad() {
			ps.Moves = append(ps.Moves, pageMove{e.r.label(), e.r.targetName(), effectsText(e.r.effects)})
		}
	}
	return ps
}

// statesPageData builds the page for pr in s; nil marks nothing.
func statesPageData(pr *PR, s *PRState) statesData {
	var cur stateKey
	d := statesData{}
	sample := PR{Owner: "owner", Repo: "repo", Number: 1}
	if pr != nil {
		sample, d.PR = *pr, pr.URL()
	}
	if s != nil {
		cur = stateKey{s.State, s.Kind}
		d.Current = cur.name()
	}
	pos, w, h := boxes(cur)
	d.Width, d.Height = w, h
	for _, col := range columns {
		for _, n := range col {
			d.Boxes = append(d.Boxes, pos[n])
		}
	}
	d.Arrows = stateArrows(pos)
	for _, k := range allStates {
		d.States = append(d.States, stateRow(k, sample, d.Current))
	}
	for _, r := range rules {
		if !r.broad() {
			continue
		}
		from := "any state"
		if r.from.not {
			from = "any state but " + strings.Join(matchNames(r.from), ", ")
		}
		d.Anywhere = append(d.Anywhere, pageMove{r.label() + ", from " + from, r.targetName(), effectsText(r.effects)})
	}
	return d
}

// matchNames are the states a negated match leaves out.
func matchNames(m match) []string {
	var out []string
	for _, k := range allStates {
		if !m.matches(k.State, k.Kind) && !slices.Contains(out, string(k.State)) {
			out = append(out, string(k.State))
		}
	}
	return out
}

var statesTmpl = template.Must(template.New("states").Parse(`<!doctype html>
<html lang="en"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width, initial-scale=1">
<title>claude-review states</title>
<style>
:root { --bg:#fafafa; --fg:#1f2328; --muted:#656d76; --line:#8c959f; --box:#fff; --cur:#f6c344; --pseudo:#eef1f5; }
@media (prefers-color-scheme: dark) { :root { --bg:#0d1117; --fg:#e6edf3; --muted:#8d96a0; --line:#6e7681; --box:#161b22; --cur:#9e6a03; --pseudo:#21262d; } }
body { margin:0; padding:16px; background:var(--bg); color:var(--fg); font:14px/1.45 system-ui, sans-serif; }
h1 { font-size:18px; margin:0 0 4px; } p { color:var(--muted); margin:0 0 12px; }
.svg { overflow-x:auto; border:1px solid var(--line); border-radius:8px; background:var(--bg); }
svg text { fill:var(--fg); font:12px system-ui, sans-serif; } svg .kind { fill:var(--muted); font-size:11px; }
svg rect { fill:var(--box); stroke:var(--line); } svg rect.cur { fill:color-mix(in srgb, var(--cur) 35%, var(--box)); stroke-width:2; } svg .curkind { fill:var(--fg); font-weight:700; } svg rect.pseudo { fill:var(--pseudo); stroke-dasharray:4 3; }
svg path { fill:none; stroke:var(--line); stroke-width:1; marker-end:url(#a); } svg .lbl { fill:var(--muted); font-size:10px; }
table { border-collapse:collapse; margin-top:16px; width:100%; } td, th { text-align:left; vertical-align:top; padding:6px 8px; border-top:1px solid var(--line); }
tr.cur td { background:color-mix(in srgb, var(--cur) 30%, transparent); } code { font-size:12px; } ul { margin:0; padding-left:16px; }
</style></head><body>
<h1>claude-review states</h1>
<p>{{if .PR}}{{.PR}}: {{if .Current}}<b>{{.Current}}</b>, highlighted{{else}}no state{{end}}. {{end}}Drawn from the rules the code runs; a move with no arrow returns to the state before.</p>
<div class="svg"><svg width="{{.Width}}" height="{{.Height}}" viewBox="0 0 {{.Width}} {{.Height}}" role="img" aria-label="state diagram">
<defs><marker id="a" viewBox="0 0 10 10" refX="10" refY="5" markerWidth="7" markerHeight="7" orient="auto-start-reverse"><path d="M0 0L10 5L0 10z" style="fill:var(--line);stroke:none"/></marker></defs>
{{range .Arrows}}<path d="{{.Path}}"/>{{if .Label}}<text class="lbl" x="{{.LX}}" y="{{.LY}}" text-anchor="middle">{{.Label}}</text>{{end}}
{{end}}{{range .Boxes}}<g><title>{{.Title}}</title><rect x="{{.X}}" y="{{.Y}}" width="200" height="{{.H}}" rx="6"{{if .Current}} class="cur"{{else if .Pseudo}} class="pseudo"{{end}}/><text x="{{.X}}" y="{{.Y}}" dx="10" dy="19" font-weight="600">{{.Name}}</text>{{range .Kinds}}<text class="kind{{if .Current}} curkind{{end}}" x="{{.X}}" y="{{.Y}}">{{.Name}}</text>{{end}}</g>
{{end}}</svg></div>
<table><tr><th>From any state</th><th>to</th><th>effects</th></tr>
{{range .Anywhere}}<tr><td>{{.Event}}</td><td>{{.To}}</td><td>{{.Effects}}</td></tr>{{end}}</table>
<table><tr><th>state</th><th>elly row</th><th>moves</th></tr>
{{range .States}}<tr{{if .Current}} class="cur"{{end}}><td><code>{{.Name}}</code></td><td>{{.Row}}{{if .Note}}: {{.Note}}{{end}}{{if .Links}}<br><small>{{.Links}}</small>{{end}}</td>
<td><ul>{{range .Moves}}<li>{{.Event}} → {{.To}}{{if .Effects}} <small>({{.Effects}})</small>{{end}}</li>{{end}}</ul></td></tr>
{{end}}</table>
</body></html>
`))

func statesPage(pr *PR, s *PRState) (string, error) {
	var b bytes.Buffer
	err := statesTmpl.Execute(&b, statesPageData(pr, s))
	return b.String(), err
}

// runStates writes the page for `claude-review states [<pr>]` and prints
// its path; open, from the `?` link, opens it in the browser too.
func runStates(args []string, d Deps, open bool) int {
	var pr *PR
	var s *PRState
	if len(args) > 1 {
		_, _ = fmt.Fprintln(d.Stderr, "usage: claude-review states [<pr>]")
		return 2
	}
	if len(args) == 1 {
		p, err := ParseTarget(args[0])
		if err != nil {
			_, _ = fmt.Fprintln(d.Stderr, err)
			return 2
		}
		pr = &p
		l, err := OpenLedger(LedgerPath(d.StateDir))
		if err != nil {
			return fail(d, err)
		}
		st, ok, err := l.PRState(p)
		_ = l.Close()
		if err != nil {
			return fail(d, err)
		}
		if ok {
			s = &st
		}
	}
	page, err := statesPage(pr, s)
	if err != nil {
		return fail(d, err)
	}
	dir := filepath.Dir(LedgerPath(d.StateDir))
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fail(d, err)
	}
	path := filepath.Join(dir, "states.html")
	if err := os.WriteFile(path, []byte(page), 0o600); err != nil { //nolint:gosec // path under the user's own state dir
		return fail(d, err)
	}
	_, _ = fmt.Fprintln(d.Stdout, path)
	if open {
		opener := "xdg-open"
		if d.GOOS == darwin {
			opener = "open"
		}
		if err := d.Spawn("", []string{opener, path}); err != nil {
			return fail(d, err)
		}
	}
	return 0
}
