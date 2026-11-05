package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"io"
	"math"
	"sort"
	"strings"
	"sync"
	"time"
)

// maxIDLen caps the deviceId the store keeps. A MAC address has 17 characters.
const maxIDLen = 64

// maxKeys caps the number of sensor keys the store keeps over all readings.
// A Mono Gateway reading has 17; later new keys are dropped.
const maxKeys = 64

// ignored lists the keys the store drops: sensors that are not connected on
// the Mono Gateway and always read 0. The JSONL file still has them.
var ignored = map[string]bool{
	"tmp_u58001_remote_c": true,
}

// tempSuffix marks the temperature keys of a reading, in degrees Celsius
// ("ls1046a_a72_cluster_c", "sfp_p16000_c", ...).
const tempSuffix = "_c"

// window is one period the dashboard reports on. A zero span means all time.
type window struct {
	Label string
	Span  time.Duration
}

var windows = []window{
	{"24 h", 24 * time.Hour},
	{"2 days", 48 * time.Hour},
	{"7 days", 7 * 24 * time.Hour},
	{"All time", 0},
}

// point is one stored reading of one device.
type point struct {
	at     time.Time // when the collector received the reading
	temp   float64   // the hottest sensor of the reading
	ok     bool      // the reading had at least one temperature
	values []float64 // by sensor key index; NaN or past the end: no value
}

// value returns the reading's value of the sensor key with index k.
func (p point) value(k int) (float64, bool) {
	if k >= len(p.values) || math.IsNaN(p.values[k]) {
		return 0, false
	}
	return p.values[k], true
}

// sensorKey is one numeric key that readings carry, in the order the store
// first saw it (for a Mono Gateway, the order of the record).
type sensorKey struct {
	Name    string
	Decimal bool // some value had a decimal point, so show 3 decimals
	index   int
}

// Temp reports whether the key is a temperature.
func (k sensorKey) Temp() bool { return strings.HasSuffix(k.Name, tempSuffix) }

// store keeps every reading in memory, so the dashboard does not read the
// JSONL file again for each page.
type store struct {
	mu      sync.RWMutex
	devices map[string][]point
	keys    []sensorKey
	index   map[string]int
	events  hub // tells open dashboard pages about each new reading
}

func newStore() *store {
	return &store{devices: map[string][]point{}, index: map[string]int{}}
}

type field struct {
	key string
	raw json.RawMessage
}

// fields returns the keys and values of a JSON object, in their order.
func fields(body []byte) ([]field, bool) {
	dec := json.NewDecoder(bytes.NewReader(body))
	if tok, err := dec.Token(); err != nil || tok != json.Delim('{') {
		return nil, false
	}
	var out []field
	for dec.More() {
		tok, err := dec.Token()
		if err != nil {
			return nil, false
		}
		var raw json.RawMessage
		if dec.Decode(&raw) != nil {
			return nil, false
		}
		out = append(out, field{tok.(string), raw})
	}
	return out, true
}

// add records one reading body received at the given time. It returns false
// and keeps nothing when the body is not an object with a string deviceId.
// Every key with a number or null value is a sensor; other keys are dropped.
func (s *store) add(at time.Time, body []byte) bool {
	fs, valid := fields(body)
	if !valid {
		return false
	}
	var id string
	for _, f := range fs {
		if f.key == "deviceId" && json.Unmarshal(f.raw, &id) != nil {
			return false
		}
	}
	if id == "" || len(id) > maxIDLen {
		return false
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	p := point{at: at}
	for _, f := range fs {
		var v *float64
		if ignored[f.key] || json.Unmarshal(f.raw, &v) != nil {
			continue // not connected, or not a number or null
		}
		k, known := s.index[f.key]
		if !known {
			if len(s.keys) >= maxKeys {
				continue
			}
			k = len(s.keys)
			s.index[f.key] = k
			s.keys = append(s.keys, sensorKey{Name: f.key, index: k})
		}
		for len(p.values) <= k {
			p.values = append(p.values, math.NaN())
		}
		if v == nil {
			continue
		}
		p.values[k] = *v
		if bytes.ContainsAny(f.raw, ".eE") {
			s.keys[k].Decimal = true
		}
		if s.keys[k].Temp() && (!p.ok || *v > p.temp) {
			p.temp, p.ok = *v, true
		}
	}
	s.devices[id] = append(s.devices[id], p)
	s.events.publish(id)
	return true
}

// load adds every reading in a JSONL file that the collector wrote. It returns
// the number of readings kept and of lines skipped (not a record, or a record
// with no deviceId).
func (s *store) load(r io.Reader) (kept, skipped int, err error) {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64<<10), 1<<20)
	for sc.Scan() {
		var rec record
		if json.Unmarshal(sc.Bytes(), &rec) != nil {
			skipped++
			continue
		}
		at, err := time.Parse(time.RFC3339, rec.ReceivedAt)
		if err != nil || !s.add(at, rec.Data) {
			skipped++
			continue
		}
		kept++
	}
	return kept, skipped, sc.Err()
}

