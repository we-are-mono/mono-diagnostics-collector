package main

import (
	"fmt"
	"html"
	"html/template"
	"log"
	"net/http"
	"strings"
	"time"
)

// maxWindows is how many windows the "Max" rows show: 24 h, 2 days, 7 days.
const maxWindows = 3

type dashPage struct {
	Device string // the full deviceId in the device view; empty in the overview
	Short  string
	Sum    summary
	Cards  []card
	// The device view's charts (nil with no temperature in 7 days) and table.
	Chart       *chart
	SensorChart *chart
	Keys        []sensorKey
	Rows        []row
	Windows     []window
	Now         time.Time
}

// dashboard serves the overview at "/" and one device at "/?device=<id>".
func dashboard(s *store, now func() time.Time) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" {
			http.NotFound(w, r)
			return
		}
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			http.Error(w, "GET only", http.StatusMethodNotAllowed)
			return
		}

		p := dashPage{Windows: windows, Now: now()}
		if id := r.URL.Query().Get("device"); id != "" {
			sum, found := s.device(id, p.Now)
			if !found {
				http.Error(w, "no readings from this device", http.StatusNotFound)
				return
			}
			p.Device, p.Short, p.Sum = id, shortID(id), sum
			start, hottest := s.hourly(id, p.Now)
			p.Chart = newChart(start, []series{{"Hottest sensor", hottest}})
			start, sensors := s.sensorHours(id, p.Now)
			for i := range sensors {
				sensors[i].Name = displayName(sensors[i].Name)
			}
			p.SensorChart = newChart(start, sensors)
			p.Keys, p.Rows = s.readings(id, start)
		} else {
			p.Sum, p.Cards = s.overview(p.Now)
		}

		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Cache-Control", "no-store")
		if err := dashTemplate.Execute(w, p); err != nil {
			log.Printf("dashboard: %v", err)
		}
	})
}

// temp formats a temperature, or a dash when the window has none.
func temp(s stat) string {
	if !s.OK {
		return "–"
	}
	return fmt.Sprintf("%.1f °C", s.V)
}

// cell formats one table value: 3 decimals for a key that ever had a decimal
// point, a whole number otherwise, and a dash when the reading has no value.
func cell(k sensorKey, v stat) string {
	switch {
	case !v.OK:
		return "–"
	case k.Decimal:
		return fmt.Sprintf("%.3f", v.V)
	default:
		return fmt.Sprintf("%.0f", v.V)
	}
}

// column returns a table column name: the display name, which may break after
// each "_" so that all the columns fit the window ("ddr_" over "controller").
func column(key string) template.HTML {
	return template.HTML(strings.ReplaceAll(html.EscapeString(displayName(key)), "_", "_<wbr>"))
}

// tableWidth returns the readings table's minimum width for n value columns:
// below it, the table box scrolls instead of squeezing the columns.
func tableWidth(n int) template.CSS {
	return template.CSS(fmt.Sprintf("min-width: %gem", 7.5+4.75*float64(n)))
}

// ago says how long before now a reading came, in the largest whole unit.
func ago(t, now time.Time) string {
	if t.IsZero() {
		return "never"
	}
	d := now.Sub(t)
	switch {
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		return fmt.Sprintf("%d min ago", int(d/time.Minute))
	case d < 48*time.Hour:
		return fmt.Sprintf("%d h ago", int(d/time.Hour))
	default:
		return fmt.Sprintf("%d days ago", int(d/(24*time.Hour)))
	}
}

// quiet reports whether a device sent nothing in the last 24 h.
func quiet(t, now time.Time) bool {
	return now.Sub(t) > 24*time.Hour
}

