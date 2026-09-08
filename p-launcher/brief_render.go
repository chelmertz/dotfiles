package main

// The brief's page: the same ink and panels as the report, but plain prose
// and a numbered list, because a brief is read once in the morning and acted
// on, not studied.

import (
	"fmt"
	"html/template"
	"io"
	"strings"
	"time"
)

var briefTmpl = template.Must(template.New("brief").Funcs(template.FuncMap{
	"css":   func(s string) template.CSS { return template.CSS(s) },
	"short": shortPR,
}).Parse(`<!doctype html>
<html>
<head>
<meta charset="utf-8">
<title>brief {{.Date}}</title>
<style>
:root { color-scheme: {{if eq .T.Name "dark"}}dark{{else}}light{{end}}; }
body { margin: 0; padding: 32px 28px 48px; background: {{css .T.BG}}; color: {{css .T.Ink}};
  font: 14px/1.55 "Inter", ui-sans-serif, system-ui, sans-serif; }
main { max-width: 760px; margin: 0 auto; }
h1 { font-size: 13px; font-weight: 600; letter-spacing: 0.08em; text-transform: uppercase;
  color: {{css .T.Ink2}}; margin: 0 0 4px; }
.when { color: {{css .T.Ink3}}; font-size: 12px; margin: 0 0 24px; }
.summary { background: {{css .T.Panel}}; border: 1px solid {{css .T.Border}}; border-left: 2px solid {{css .T.You}};
  padding: 14px 16px; margin: 0 0 28px; }
h2 { font-size: 12px; font-weight: 600; letter-spacing: 0.08em; text-transform: uppercase;
  color: {{css .T.Ink3}}; margin: 28px 0 10px; }
ol { margin: 0; padding: 0; list-style: none; counter-reset: item; }
li { counter-increment: item; display: grid; grid-template-columns: 24px 1fr; gap: 10px;
  padding: 10px 0; border-bottom: 1px solid {{css .T.Grid}}; }
li::before { content: counter(item); color: {{css .T.Ink3}}; font-variant-numeric: tabular-nums; }
.action { font-weight: 600; }
.why { color: {{css .T.Ink2}}; }
a { color: {{css .T.Claude}}; text-decoration: none; }
a:hover { text-decoration: underline; }
table { border-collapse: collapse; width: 100%; }
td, th { text-align: left; padding: 6px 10px 6px 0; border-bottom: 1px solid {{css .T.Grid}}; font-weight: 400; }
th { color: {{css .T.Ink3}}; font-size: 12px; }
.num { font-variant-numeric: tabular-nums; }
.skip, .empty { color: {{css .T.Ink3}}; }
</style>
</head>
<body>
<main>
<h1>Daily brief</h1>
<p class="when">{{.Date}}{{if .Repeats}} · {{.Repeats}} carried over{{end}}</p>
{{if .Out.Summary}}<div class="summary">{{.Out.Summary}}</div>{{end}}
{{if .Out.Items}}
<h2>Do today</h2>
<ol>
{{range .Out.Items}}<li><div><div class="action">{{.Action}}</div>
<div class="why">{{.Why}}</div>
<a href="{{.URL}}">{{short .URL}}</a>{{range .Also}} · <a href="{{.}}">{{short .}}</a>{{end}}</div></li>
{{end}}</ol>
{{else}}<p class="empty">Nothing to act on today.</p>{{end}}
{{if .Out.Waiting}}
<h2>Waiting on others</h2>
<table><tr><th>PR</th><th>Reviewer</th><th>Days</th></tr>
{{range .Out.Waiting}}<tr><td><a href="{{.URL}}">{{short .URL}}</a></td><td>{{.Reviewer}}</td><td class="num">{{.Days}}</td></tr>
{{end}}</table>
{{end}}
{{if .Out.Skip}}<h2>Skip</h2><p class="skip">{{.Out.Skip}}</p>{{end}}
</main>
</body>
</html>
`))

// renderBrief writes the brief's page. repeats counts items the previous
// brief also named, shown next to the date so a stuck queue is visible.
func renderBrief(w io.Writer, out briefOut, at time.Time, t Theme) error {
	return briefTmpl.Execute(w, struct {
		Out     briefOut
		Date    string
		Repeats string
		T       Theme
	}{out, at.Format("Monday 2 January, 15:04"), "", t})
}

// briefText renders the brief for a terminal (the `brief --show` output and
// the notification body's long form).
func briefText(out briefOut, at time.Time) string {
	var b strings.Builder
	fmt.Fprintf(&b, "brief %s\n\n%s\n", at.Format("2006-01-02 15:04"), out.Summary)
	for i, it := range out.Items {
		fmt.Fprintf(&b, "\n%d. %s\n   %s\n   %s", i+1, it.Action, it.Why, it.URL)
		for _, u := range it.Also {
			fmt.Fprintf(&b, "\n   %s", u)
		}
	}
	if len(out.Items) > 0 {
		b.WriteString("\n")
	}
	for _, w := range out.Waiting {
		fmt.Fprintf(&b, "\nwaiting %s · %s · %dd", shortPR(w.URL), w.Reviewer, w.Days)
	}
	if len(out.Waiting) > 0 {
		b.WriteString("\n")
	}
	if out.Skip != "" {
		fmt.Fprintf(&b, "\nskip: %s\n", out.Skip)
	}
	return b.String()
}
