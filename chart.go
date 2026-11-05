package main

import (
	"fmt"
	"math"
	"sort"
	"strings"
	"time"
)

// The device page's charts: the hottest sensor of each reading, and each
// temperature sensor, over the last 7 days, drawn as inline SVG. A small
// script on the page shows the values under the pointer from the chart's data.
const (
	chartHours  = 7 * 24
	chartSpan   = chartHours * time.Hour
	chartGap    = 45 * time.Minute // a longer gap between readings breaks a line
	chartMin    = 30 * time.Minute // the shortest time span a chart shows
	chartW      = 840
	chartH      = 240
	chartLeft   = 48.0 // room for the °C labels
	chartRight  = chartW - 12.0
	chartTop    = 12.0
	chartBot    = chartH - 28.0 // room for the day labels
	chartColors = 14            // series classes s0 .. s13 in the page's CSS
)

// chartStart returns the start of the charts' 7 days, which end now.
func chartStart(now time.Time) time.Time {
	return now.Add(-chartSpan)
}

// series is one line of a chart: a name and one value per reading.
type series struct {
	Name   string
	Values []stat
}

// timeline returns the device's readings in the charts' 7 days, oldest first:
// the time span the charts show, the reading times, the hottest sensor of each
// reading, and one series for each temperature key with a value in them. The
// span ends now and starts 7 days before; for a device with no older reading,
// it starts at the first reading, so that a few readings spread over the
// chart. It is at least chartMin long.
func (s *store) timeline(id string, now time.Time) (from, to time.Time, times []time.Time, hottest []stat, sensors []series) {
	start := chartStart(now)

	s.mu.RLock()
	defer s.mu.RUnlock()
	var points []point
	older := false
	for _, p := range s.devices[id] {
		older = older || p.at.Before(start)
		if !p.at.Before(start) && !p.at.After(now) {
			points = append(points, p)
		}
	}
	sort.SliceStable(points, func(i, j int) bool { return points[i].at.Before(points[j].at) })

	times = make([]time.Time, len(points))
	hottest = make([]stat, len(points))
	values := make([][]stat, len(s.keys))
	for i, p := range points {
		times[i] = p.at
		if p.ok {
			hottest[i] = stat{p.temp, true}
		}
		for k, key := range s.keys {
			v, found := p.value(k)
			if !found || !key.Temp() {
				continue
			}
			if values[k] == nil {
				values[k] = make([]stat, len(points))
			}
			values[k][i] = stat{v, true}
		}
	}

	for k, key := range s.keys {
		if values[k] != nil {
			sensors = append(sensors, series{key.Name, values[k]})
		}
	}
	from, to = start, now
	if !older && len(times) > 0 && times[0].After(start) {
		from = times[0]
	}
	if to.Sub(from) < chartMin {
		from = to.Add(-chartMin)
	}
	return from, to, times, hottest, sensors
}

// tickSteps are the x axis label steps; the chart uses the shortest that
// gives at most 8 steps over its span.
var tickSteps = []time.Duration{
	15 * time.Minute, 30 * time.Minute, time.Hour, 3 * time.Hour, 6 * time.Hour, 12 * time.Hour, 24 * time.Hour,
}

// timeTicks returns the x axis labels over from .. to in the time zone loc:
// "15:04" on whole steps from midnight, and "Jan 2" at midnight.
func timeTicks(from, to time.Time, loc *time.Location, x func(time.Time) float64) []chartTick {
	step := tickSteps[len(tickSteps)-1]
	for _, s := range tickSteps {
		if to.Sub(from)/s <= 8 {
			step = s
			break
		}
	}
	day := from.In(loc)
	var ticks []chartTick
	for at := time.Date(day.Year(), day.Month(), day.Day(), 0, 0, 0, 0, loc); !at.After(to); at = at.Add(step) {
		if at.Before(from) {
			continue
		}
		label := at.Format("15:04")
		if at.Hour() == 0 && at.Minute() == 0 {
			label = at.Format("Jan 2")
		}
		ticks = append(ticks, chartTick{x(at), label})
	}
	return ticks
}

type chartTick struct {
	Pos   float64
	Label string
}

// chartDot marks a value that no line shows: the reading before and the one
// after it have no value, or are more than chartGap away.
type chartDot struct {
	X, Y float64
}

// chartSeries is one series laid out. Lines has one "x,y x,y ..." list per run
// of readings with a value; a missing value or a gap breaks the line.
type chartSeries struct {
	Name  string
	Class string // "s0" .. "s13" with several series; empty with one
	Lines []string
	Dots  []chartDot
}

// chart holds the SVG geometry, and in Data what the hover script needs.
type chart struct {
	W, H                     int
	Left, Right, Top, Bottom float64
	Series                   []chartSeries
	YTicks, XTicks           []chartTick
	Data                     chartData
}

// chartData is the chart as the hover script reads it: the reading times and
// their labels, the values of each series at those times (null for no value),
// and how to place them.
type chartData struct {
	Start  int64             `json:"start"` // the time at Left, in Unix seconds
	End    int64             `json:"end"`   // the time at Right
	Left   float64           `json:"left"`
	Right  float64           `json:"right"`
	Top    float64           `json:"top"`
	Bottom float64           `json:"bottom"`
	Lo     float64           `json:"lo"` // the value at Bottom
	Hi     float64           `json:"hi"` // the value at Top
	Times  []int64           `json:"times"`
	Labels []string          `json:"labels"` // the reading times, as the page shows them
	Series []chartDataSeries `json:"series"`
}

