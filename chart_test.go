package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"
	"time"
)

// A 7-day chart span, from Oct 29 13:00 to Nov 5 13:00 UTC.
var firstHour = time.Date(2026, 10, 29, 13, 0, 0, 0, time.UTC)

var lastHour = firstHour.Add(chartSpan)

// TestTimeline: every reading in the 7 days is a point, also several in one
// hour (the DK sent at 12:00, 12:15 and 12:30 and the chart showed one point).
func TestTimeline(t *testing.T) {
	s := newStore()
	at := func(month time.Month, day, hour, minute, second int) time.Time {
		return time.Date(2026, month, day, hour, minute, second, 0, time.UTC)
	}
	s.add(at(11, 5, 12, 30, 25), reading("d", 51))
	s.add(at(11, 5, 12, 0, 25), reading("d", 50))
	s.add(at(11, 5, 12, 15, 25), reading("d", 52))
	s.add(at(11, 5, 11, 30, 0), reading("d", "null"))
	s.add(at(11, 5, 12, 10, 0), reading("other", 70))

	from, to, times, hottest, sensors := s.timeline("d", at(11, 5, 12, 31, 37))
	// Less than 7 days of readings: the span runs from the first one to now.
	if !from.Equal(at(11, 5, 11, 30, 0)) || !to.Equal(at(11, 5, 12, 31, 37)) {
		t.Errorf("span = %v .. %v, want 11:30:00 .. 12:31:37", from, to)
	}
	if len(times) != 4 || !times[0].Equal(at(11, 5, 11, 30, 0)) || !times[3].Equal(at(11, 5, 12, 30, 25)) {
		t.Fatalf("times = %v, want 4 from 11:30:00 to 12:30:25", times)
	}
	wantStatsN(t, "hottest", hottest, []any{nil, 50.0, 52.0, 51.0})

	// Temperature keys with a value, in record order; the CPU reads 5 lower.
	// tmp_u58001_remote_c is not stored and fan1_rpm is not a temperature.
	if len(sensors) != 2 || sensors[0].Name != "ls1046a_a72_cluster_c" || sensors[1].Name != "gpy_u12000_c" {
		t.Fatalf("sensors = %+v", sensors)
	}
	wantStatsN(t, "cpu", sensors[0].Values, []any{nil, 45.0, 47.0, 46.0})

	c := newChart(from, to, times, []series{{"Hottest sensor", hottest}}, time.UTC)
	// The 3 readings spread over the chart; the newest is near the right edge.
	if got := c.Series[0].Lines; len(got) != 1 || got[0] != "433,212 622.9,132 812.8,172" || len(c.Series[0].Dots) != 0 {
		t.Errorf("hottest = %+v, want one line through the 3 readings", c.Series[0])
	}
	var labels []string
	for _, tick := range c.XTicks {
		labels = append(labels, tick.Label)
	}
	if got := strings.Join(labels, " "); got != "11:30 11:45 12:00 12:15 12:30" {
		t.Errorf("x ticks = %q", got)
	}

	// With a reading older than 7 days, the span is the 7 days.
	s.add(at(10, 29, 12, 0, 0), reading("d", 99))
	if from, _, _, _, _ := s.timeline("d", at(11, 5, 12, 31, 37)); !from.Equal(at(10, 29, 12, 31, 37)) {
		t.Errorf("from = %v, want 7 days before now", from)
	}

	if _, _, times, _, _ := s.timeline("d", at(11, 5, 12, 31, 37)); len(times) != 4 {
		t.Errorf("times = %v, want the reading older than 7 days left out", times)
	}

	// One reading: the span is the 30 minutes up to now.
	one := newStore()
	one.add(at(11, 5, 12, 30, 25), reading("d", 51))
	if from, to, _, _, _ := one.timeline("d", at(11, 5, 12, 31, 37)); to.Sub(from) != chartMin {
		t.Errorf("one reading: span = %v, want %v", to.Sub(from), chartMin)
	}
}

// every15 returns n reading times 15 minutes apart from the charts' start.
func every15(n int) []time.Time {
	times := make([]time.Time, n)
	for i := range times {
		times[i] = firstHour.Add(time.Duration(i) * 15 * time.Minute)
	}
	return times
}

