package main

import (
	"encoding/json"
	"net/http"
	"regexp"
	"strings"
	"testing"
	"time"
)

// With testNow at Nov 5 12:00 UTC, the chart's first hour starts at Oct 29
// 13:00 and the last (167) is Nov 5 12:00.
var firstHour = time.Date(2026, 10, 29, 13, 0, 0, 0, time.UTC)

func TestHourly(t *testing.T) {
	s := newStore()
	at := func(day, hour, minute int) time.Time {
		return time.Date(2026, time.Month(10+day/100), day%100, hour, minute, 0, 0, time.UTC)
	}
	s.add(at(105, 11, 10), reading("d", 50))
	s.add(at(105, 11, 40), reading("d", 52))
	s.add(at(105, 12, 0), reading("d", 47))
	s.add(at(105, 9, 30), reading("d", "null"))
	s.add(at(29, 12, 59), reading("d", 99)) // before the first hour
	s.add(at(29, 13, 0), reading("d", 44))
	s.add(at(105, 11, 30), reading("other", 70))

	start, hours := s.hourly("d", testNow)
	if !start.Equal(firstHour) || len(hours) != chartHours {
		t.Fatalf("start = %v, %d hours; want %v, %d", start, len(hours), firstHour, chartHours)
	}
	want := map[int]float64{0: 44, 166: 52, 167: 47}
	for i, h := range hours {
		if w, found := want[i]; found != h.OK || (found && h.V != w) {
			t.Errorf("hour %d = %+v, want %v (set: %v)", i, h, w, found)
		}
	}
}

func hoursWith(values map[int]float64) []stat {
	hours := make([]stat, chartHours)
	for i, v := range values {
		hours[i] = stat{v, true}
	}
	return hours
}

func TestChart(t *testing.T) {
	c := newChart(firstHour, []series{{"Hottest", hoursWith(map[int]float64{0: 44, 166: 52, 167: 47})}})
	if c == nil || len(c.Series) != 1 || c.Legend() {
		t.Fatalf("chart = %+v, want one series and no legend", c)
	}
	s := c.Series[0]
	if s.Class != "" {
		t.Errorf("class = %q, want none for one series", s.Class)
	}
	if len(s.Lines) != 1 || s.Lines[0] != "823.3,52 828,118.7" {
		t.Errorf("lines = %q, want one line over hours 166 and 167", s.Lines)
	}
	// Only hour 0 has no neighbor, so only it gets a dot.
	if len(s.Dots) != 1 || s.Dots[0] != (chartDot{chartLeft, 158.7}) {
		t.Errorf("dots = %+v, want one at (%v, 158.7)", s.Dots, chartLeft)
	}

	// The hover data has every hour, null where there is no value.
	d := c.Data
	if d.Start != firstHour.Unix() || d.Lo != 40 || d.Hi != 55 || d.Left != chartLeft || d.Bottom != chartBot {
		t.Errorf("data = %+v", d)
	}
	if v := d.Series[0].Values; len(v) != chartHours || *v[0] != 44 || v[1] != nil || *v[166] != 52 {
		t.Errorf("values = %v", v)
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

func TestChartScale(t *testing.T) {
	for _, tc := range []struct {
		values map[int]float64
		want   string
	}{
		{map[int]float64{0: 20, 1: 95}, "20 °C 40 °C 60 °C 80 °C 100 °C"},
		{map[int]float64{5: 45}, "45 °C 50 °C"},
		{map[int]float64{5: -3, 6: 4}, "-5 °C 0 °C 5 °C"},
	} {
		var ys []string
		for _, tick := range newChart(firstHour, []series{{"x", hoursWith(tc.values)}}).YTicks {
			ys = append(ys, tick.Label)
		}
		if got := strings.Join(ys, " "); got != tc.want {
			t.Errorf("%v: y ticks = %q, want %q", tc.values, got, tc.want)
		}
	}
}

func TestChartEmpty(t *testing.T) {
	if c := newChart(firstHour, []series{{"x", make([]stat, chartHours)}, {"y", nil}}); c != nil {
		t.Errorf("chart = %+v, want nil", c)
	}
}

// TestChartSeries: several series share one y axis and get a color class
// each; only the values no line shows get a dot.
func TestChartSeries(t *testing.T) {
	c := newChart(firstHour, []series{
		{"cpu_c", hoursWith(map[int]float64{10: 45, 11: 46, 20: 47})},
		{"sfp_c", hoursWith(map[int]float64{10: 61, 11: 62})},
	})
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
	if len(cpu.Lines) != 1 || len(cpu.Dots) != 1 || cpu.Dots[0].X != round1(chartLeft+20*(chartRight-chartLeft)/(chartHours-1)) {
		t.Errorf("cpu = %+v, want one line and one dot for hour 20", cpu)
	}
	if d := c.Data.Series[1]; d.Name != "sfp_c" || d.Class != "s1" {
		t.Errorf("data series = %+v", d)
	}
	if len(sfp.Lines) != 1 || len(sfp.Dots) != 0 {
		t.Errorf("sfp = %+v, want one line and no dots", sfp)
	}
}

func TestSensorHours(t *testing.T) {
	s := newStore()
	s.add(testNow.Add(-50*time.Minute), []byte(`{"deviceId":"d","cpu_c":45,"sfp_c":null,"fan1_rpm":2556}`))
	s.add(testNow.Add(-20*time.Minute), []byte(`{"deviceId":"d","cpu_c":47.5,"sfp_c":null,"fan1_rpm":2600}`))
	s.add(testNow.Add(-8*24*time.Hour), []byte(`{"deviceId":"d","old_c":40}`))
	s.add(testNow.Add(-20*time.Minute), []byte(`{"deviceId":"other","gpy_c":70}`))

	start, ss := s.sensorHours("d", testNow)
	if !start.Equal(firstHour) {
		t.Errorf("start = %v, want %v", start, firstHour)
	}
	// Only temperature keys with a value in the 7 days, in record order.
	if len(ss) != 1 || ss[0].Name != "cpu_c" {
		t.Fatalf("series = %+v, want cpu_c only", ss)
	}
	if h := ss[0].Hours[166]; !h.OK || h.V != 47.5 {
		t.Errorf("cpu_c hour 166 = %+v, want 47.5", h)
	}
}

func TestDashboardChart(t *testing.T) {
	s := newStore()
	s.add(testNow.Add(-90*time.Minute), reading("e8:f6:d7:00:17:df", 50))
	s.add(testNow.Add(-30*time.Minute), reading("e8:f6:d7:00:17:df", 52))
	s.add(testNow.Add(-10*24*time.Hour), reading("aa:bb:cc:00:00:01", 50))

	code, body := get(t, s, "/?device=e8:f6:d7:00:17:df")
	if code != http.StatusOK {
		t.Fatalf("code = %d", code)
	}
	mustContain(t, body, "Hourly maximum, last 7 days", `<svg viewBox="0 0 840 240"`,
		`<polyline points="818.7,`, "Each temperature is the hottest sensor on this device.",
		"All temperature sensors, hourly maximum", `<ul class="legend">`, `<li class="s0 mono">cpu</li>`,
		`<li class="s1 mono">phy2</li>`,
		`<th class="mono" title="ls1046a_a72_cluster_c">cpu</th>`,
		`<th class="mono" title="gpy_u12000_c">phy2</th>`,
		">Nov 5</text>")
	if strings.Contains(body, "tmp1_") {
		t.Error("page shows the unconnected tmp1_remote sensor")
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
	if v := d.Series[1].Values[166]; v == nil || *v != 52 {
		t.Errorf("gpy hour 166 = %v, want 52", v)
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