type chartDataSeries struct {
	Name   string     `json:"name"`
	Class  string     `json:"class"`
	Values []*float64 `json:"values"`
}

// Legend reports whether the chart needs a legend (more than one series).
func (c *chart) Legend() bool { return len(c.Series) > 1 }

// newChart lays out the series over the span from .. to; each series has one
// value per reading time. Labels use the time zone loc. It returns nil when no
// reading has a value.
func newChart(from, to time.Time, times []time.Time, ss []series, loc *time.Location) *chart {
	lo, hi, found := 0.0, 0.0, false
	for _, s := range ss {
		for _, v := range s.Values {
			if !v.OK {
				continue
			}
			if !found || v.V < lo {
				lo = v.V
			}
			if !found || v.V > hi {
				hi = v.V
			}
			found = true
		}
	}
	if !found {
		return nil
	}

	// The y axis runs between whole steps of 5 °C (or 10, 20, ...) around the
	// values, with at most 6 steps.
	step, bottom, top := 5.0, 0.0, 0.0
	for {
		bottom, top = math.Floor(lo/step)*step, math.Ceil(hi/step)*step
		if top == bottom {
			top += step
		}
		if (top-bottom)/step <= 6 {
			break
		}
		step *= 2
	}

	c := &chart{W: chartW, H: chartH, Left: chartLeft, Right: chartRight, Top: chartTop, Bottom: chartBot}
	c.Data = chartData{Start: from.Unix(), End: to.Unix(), Left: chartLeft, Right: chartRight,
		Top: chartTop, Bottom: chartBot, Lo: bottom, Hi: top,
		Times: make([]int64, len(times)), Labels: make([]string, len(times))}
	for i, t := range times {
		c.Data.Times[i] = t.Unix()
		c.Data.Labels[i] = t.In(loc).Format("Jan 2 15:04 MST")
	}
	x := func(t time.Time) float64 {
		return round1(chartLeft + float64(t.Sub(from))/float64(to.Sub(from))*(chartRight-chartLeft))
	}
	y := func(v float64) float64 { return round1(chartBot - (v-bottom)/(top-bottom)*(chartBot-chartTop)) }

	for v := bottom; v <= top; v += step {
		c.YTicks = append(c.YTicks, chartTick{y(v), fmt.Sprintf("%g °C", v)})
	}
	c.XTicks = timeTicks(from, to, loc, x)

	for n, s := range ss {
		cs := chartSeries{Name: s.Name}
		if len(ss) > 1 {
			cs.Class = fmt.Sprintf("s%d", n%chartColors)
		}
		ds := chartDataSeries{Name: s.Name, Class: cs.Class, Values: make([]*float64, len(s.Values))}

		var run []chartDot
		flush := func() {
			switch {
			case len(run) == 1:
				cs.Dots = append(cs.Dots, run[0])
			case len(run) > 1:
				xy := make([]string, len(run))
				for i, d := range run {
					xy[i] = fmt.Sprintf("%g,%g", d.X, d.Y)
				}
				cs.Lines = append(cs.Lines, strings.Join(xy, " "))
			}
			run = nil
		}
		for i, v := range s.Values {
			if !v.OK {
				flush()
				continue
			}
			value := v.V
			ds.Values[i] = &value
			if i > 0 && times[i].Sub(times[i-1]) > chartGap {
				flush()
			}
			run = append(run, chartDot{x(times[i]), y(v.V)})
		}
		flush()
		c.Series = append(c.Series, cs)
		c.Data.Series = append(c.Data.Series, ds)
	}
	return c
}

func round1(v float64) float64 { return math.Round(v*10) / 10 }

// shortNames maps the start of a long sensor key to a shorter one for the
// page. The first match wins, so a longer start comes before a shorter one.
var shortNames = []struct{ long, short string }{
	{"ls1046a_ddr_controller", "ddr_ctrl"},
	{"ls1046a_a72_cluster", "cpu"},
	{"ls1046a_", ""},
	{"tmp_u58000_", "tmp0_"},
	{"tmp_u58001_", "tmp1_"},
	{"gpy_u11000", "phy1"},
	{"gpy_u12000", "phy2"},
	{"gpy_u13000", "phy3"},
	{"sfp_p16000", "sfp1"},
	{"sfp_p17000", "sfp2"},
}

// displayName returns the name the page shows for a sensor key: a shorter
// start (see shortNames) and no "_c" at the end. "ls1046a_a72_cluster_c" is
// "cpu", "tmp_u58001_local_c" is "tmp1_local", "gpy_u12000_c" is
// "phy2". Other keys lose only the "_c".
func displayName(key string) string {
	for _, n := range shortNames {
		if rest, found := strings.CutPrefix(key, n.long); found {
			key = n.short + rest
			break
		}
	}
	return strings.TrimSuffix(key, tempSuffix)
}