// values returns one stat per argument; nil is no value.
func values(vs ...any) []stat {
	out := make([]stat, len(vs))
	for i, v := range vs {
		if v != nil {
			out[i] = stat{v.(float64), true}
		}
	}
	return out
}

func TestChart(t *testing.T) {
	times := []time.Time{firstHour, firstHour.Add(167 * time.Hour), firstHour.Add(167*time.Hour + 15*time.Minute)}
	c := newChart(firstHour, lastHour, times, []series{{"Hottest", values(44.0, 52.0, 47.0)}}, time.UTC)
	if c == nil || len(c.Series) != 1 || c.Legend() {
		t.Fatalf("chart = %+v, want one series and no legend", c)
	}
	s := c.Series[0]
	if s.Class != "" {
		t.Errorf("class = %q, want none for one series", s.Class)
	}
	// Each reading sits at its time; 167 hours of gap leave the first alone.
	if len(s.Lines) != 1 || s.Lines[0] != "823.4,52 824.5,118.7" {
		t.Errorf("lines = %q, want one line over the last 2 readings", s.Lines)
	}
	if len(s.Dots) != 1 || s.Dots[0] != (chartDot{chartLeft, 158.7}) {
		t.Errorf("dots = %+v, want one at (%v, 158.7)", s.Dots, chartLeft)
	}

	d := c.Data
	if d.Start != firstHour.Unix() || d.End != firstHour.Add(chartSpan).Unix() || d.Lo != 40 || d.Hi != 55 ||
		d.Left != chartLeft || d.Bottom != chartBot {
		t.Errorf("data = %+v", d)
	}
	if len(d.Times) != 3 || d.Times[2] != times[2].Unix() || *d.Series[0].Values[1] != 52 {
		t.Errorf("data times = %v, values = %v", d.Times, d.Series[0].Values)
	}

	var ys []string
	for _, tick := range c.YTicks {
		ys = append(ys, tick.Label)
	}
	if got := strings.Join(ys, " "); got != "40 °C 45 °C 50 °C 55 °C" {
		t.Errorf("y ticks = %q", got)
	}
	if c.YTicks[0].Pos != chartBot || c.YTicks[3].Pos != chartTop {
		t.Errorf("y ticks at %v .. %v, want %v .. %v", c.YTicks[0].Pos, c.YTicks[3].Pos, chartBot, chartTop)
	}
	if len(c.XTicks) != 7 || c.XTicks[0].Label != "Oct 30" || c.XTicks[6].Label != "Nov 5" {
		t.Errorf("x ticks = %+v, want Oct 30 .. Nov 5", c.XTicks)
	}
}

// TestChartGaps: a line runs through readings up to 45 minutes apart; a longer
// gap or a missing value breaks it, and a reading alone gets a dot.
func TestChartGaps(t *testing.T) {
	m := func(minutes int) time.Time { return firstHour.Add(time.Duration(minutes) * time.Minute) }
	times := []time.Time{m(0), m(15), m(60), m(106), m(121), m(136), m(151)}
	c := newChart(firstHour, lastHour, times, []series{{"x", values(40.0, 41.0, 42.0, 43.0, nil, 45.0, 46.0)}}, time.UTC)
	s := c.Series[0]
	if len(s.Lines) != 2 || strings.Count(s.Lines[0], ",") != 3 || strings.Count(s.Lines[1], ",") != 2 {
		t.Errorf("lines = %q, want 3 points (gap 45 min), then 2 after the missing value", s.Lines)
	}
	if len(s.Dots) != 1 || s.Dots[0] != (chartDot{56.2, 152}) { // 43 °C at 106 min, after a 46-minute gap
		t.Errorf("dots = %+v, want the 43 °C reading alone", s.Dots)
	}
}

func TestChartScale(t *testing.T) {
	for _, tc := range []struct {
		values []stat
		want   string
	}{
		{values(20.0, 95.0), "20 °C 40 °C 60 °C 80 °C 100 °C"},
		{values(45.0, nil), "45 °C 50 °C"},
		{values(-3.0, 4.0), "-5 °C 0 °C 5 °C"},
	} {
		var ys []string
		for _, tick := range newChart(firstHour, lastHour, every15(2), []series{{"x", tc.values}}, time.UTC).YTicks {
			ys = append(ys, tick.Label)
		}
		if got := strings.Join(ys, " "); got != tc.want {
			t.Errorf("%v: y ticks = %q, want %q", tc.values, got, tc.want)
		}
	}
}