// stat is one value that may be missing (no temperature in the window).
type stat struct {
	V  float64
	OK bool
}

// summary holds the dashboard numbers for one device or for all of them.
// Avg and Max have one entry per window, in the order of windows. A device's
// maximum is its hottest sensor over all its readings in the window. Avg is the
// mean of the device maximums; Max is the highest of them.
type summary struct {
	Devices     int // devices with at least one reading
	Devices24h  int // devices with a reading in the last 24 h
	Readings24h int
	LastSeen    time.Time
	Avg         []stat
	Max         []stat
}

// card is one device in the overview grid.
type card struct {
	ID    string
	Short string
	Sum   summary
}

// summarize computes a summary over the given devices' points.
func summarize(devices [][]point, now time.Time) summary {
	sums := make([]float64, len(windows))
	counts := make([]int, len(windows))
	sum := summary{Avg: make([]stat, len(windows)), Max: make([]stat, len(windows))}
	day := now.Add(-24 * time.Hour)

	for _, points := range devices {
		if len(points) == 0 {
			continue
		}
		sum.Devices++
		recent := false
		device := make([]stat, len(windows))
		for _, p := range points {
			if p.at.After(sum.LastSeen) {
				sum.LastSeen = p.at
			}
			if !p.at.Before(day) {
				recent = true
				sum.Readings24h++
			}
			if !p.ok {
				continue
			}
			for i, w := range windows {
				if w.Span != 0 && p.at.Before(now.Add(-w.Span)) {
					continue
				}
				if !device[i].OK || p.temp > device[i].V {
					device[i] = stat{p.temp, true}
				}
			}
		}
		if recent {
			sum.Devices24h++
		}
		for i, m := range device {
			if !m.OK {
				continue
			}
			sums[i] += m.V
			counts[i]++
			if !sum.Max[i].OK || m.V > sum.Max[i].V {
				sum.Max[i] = m
			}
		}
	}
	for i := range windows {
		if counts[i] > 0 {
			sum.Avg[i] = stat{sums[i] / float64(counts[i]), true}
		}
	}
	return sum
}

// overview returns the summary over all devices and one card per device,
// sorted by short ID.
func (s *store) overview(now time.Time) (summary, []card) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	all := make([][]point, 0, len(s.devices))
	cards := make([]card, 0, len(s.devices))
	for id, points := range s.devices {
		all = append(all, points)
		cards = append(cards, card{ID: id, Short: shortID(id), Sum: summarize([][]point{points}, now)})
	}
	sort.Slice(cards, func(i, j int) bool {
		if cards[i].Short != cards[j].Short {
			return cards[i].Short < cards[j].Short
		}
		return cards[i].ID < cards[j].ID
	})
	return summarize(all, now), cards
}

// device returns the summary of one device, and false when the store has no
// reading from it.
func (s *store) device(id string, now time.Time) (summary, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	points, found := s.devices[id]
	if !found {
		return summary{}, false
	}
	return summarize([][]point{points}, now), true
}

// shortID returns the last three bytes of a MAC address ("00:17:df"), or the
// whole ID when it is shorter than that.
func shortID(id string) string {
	r := []rune(id)
	if len(r) <= 8 {
		return id
	}
	return string(r[len(r)-8:])
}

// row is one reading in the device table. Values line up with the keys that
// readings returns.
type row struct {
	At     time.Time
	Values []stat
}

// readings returns the device's readings received at or after since, newest
// first, and the sensor keys that have a value in at least one of them.
func (s *store) readings(id string, since time.Time) ([]sensorKey, []row) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	var points []point
	used := make([]bool, len(s.keys))
	for _, p := range s.devices[id] {
		if p.at.Before(since) {
			continue
		}
		points = append(points, p)
		for k := range s.keys {
			if _, found := p.value(k); found {
				used[k] = true
			}
		}
	}
	sort.SliceStable(points, func(i, j int) bool { return points[i].at.After(points[j].at) })

	var keys []sensorKey
	for k, key := range s.keys {
		if used[k] {
			keys = append(keys, key)
		}
	}
	rows := make([]row, len(points))
	for i, p := range points {
		rows[i] = row{At: p.at, Values: make([]stat, len(keys))}
		for j, key := range keys {
			if v, found := p.value(key.index); found {
				rows[i].Values[j] = stat{v, true}
			}
		}
	}
	return keys, rows
}
