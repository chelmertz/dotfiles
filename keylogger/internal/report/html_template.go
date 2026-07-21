package report

import "html/template"

var htmlTmpl = template.Must(template.New("report").Parse(`<!doctype html>
<html lang="en"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width, initial-scale=1">
<title>keylog — {{.Heading}}</title>
<style>
:root{
 --page:#f9f9f7;--surface:#fcfcfb;--ink:#0b0b0b;--ink-2:#52514e;--muted:#898781;
 --grid:#e1e0d9;--baseline:#c3c2b7;--border:rgba(11,11,11,.10);--accent:#2a78d6;
 --left:#2a78d6;--right:#eb6834;--good:#0ca30c;--warning:#eda100;--critical:#d03b3b;
 --good-ink:#006300;--warn-ink:#8a5d00;--crit-ink:#b3232a;--keycap-bg:#fff;--keycap-sh:rgba(11,11,11,.16);
 --mono:ui-monospace,"SF Mono",Menlo,Consolas,monospace;--sans:system-ui,-apple-system,"Segoe UI",sans-serif;}
@media(prefers-color-scheme:dark){:root{
 --page:#0d0d0d;--surface:#1a1a19;--ink:#fff;--ink-2:#c3c2b7;--muted:#898781;--grid:#2c2c2a;
 --baseline:#383835;--border:rgba(255,255,255,.10);--accent:#3987e5;--left:#3987e5;--right:#d95926;
 --warning:#c98500;--critical:#e06666;--good-ink:#4fd24f;--warn-ink:#e8b74d;--crit-ink:#f08a8a;
 --keycap-bg:#262625;--keycap-sh:rgba(0,0,0,.5);}}
*{box-sizing:border-box}body{margin:0;background:var(--page);color:var(--ink);font-family:var(--sans);line-height:1.5}
.wrap{max-width:940px;margin:0 auto;padding:40px 24px 96px}
.eyebrow{font-family:var(--mono);font-size:12px;letter-spacing:.08em;text-transform:uppercase;color:var(--muted);margin:0 0 8px}
h1{font-size:30px;margin:0 0 6px;letter-spacing:-.01em}
.meta{display:flex;flex-wrap:wrap;gap:6px 20px;margin-top:16px;font-family:var(--mono);font-size:12.5px;color:var(--ink-2)}
.meta b{color:var(--ink);font-weight:600}
.device-split{display:flex;height:22px;border-radius:6px;overflow:hidden;margin:14px 0 2px;border:1px solid var(--border)}
.device-split div{display:flex;align-items:center;justify-content:center;font-family:var(--mono);font-size:11px;color:#fff}
.tiles{display:grid;grid-template-columns:repeat(4,1fr);gap:12px;margin:28px 0 8px}
.tile{background:var(--surface);border:1px solid var(--border);border-radius:10px;padding:16px}
.tile .k{font-family:var(--mono);font-size:11px;text-transform:uppercase;letter-spacing:.06em;color:var(--muted)}
.tile .v{font-size:26px;font-weight:650;margin-top:6px;font-variant-numeric:tabular-nums}
.tile .n{font-size:12.5px;color:var(--ink-2);margin-top:2px}
.v.warn{color:var(--warn-ink)}.v.good{color:var(--good-ink)}
.card{background:var(--surface);border:1px solid var(--border);border-radius:12px;padding:22px 24px;margin-top:20px}
.card h2{font-size:18px;margin:0 0 14px;letter-spacing:-.01em}
.bars{display:flex;flex-direction:column;gap:7px}
.row{display:grid;grid-template-columns:96px 1fr 62px;align-items:center;gap:10px}
.row .lab{text-align:right;font-family:var(--mono);font-size:13px;color:var(--ink-2)}
.track{height:16px}.bar{height:16px;border-radius:4px;min-width:3px;background:var(--accent)}
.row .val{font-family:var(--mono);font-size:12.5px;color:var(--ink-2);font-variant-numeric:tabular-nums}
.row.dead .bar{background:var(--baseline)}.row.dead .lab,.row.dead .val{color:var(--muted)}
.cap{display:inline-flex;align-items:center;justify-content:center;min-width:20px;height:20px;padding:0 5px;
 font-family:var(--mono);font-size:12px;font-weight:600;color:var(--ink);background:var(--keycap-bg);
 border:1px solid var(--border);border-bottom-width:2px;border-radius:5px;box-shadow:0 1px 0 var(--keycap-sh)}
.fscale{position:relative;height:150px;margin-top:6px}
.fingers{display:grid;grid-template-columns:repeat(10,1fr);gap:6px;align-items:end;height:150px}
.finger{display:flex;flex-direction:column;justify-content:flex-end;align-items:center;height:100%;gap:4px}
.finger .fbar{width:70%;max-width:34px;border-radius:4px 4px 2px 2px;background:var(--left)}
.finger.r .fbar{background:var(--right)}
.finger .fnum{font-family:var(--mono);font-size:11px;color:var(--ink-2)}
.finger .fname{font-family:var(--mono);font-size:10px;color:var(--muted)}
.legend{display:flex;gap:16px;margin-top:14px;font-size:12.5px;color:var(--ink-2)}
.legend i{display:inline-block;width:11px;height:11px;border-radius:3px;margin-right:6px}
table{width:100%;border-collapse:collapse;font-size:13.5px}
th,td{text-align:left;padding:7px 8px;border-bottom:1px solid var(--grid)}
th{font-family:var(--mono);font-size:11px;text-transform:uppercase;letter-spacing:.05em;color:var(--muted)}
td.num{text-align:right;font-family:var(--mono);font-variant-numeric:tabular-nums;color:var(--ink-2)}
.ctx{display:grid;grid-template-columns:repeat(auto-fit,minmax(200px,1fr));gap:18px}
.ctx h3{font-size:13px;font-family:var(--mono);margin:0 0 10px;display:flex;gap:8px}
.ctx h3 .pct{color:var(--muted);margin-left:auto}
.minibar{display:grid;grid-template-columns:54px 1fr 46px;align-items:center;gap:8px;margin-bottom:5px}
.minibar .l{text-align:right;font-family:var(--mono);font-size:12px;color:var(--ink-2)}
.minibar .b{height:12px;border-radius:3px;min-width:2px;background:var(--accent)}
.minibar .v{font-family:var(--mono);font-size:11.5px;color:var(--muted);text-align:right}
.verdict{margin-top:16px;border-left:3px solid var(--muted);padding:2px 0 2px 14px}
.verdict.crit{border-color:var(--critical)}.verdict.warn{border-color:var(--warning)}.verdict.good{border-color:var(--good)}
.verdict .tag{font-family:var(--mono);font-size:11px;text-transform:uppercase;letter-spacing:.06em;font-weight:700}
.verdict.crit .tag{color:var(--crit-ink)}.verdict.warn .tag{color:var(--warn-ink)}.verdict.good .tag{color:var(--good-ink)}.verdict.none .tag{color:var(--muted)}
.verdict h3{font-size:14.5px;margin:3px 0 0}.verdict p{margin:4px 0 0;font-size:13.5px;color:var(--ink-2)}
.foot{margin-top:34px;font-size:12.5px;color:var(--muted);font-family:var(--mono);border-top:1px solid var(--grid);padding-top:16px}
@media(max-width:640px){.tiles{grid-template-columns:repeat(2,1fr)}}
</style></head><body><div class="wrap">
<p class="eyebrow">keylog · session report</p>
<h1>{{.Heading}}</h1>
<div class="meta"><span>host <b>{{.Host}}</b></span>{{if .Dur}}<span>duration <b>{{.Dur}}</b></span>{{end}}{{if .Note}}<span><b>{{.Note}}</b></span>{{end}}<span>keydowns <b>{{.Total}}</b></span></div>
{{if .DeviceSplit}}<div class="device-split">{{range .DeviceSplit}}<div style="width:{{.WidthPct}}%;background:var(--left)">{{.Label}} · {{.Val}}</div>{{end}}</div>{{end}}

<div class="tiles">{{range .Tiles}}
 <div class="tile"><div class="k">{{.K}}</div><div class="v {{.Class}}">{{.V}}</div><div class="n">{{.N}}</div></div>{{end}}
</div>

<section class="card"><h2>Verdicts</h2>
{{range .Findings}}<div class="verdict {{.RailClass}}"><span class="tag">{{.Tag}}</span><h3>{{.Title}}</h3><p>{{.Body}}</p></div>{{end}}
</section>

<section class="card"><h2>Key frequency</h2><div class="bars">
{{range .Keys}}<div class="row{{if .Dead}} dead{{end}}"><div class="lab"><span class="cap">{{.Label}}</span></div><div class="track"><div class="bar" style="width:{{.WidthPct}}%"></div></div><div class="val">{{.Val}}</div></div>{{end}}
</div></section>

<section class="card"><h2>Per-finger load</h2><div class="fscale"><div class="fingers">
{{range .Fingers}}<div class="finger{{if .Right}} r{{end}}"><div class="fnum">{{.Val}}</div><div class="fbar" style="height:{{.HeightPct}}%"></div><div class="fname">{{.Finger}}</div></div>{{end}}
</div></div><div class="legend"><span><i style="background:var(--left)"></i>left</span><span><i style="background:var(--right)"></i>right</span></div></section>

<section class="card"><h2>Same-finger bigrams — {{.SFBPct}} of bigrams</h2>
<table><thead><tr><th>bigram</th><th>finger</th><th class="num">count</th><th class="num">% of bigrams</th></tr></thead><tbody>
{{range .SFB}}<tr><td><span class="cap">{{.Pair}}</span></td><td>{{.Finger}}</td><td class="num">{{.Count}}</td><td class="num">{{.Pct}}</td></tr>{{end}}
</tbody></table></section>

{{if .Mistypes}}<section class="card"><h2>Most-mistyped keys</h2>
<p class="lead" style="color:var(--ink-2);font-size:13.5px;margin:-6px 0 16px;max-width:64ch">Keys you typed, deleted, and replaced with a different key — normalized by how often you type them. High = genuinely awkward for you.</p>
<table><thead><tr><th>key</th><th></th><th class="num">mistypes</th><th class="num">of use</th><th>usually meant</th></tr></thead><tbody>
{{range .Mistypes}}<tr><td><span class="cap">{{.Char}}</span></td><td style="width:40%"><div class="b" style="width:{{.WidthPct}}%;height:12px;border-radius:3px;background:var(--right)"></div></td><td class="num">{{.Count}}</td><td class="num">{{.Rate}}</td><td><span class="cap">{{.TopSub}}</span></td></tr>{{end}}
</tbody></table></section>{{end}}

{{if .Contexts}}<section class="card"><h2>By context</h2><div class="ctx">
{{range .Contexts}}<div><h3>{{.Label}}<span class="pct">{{.Pct}}</span></h3>
{{range .Keys}}<div class="minibar"><div class="l"><span class="cap">{{.Label}}</span></div><div class="b" style="width:{{.WidthPct}}%"></div><div class="v">{{.Val}}</div></div>{{end}}
</div>{{end}}
</div></section>{{end}}

{{if .Bindings}}<section class="card"><h2>i3 bindings</h2><div class="bars">
{{range .Bindings}}<div class="row"><div class="lab"><span class="cap">{{.Combo}}</span></div><div class="track"><div class="bar" style="width:60%"></div></div><div class="val">{{.Count}}</div></div>{{end}}
</div>{{if .DeadBindings}}<p class="foot" style="border:0;margin-top:12px;padding:0">{{.DeadBindings}} configured bindings never fired this session.</p>{{end}}</section>{{end}}

<p class="foot">keylog · aggregated on the fly · raw keystrokes never written to disk · session #{{.SessionID}}</p>
</div></body></html>`))