func TestChartEmpty(t *testing.T) {
	if c := newChart(firstHour, lastHour, every15(2), []series{{"x", values(nil, nil)}}, time.UTC); c != nil {
		t.Errorf("chart = %+v, want nil", c)
	}
	if c := newChart(firstHour, lastHour, nil, nil, time.UTC); c != nil {
		t.Errorf("chart with no readings = %+v, want nil", c)
	}
}

// TestChartSeries: several series share one y axis and get a color class
// each; only the values no line shows get a dot.
func TestChartSeries(t *testing.T) {
	c := newChart(firstHour, lastHour, every15(4), []series{
		{"cpu_c", values(45.0, 46.0, nil, 47.0)},
		{"sfp_c", values(61.0, 62.0, nil, nil)},
	}, time.UTC)
	if c == nil || len(c.Series) != 2 || !c.Legend() {
		t.Fatalf("chart = %+v, want 2 series with a legend", c)
	}
	if c.Series[0].Class != "s0" || c.Series[1].Class != "s1" {
		t.Errorf("classes = %q, %q", c.Series[0].Class, c.Series[1].Class)
	}
	if c.YTicks[0].Label != "45 °C" || c.YTicks[len(c.YTicks)-1].Label != "65 °C" {
		t.Errorf("y ticks = %+v, want 45 .. 65 °C", c.YTicks)
	}
	cpu, sfp := c.Series[0], c.Series[1]
	if len(cpu.Lines) != 1 || len(cpu.Dots) != 1 || cpu.Dots[0].X != 51.5 {
		t.Errorf("cpu = %+v, want one line and one dot at 45 minutes", cpu)
	}
	if d := c.Data.Series[1]; d.Name != "sfp_c" || d.Class != "s1" || len(d.Values) != 4 || d.Values[2] != nil {
		t.Errorf("data series = %+v", d)
	}
	if len(sfp.Lines) != 1 || len(sfp.Dots) != 0 {
		t.Errorf("sfp = %+v, want one line and no dots", sfp)
	}
}

func TestDashboardChart(t *testing.T) {
	s := newStore()
	s.add(testNow.Add(-45*time.Minute), reading("e8:f6:d7:00:17:df", 50))
	s.add(testNow.Add(-30*time.Minute), reading("e8:f6:d7:00:17:df", 52))
	s.add(testNow.Add(-10*24*time.Hour), reading("aa:bb:cc:00:00:01", 50))

	code, body := get(t, s, "/?device=e8:f6:d7:00:17:df")
	if code != http.StatusOK {
		t.Fatalf("code = %d", code)
	}
	mustContain(t, body, "Hottest sensor, last 7 days", `<svg viewBox="0 0 840 240"`,
		`<polyline points="48,`, "Each temperature is the hottest sensor on this device.",
		"All temperature sensors, last 7 days", `<ul class="legend">`, `<li class="s0 mono">cpu</li>`,
		`<li class="s1 mono">phy2</li>`,
		`<th class="mono" title="ls1046a_a72_cluster_c">cpu</th>`,
		`<th class="mono" title="gpy_u12000_c">phy2</th>`,
		">11:15</text>", ">12:00</text>")
	if strings.Contains(body, "tmp1_") || strings.Contains(body, "Hourly") {
		t.Error("page shows the unconnected tmp1_remote sensor or an hourly heading")
	}

	_, body = get(t, s, "/?device=aa:bb:cc:00:00:01")
	mustContain(t, body, "No temperatures in the last 7 days.")

	if _, body = get(t, s, "/"); strings.Contains(body, "<svg") {
		t.Error("overview shows the chart")
	}
}

