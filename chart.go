package main

import (
	"fmt"
	"math"
	"strings"
	"time"
)

// The device page's charts: the hottest sensor per hour, and each temperature
// sensor per hour, over the last 7 days, drawn as inline SVG. A small script
// on the page shows the values under the pointer from the chart's data.
const (
	chartHours  = 7 * 24
	chartW      = 840
	chartH      = 240
	chartLeft   = 48.0 // room for the °C labels
	chartRight  = chartW - 12.0
	chartTop    = 12.0
	chartBot    = chartH - 28.0 // room for the day labels
	chartColors = 14            // series classes s0 .. s13 in the page's CSS
)

// chartStart returns the start of the first of the 7 days of hours that end
// with the current hour.
func chartStart(now time.Time) time.Time {
	return now.UTC().Truncate(time.Hour).Add(-(chartHours - 1) * time.Hour)
}

// hourIndex returns the chart hour that t falls in, and false when it is
// outside the 7 days.
func hourIndex(start, t time.Time) (int, bool) {
	if t.Before(start) {
		return 0, false
	}
	i := int(t.Sub(start) / time.Hour)
	return i, i < chartHours
}

// keepMax sets h to v when h has no value or a lower one.
func keepMax(h *stat, v float64) {
	if !h.OK || v > h.V {
		*h = stat{v, true}
	}
}

// hourly returns the start of the first hour and the device's hottest sensor
// in each hour of the last 7 days, oldest first. The last hour is the current
// one, so it may hold only part of an hour.
func (s *store) hourly(id string, now time.Time) (time.Time, []stat) {
	start := chartStart(now)
	hours := make([]stat, chartHours)

	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, p := range s.devices[id] {
		if i, in := hourIndex(start, p.at); in && p.ok {
			keepMax(&hours[i], p.temp)
		}
	}
	return start, hours
}

// series is one line of a chart: a name and one value per hour.
type series struct {
	Name  string
	Hours []stat
}

// sensorHours returns the start of the first hour and, for each temperature
// key with a value in the last 7 days, the device's maximum in each hour.
func (s *store) sensorHours(id string, now time.Time) (time.Time, []series) {
	start := chartStart(now)

	s.mu.RLock()
	defer s.mu.RUnlock()
	hours := make([][]stat, len(s.keys))
	for _, p := range s.devices[id] {
		i, in := hourIndex(start, p.at)
		if !in {
			continue
		}
		for k, key := range s.keys {
			v, found := p.value(k)
			if !found || !key.Temp() {
				continue
			}
			if hours[k] == nil {
				hours[k] = make([]stat, chartHours)
			}
			keepMax(&hours[k][i], v)
		}
	}

	var out []series
	for k, key := range s.keys {
		if hours[k] != nil {
			out = append(out, series{key.Name, hours[k]})
		}
	}
	return start, out
}

type chartTick struct {
	Pos   float64
	Label string
}

// chartDot marks a value that no line shows: no value in the hour before or
// after it.
type chartDot struct {
	X, Y float64
}

// chartSeries is one series laid out. Lines has one "x,y x,y ..." list per run
// of hours with a value; an hour with no value breaks the line.
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

// chartData is the chart as the hover script reads it: the hour values of
// each series (null for no value) and how to place them.
type chartData struct {
	Start  int64             `json:"start"` // the first hour, in Unix seconds
	Left   float64           `json:"left"`
	Right  float64           `json:"right"`
	Top    float64           `json:"top"`
	Bottom float64           `json:"bottom"`
	Lo     float64           `json:"lo"` // the value at Bottom
	Hi     float64           `json:"hi"` // the value at Top
	Series []chartDataSeries `json:"series"`
}

type chartDataSeries struct {
	Name   string     `json:"name"`
	Class  string     `json:"class"`
	Values []*float64 `json:"values"`
}

// Legend reports whether the chart needs a legend (more than one series).
func (c *chart) Legend() bool { return len(c.Series) > 1 }

// newChart lays out the series over hours from start. It returns nil when no
// hour has a value.
func newChart(start time.Time, ss []series) *chart {
	lo, hi, found := 0.0, 0.0, false
	for _, s := range ss {
		for _, h := range s.Hours {
			if !h.OK {
				continue
			}
			if !found || h.V < lo {
				lo = h.V
			}
			if !found || h.V > hi {
				hi = h.V
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
	c.Data = chartData{Start: start.Unix(), Left: chartLeft, Right: chartRight, Top: chartTop, Bottom: chartBot,
		Lo: bottom, Hi: top}
	x := func(i int) float64 {
		return round1(chartLeft + float64(i)*(chartRight-chartLeft)/float64(chartHours-1))
	}
	y := func(v float64) float64 { return round1(chartBot - (v-bottom)/(top-bottom)*(chartBot-chartTop)) }

	for v := bottom; v <= top; v += step {
		c.YTicks = append(c.YTicks, chartTick{y(v), fmt.Sprintf("%g °C", v)})
	}
	for i := range chartHours {
		if at := start.Add(time.Duration(i) * time.Hour); at.Hour() == 0 {
			c.XTicks = append(c.XTicks, chartTick{x(i), at.Format("Jan 2")})
		}
	}

	for n, s := range ss {
		cs := chartSeries{Name: s.Name}
		if len(ss) > 1 {
			cs.Class = fmt.Sprintf("s%d", n%chartColors)
		}
		ds := chartDataSeries{Name: s.Name, Class: cs.Class, Values: make([]*float64, len(s.Hours))}
		var run []string
		for i, h := range s.Hours {
			if !h.OK {
				if len(run) > 1 {
					cs.Lines = append(cs.Lines, strings.Join(run, " "))
				}
				run = nil
				continue
			}
			v := h.V
			ds.Values[i] = &v
			run = append(run, fmt.Sprintf("%g,%g", x(i), y(h.V)))
			if (i == 0 || !s.Hours[i-1].OK) && (i == len(s.Hours)-1 || !s.Hours[i+1].OK) {
				cs.Dots = append(cs.Dots, chartDot{x(i), y(h.V)})
			}
		}
		if len(run) > 1 {
			cs.Lines = append(cs.Lines, strings.Join(run, " "))
		}
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