var dashTemplate = template.Must(template.New("dash").Funcs(template.FuncMap{
	"temp":       temp,
	"cell":       cell,
	"short":      displayName,
	"column":     column,
	"tableWidth": tableWidth,
	"lower":      strings.ToLower,
	"ago":        ago,
	"quiet":      quiet,
	"utc":        func(t time.Time) string { return t.UTC().Format("2006-01-02 15:04:05 UTC") },
	"first":      func(n int, s []stat) []stat { return s[:min(n, len(s))] },
	"maxN":       func() int { return maxWindows },
}).Parse(`<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<meta http-equiv="refresh" content="300">
<title>{{if .Device}}{{.Short}} · {{end}}Mono diagnostics</title>
<style>
/* Catppuccin Mocha (https://catppuccin.com/palette): base, mantle, surface0,
   text, subtext0 and blue, with the accent colors for the chart lines. */
:root {
  color-scheme: dark;
  --bg: #1e1e2e; --surface: #181825; --text: #cdd6f4; --muted: #a6adc8;
  --line: #313244; --accent: #89b4fa; --accent-text: #11111b; --hover: #313244;
}
* { box-sizing: border-box; }
body { margin: 0; background: var(--bg); color: var(--text);
  font: 15px/1.45 system-ui, -apple-system, "Segoe UI", sans-serif; }
/* A centered 1068 px column with 16 px gutters; .full spans the whole window. */
main { display: grid; grid-template-columns: minmax(16px, 1fr) minmax(0, 1068px) minmax(16px, 1fr);
  padding: 24px 0 48px; }
main > * { grid-column: 2; min-width: 0; }
main > .full { grid-column: 1 / -1; margin: 0 16px; }
h1 { font-size: 1.5rem; margin: 0; }
h2 { font-size: 1rem; margin: 32px 0 12px; color: var(--muted); font-weight: 600;
  text-transform: uppercase; letter-spacing: .04em; }
h3 { font-size: .85rem; margin: 16px 0 8px; color: var(--muted); font-weight: 600; }
.mono { font-family: ui-monospace, "SFMono-Regular", Menlo, Consolas, monospace; }
.muted { color: var(--muted); }
header { display: flex; gap: 16px; align-items: center; justify-content: space-between;
  flex-wrap: wrap; }
header p { margin: 4px 0 0; }
.note { margin: 8px 0 0; font-size: .85rem; }
.note + .note { margin-top: 2px; }
.button { display: inline-block; padding: 8px 14px; border-radius: 8px; background: var(--accent);
  color: var(--accent-text); text-decoration: none; font-weight: 600; }
.button:hover { filter: brightness(1.1); }
.tiles { display: grid; grid-template-columns: repeat(auto-fill, minmax(160px, 1fr)); gap: 12px; }
.tile { background: var(--surface); border: 1px solid var(--line); border-radius: 10px; padding: 12px 14px; }
.tile .label { display: block; font-size: .8rem; color: var(--muted); }
.tile .value { display: block; font-size: 1.5rem; font-weight: 650; font-variant-numeric: tabular-nums; }
.tile .sub { font-size: .8rem; color: var(--muted); }
.grid { display: grid; grid-template-columns: repeat(auto-fill, minmax(200px, 1fr)); gap: 12px; }
.card { display: block; background: var(--surface); border: 1px solid var(--line); border-radius: 10px;
  padding: 12px 14px; color: inherit; text-decoration: none; }
.card:hover { background: var(--hover); border-color: var(--accent); }
.card.quiet { opacity: .55; }
.card .id { font-size: 1.15rem; font-weight: 650; }
.card dl { display: grid; grid-template-columns: auto 1fr; gap: 2px 12px; margin: 8px 0; }
.card dt { color: var(--muted); font-size: .85rem; }
.card dd { margin: 0; text-align: right; font-variant-numeric: tabular-nums; }
.card .seen { font-size: .8rem; color: var(--muted); }
.chart { position: relative; background: var(--surface); border: 1px solid var(--line); border-radius: 10px;
  padding: 8px; }
.chart svg { display: block; width: 100%; height: auto; touch-action: pan-y; }
.chart .grid { stroke: var(--line); stroke-width: 1; }
.chart text { fill: var(--muted); font-size: 11px; }
.chart polyline { fill: none; stroke: var(--c, var(--accent)); stroke-width: 2; stroke-linejoin: round; stroke-linecap: round; }
.chart.multi polyline { stroke-width: 1.5; }
.chart polyline:hover { stroke-width: 3; }
.chart circle { fill: var(--c, var(--accent)); }
.chart .guide { stroke: var(--muted); stroke-width: 1; stroke-dasharray: 3 3; }
.chart .mark { stroke: var(--surface); stroke-width: 1.5; }
.chart .guide, .chart .mark, .chart .tip { display: none; }
.chart.hover .guide, .chart.hover .mark, .chart.hover .tip { display: block; }
.tip { position: absolute; z-index: 3; min-width: 170px; padding: 6px 8px; pointer-events: none;
  background: var(--surface); border: 1px solid var(--line); border-radius: 8px;
  box-shadow: 0 4px 14px rgb(0 0 0 / .18); font-size: .8rem; font-variant-numeric: tabular-nums; }
.tip-head { font-weight: 600; margin-bottom: 2px; }
.tip-row { display: flex; gap: 12px; justify-content: space-between; align-items: center; }
.tip-row span:first-child { font-family: ui-monospace, "SFMono-Regular", Menlo, Consolas, monospace; }
.tip-row span:first-child::before { content: ""; display: inline-block; width: 8px; height: 8px;
  margin-right: 6px; border-radius: 50%; background: var(--c, var(--accent)); }
.s0 { --c: #89b4fa; } .s1 { --c: #fab387; } .s2 { --c: #f38ba8; } .s3 { --c: #94e2d5; }
.s4 { --c: #a6e3a1; } .s5 { --c: #f9e2af; } .s6 { --c: #cba6f7; } .s7 { --c: #f5c2e7; }
.s8 { --c: #f2cdcd; } .s9 { --c: #9399b2; } .s10 { --c: #74c7ec; } .s11 { --c: #b4befe; }
.s12 { --c: #89dceb; } .s13 { --c: #eba0ac; }
.legend { display: flex; flex-wrap: wrap; gap: 4px 16px; margin: 8px 4px 0; padding: 0; list-style: none;
  font-size: .8rem; }
.legend li::before { content: ""; display: inline-block; width: 12px; height: 3px; margin-right: 6px;
  vertical-align: middle; background: var(--c); }
.table-wrap { overflow: auto; max-height: 480px; background: var(--surface); border: 1px solid var(--line);
  border-radius: 10px; }
/* Fixed layout: the Received column is 7.5em and the value columns share the
   rest equally, each at least 4.75em (the table's style sets its min-width). */
table { width: 100%; table-layout: fixed; border-collapse: separate; border-spacing: 0; font-size: .8rem;
  font-variant-numeric: tabular-nums; }
th, td { padding: 4px 6px; text-align: center; white-space: nowrap; border-bottom: 1px solid var(--line); }
thead th { white-space: normal; vertical-align: bottom; }
th { font-weight: 600; color: var(--muted); }
thead th { position: sticky; top: 0; z-index: 1; background: var(--surface); font-weight: 700; color: var(--text); }
th:first-child, td:first-child { position: sticky; left: 0; background: var(--surface); }
thead th:first-child { z-index: 2; width: 7.5em; }
.empty { padding: 24px; text-align: center; background: var(--surface); border: 1px dashed var(--line);
  border-radius: 10px; color: var(--muted); }
</style>
</head>
<body>
<main>
{{- if .Device}}
<header>
  <div>
    <h1>Viewing <span class="mono">{{.Short}}</span></h1>
    <p class="mono muted">{{.Device}}</p>
  </div>
  <a class="button" href="/">← All devices</a>
</header>
{{- else}}
<header><h1>Mono diagnostics</h1></header>
{{- end}}
<p class="note muted">{{if .Device}}Each temperature is the hottest sensor on this device.{{else}}Each temperature is the hottest sensor on a device. Averages are across devices.{{end}}</p>
<p class="note muted">Times in UTC, as received by the collector. The page reloads every 5 minutes.</p>

<h2>{{if .Device}}Stats{{else}}Combined stats{{end}}</h2>
<div class="tiles">
{{- if .Device}}
  <div class="tile"><span class="label">Readings, last 24 h</span><span class="value">{{.Sum.Readings24h}}</span></div>
  <div class="tile" title="{{utc .Sum.LastSeen}}"><span class="label">Last reading</span><span class="value">{{ago .Sum.LastSeen .Now}}</span></div>
{{- else}}
  <div class="tile"><span class="label">Devices, last 24 h</span><span class="value">{{.Sum.Devices24h}}</span><span class="sub">of {{.Sum.Devices}} in total</span></div>
{{- range $i, $w := .Windows}}
  <div class="tile"><span class="label">Average max, {{lower $w.Label}}</span><span class="value">{{temp (index $.Sum.Avg $i)}}</span></div>
{{- end}}
{{- end}}
</div>

{{- if .Device}}

<h3>Maximum temperature</h3>
<div class="tiles">
{{- range $i, $w := .Windows}}
  <div class="tile"><span class="label">{{$w.Label}}</span><span class="value">{{temp (index $.Sum.Max $i)}}</span></div>
{{- end}}
</div>

<h3>Hourly maximum, last 7 days</h3>
{{- with .Chart}}{{template "chart" .}}{{else}}
<div class="empty">No temperatures in the last 7 days.</div>
{{- end}}

<h3>All temperature sensors, hourly maximum, last 7 days</h3>
{{- with .SensorChart}}{{template "chart" .}}{{else}}
<div class="empty">No temperatures in the last 7 days.</div>
{{- end}}

<h3>All readings, last 7 days ({{len .Rows}})</h3>
{{- if .Rows}}
<div class="table-wrap full">
<table style="{{tableWidth (len .Keys)}}">
<thead><tr><th>Received</th>{{range .Keys}}<th class="mono" title="{{.Name}}">{{column .Name}}</th>{{end}}</tr></thead>
<tbody>
{{- range .Rows}}
<tr><th title="{{utc .At}}">{{.At.UTC.Format "Jan 2 15:04"}}</th>{{range $i, $v := .Values}}<td>{{cell (index $.Keys $i) $v}}</td>{{end}}</tr>
{{- end}}
</tbody>
</table>
</div>
{{- else}}
<div class="empty">No readings in the last 7 days.</div>
{{- end}}
{{- else}}

<h2>Devices</h2>
{{- if .Cards}}
<div class="grid">
{{- range .Cards}}
  <a class="card{{if quiet .Sum.LastSeen $.Now}} quiet{{end}}" href="/?device={{.ID}}" title="{{.ID}}">
    <span class="id mono">{{.Short}}</span>
    <dl>
    {{- range $i, $m := first maxN .Sum.Max}}
      <dt>Max {{(index $.Windows $i).Label}}</dt><dd>{{temp $m}}</dd>
    {{- end}}
    </dl>
    <span class="seen" title="{{utc .Sum.LastSeen}}">Last reading {{ago .Sum.LastSeen $.Now}}</span>
  </a>
{{- end}}
</div>
{{- else}}
<div class="empty">No readings yet.</div>
{{- end}}
{{- end}}
{{- if .Device}}
<script>
// Chart hover: under the pointer, a guide line, a dot on each line and a box
// with the hour and each value, hottest first. The values come from the
// chart's JSON; the box is built with textContent only.
(function () {
  var months = ["Jan", "Feb", "Mar", "Apr", "May", "Jun", "Jul", "Aug", "Sep", "Oct", "Nov", "Dec"];
  var ns = "http://www.w3.org/2000/svg";
  document.querySelectorAll(".chart").forEach(function (box) {
    var src = box.querySelector("script.chart-data");
    var svg = box.querySelector("svg");
    if (!src || !svg) return;
    var d = JSON.parse(src.textContent);
    var n = d.series[0].values.length;
    var step = (d.right - d.left) / (n - 1);

    var guide = document.createElementNS(ns, "line");
    guide.setAttribute("class", "guide");
    guide.setAttribute("y1", d.top);
    guide.setAttribute("y2", d.bottom);
    svg.appendChild(guide);
    var marks = d.series.map(function (s) {
      var c = document.createElementNS(ns, "circle");
      c.setAttribute("class", ("mark " + s.class).trim());
      c.setAttribute("r", 4);
      svg.appendChild(c);
      return c;
    });
    var tip = document.createElement("div");
    tip.className = "tip";
    box.appendChild(tip);

    function hide() { box.classList.remove("hover"); }

    svg.addEventListener("pointermove", function (e) {
      var pt = svg.createSVGPoint();
      pt.x = e.clientX;
      pt.y = e.clientY;
      var p = pt.matrixTransform(svg.getScreenCTM().inverse());
      var i = Math.round((p.x - d.left) / step);
      if (i < 0 || i >= n) { hide(); return; }
      var x = d.left + i * step;

      var rows = [];
      d.series.forEach(function (s, k) {
        var v = s.values[i];
        marks[k].style.display = v === null ? "none" : "";
        if (v === null) return;
        marks[k].setAttribute("cx", x);
        marks[k].setAttribute("cy", d.bottom - (v - d.lo) / (d.hi - d.lo) * (d.bottom - d.top));
        rows.push({ s: s, v: v });
      });
      if (!rows.length) { hide(); return; }
      rows.sort(function (a, b) { return b.v - a.v; });
      guide.setAttribute("x1", x);
      guide.setAttribute("x2", x);

      var t = new Date((d.start + i * 3600) * 1000);
      tip.textContent = "";
      var head = document.createElement("div");
      head.className = "tip-head";
      head.textContent = months[t.getUTCMonth()] + " " + t.getUTCDate() + " " +
        String(t.getUTCHours()).padStart(2, "0") + ":00 UTC";
      tip.appendChild(head);
      rows.forEach(function (r) {
        var row = document.createElement("div");
        row.className = ("tip-row " + r.s.class).trim();
        var name = document.createElement("span");
        name.textContent = r.s.name;
        var value = document.createElement("span");
        value.textContent = r.v.toFixed(1) + " °C";
        row.append(name, value);
        tip.appendChild(row);
      });

      box.classList.add("hover");
      var b = box.getBoundingClientRect();
      var left = e.clientX - b.left + 16;
      if (left + tip.offsetWidth > b.width) left = e.clientX - b.left - 16 - tip.offsetWidth;
      var top = e.clientY - b.top - tip.offsetHeight / 2;
      tip.style.left = Math.max(0, left) + "px";
      tip.style.top = Math.max(0, Math.min(top, b.height - tip.offsetHeight)) + "px";
    });
    svg.addEventListener("pointerleave", hide);
  });
})();
</script>
{{- end}}
</main>
</body>
</html>
{{- define "chart"}}
<div class="chart{{if .Legend}} multi{{end}}">
<svg viewBox="0 0 {{.W}} {{.H}}" role="img" aria-label="Temperature per hour over the last 7 days">
{{- $c := .}}
{{- range .YTicks}}
  <line class="grid" x1="{{$c.Left}}" x2="{{$c.Right}}" y1="{{.Pos}}" y2="{{.Pos}}"/>
  <text x="{{$c.Left}}" y="{{.Pos}}" dx="-6" dy="4" text-anchor="end">{{.Label}}</text>
{{- end}}
{{- range .XTicks}}
  <line class="grid" x1="{{.Pos}}" x2="{{.Pos}}" y1="{{$c.Top}}" y2="{{$c.Bottom}}"/>
  <text x="{{.Pos}}" y="{{$c.Bottom}}" dy="18" text-anchor="middle">{{.Label}}</text>
{{- end}}
{{- range .Series}}
  <g{{if .Class}} class="{{.Class}}"{{end}}>
  {{- range .Lines}}
    <polyline points="{{.}}"/>
  {{- end}}
  {{- range .Dots}}
    <circle cx="{{.X}}" cy="{{.Y}}" r="3"/>
  {{- end}}
  </g>
{{- end}}
</svg>
<script type="application/json" class="chart-data">{{.Data}}</script>
{{- if .Legend}}
<ul class="legend">
{{- range .Series}}
  <li class="{{.Class}} mono">{{.Name}}</li>
{{- end}}
</ul>
{{- end}}
</div>
{{- end}}
`))