// TestChartData: each chart carries its hover data as JSON that parses back.
func TestChartData(t *testing.T) {
	s := newStore()
	s.add(testNow.Add(-30*time.Minute), reading("d", 52))
	_, body := get(t, s, "/?device=d")

	blocks := regexp.MustCompile(`(?s)<script type="application/json" class="chart-data">(.*?)</script>`).FindAllStringSubmatch(body, -1)
	if len(blocks) != 2 {
		t.Fatalf("%d chart data blocks, want 2", len(blocks))
	}
	var d chartData
	if err := json.Unmarshal([]byte(blocks[1][1]), &d); err != nil {
		t.Fatalf("sensor chart data: %v\n%s", err, blocks[1][1])
	}
	var names []string
	for _, s := range d.Series {
		names = append(names, s.Name)
	}
	if got := strings.Join(names, " "); got != "cpu phy2" {
		t.Errorf("series = %q", got)
	}
	if len(d.Times) != 1 || d.Times[0] != testNow.Add(-30*time.Minute).Unix() || d.Labels[0] != "Nov 5 11:30 UTC" {
		t.Errorf("times = %v, labels = %q", d.Times, d.Labels)
	}
	if v := d.Series[1].Values[0]; v == nil || *v != 52 {
		t.Errorf("gpy = %v, want 52", v)
	}
	mustContain(t, body, `svg.addEventListener("pointermove"`)
}

func TestDisplayName(t *testing.T) {
	for key, want := range map[string]string{
		"ls1046a_a72_cluster_c":    "cpu",
		"ls1046a_ddr_controller_c": "ddr_ctrl",
		"ls1046a_serdes_c":         "serdes",
		"tmp_u58000_local_c":       "tmp0_local",
		"tmp_u58001_remote_c":      "tmp1_remote",
		"gpy_u11000_c":             "phy1",
		"gpy_u12000_c":             "phy2",
		"gpy_u13000_c":             "phy3",
		"sfp_p16000_c":             "sfp1",
		"sfp_p17000_c":             "sfp2",
		"other_c":                  "other",
		"fan1_rpm":                 "fan1_rpm",
		"usb_pd_w":                 "usb_pd_w",
	} {
		if got := displayName(key); got != want {
			t.Errorf("displayName(%q) = %q, want %q", key, got, want)
		}
	}
}

func TestTimeTicks(t *testing.T) {
	x := func(time.Time) float64 { return 0 }
	for _, tc := range []struct {
		span time.Duration
		want string
	}{
		{time.Hour, "Nov 4 00:15 00:30 00:45 01:00"},
		{5 * time.Hour, "Nov 4 01:00 02:00 03:00 04:00 05:00"},
		{2 * 24 * time.Hour, "Nov 4 06:00 12:00 18:00 Nov 5 06:00 12:00 18:00 Nov 6"},
		{chartSpan, ""}, // checked in TestChart: one label per day
	} {
		from := time.Date(2026, 11, 4, 0, 0, 0, 0, time.UTC)
		var labels []string
		for _, tick := range timeTicks(from, from.Add(tc.span), time.UTC, x) {
			labels = append(labels, tick.Label)
		}
		if got := strings.Join(labels, " "); tc.want != "" && got != tc.want {
			t.Errorf("%v: ticks = %q, want %q", tc.span, got, tc.want)
		}
	}
}

// TestTimeZone: the page shows every time in the collector's time zone: the
// note, the table, the axis and the hover labels.
func TestTimeZone(t *testing.T) {
	cet := time.FixedZone("CET", 3600)
	s := newStore()
	s.add(testNow.Add(-45*time.Minute), reading("d", 50)) // 11:15 UTC, 12:15 CET
	s.add(testNow.Add(-30*time.Minute), reading("d", 52))

	rec := httptest.NewRecorder()
	dashboard(s, func() time.Time { return testNow }, cet).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/?device=d", nil))
	body := rec.Body.String()
	mustContain(t, body, "Times in CET, as received by the collector.",
		`<th title="2026-11-05 12:30:00 CET">Nov 5 12:30</th>`,
		">12:15</text>", ">13:00</text>", `"labels":["Nov 5 12:15 CET","Nov 5 12:30 CET"]`)
	if strings.Contains(body, "UTC") {
		t.Error("page still shows UTC")
	}

	// Axis labels start from midnight in the zone, not in UTC.
	from := time.Date(2026, 11, 4, 0, 0, 0, 0, time.UTC) // 01:00 CET
	var labels []string
	for _, tick := range timeTicks(from, from.Add(48*time.Hour), cet, func(time.Time) float64 { return 0 }) {
		labels = append(labels, tick.Label)
	}
	if got := strings.Join(labels, " "); got != "06:00 12:00 18:00 Nov 5 06:00 12:00 18:00 Nov 6" {
		t.Errorf("CET ticks = %q", got)
	}
}
