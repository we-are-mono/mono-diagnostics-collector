package main

import (
	"fmt"
	"strings"
	"testing"
	"time"
)

var testNow = time.Date(2026, 11, 5, 12, 0, 0, 0, time.UTC)

// reading returns a record whose hottest sensor is temp. The CPU reads 5
// lower, one sensor reads 0, and the non-temperature keys are larger than any
// temperature. A "null" temp makes every sensor null.
func reading(id string, temp any) []byte {
	cpu, zero := "null", "null"
	if f, isNum := temp.(int); isNum {
		cpu, zero = fmt.Sprint(f-5), "0"
	}
	if f, isNum := temp.(float64); isNum {
		cpu, zero = fmt.Sprint(f-5), "0"
	}
	return []byte(fmt.Sprintf(`{"deviceId":%q,"timestamp":"x","ls1046a_a72_cluster_c":%s,"gpy_u12000_c":%v,`+
		`"tmp_u58001_remote_c":%s,"fan1_rpm":2556,"fan1_pwm":255,"usb_pd_w":120.5}`, id, cpu, temp, zero))
}

// testStore holds two devices:
//   - A (…00:17:df): 40 at 1 h ago, 50 at 30 h ago, 60 at 5 days ago, 70 at 10 days ago
//   - B (…00:00:01): 44 at 2 h ago, and one reading with a null temperature 3 h ago
func testStore(t *testing.T) *store {
	t.Helper()
	s := newStore()
	add := func(id string, ago time.Duration, temp any) {
		if !s.add(testNow.Add(-ago), reading(id, temp)) {
			t.Fatalf("add %s %v refused", id, temp)
		}
	}
	add("e8:f6:d7:00:17:df", time.Hour, 40)
	add("e8:f6:d7:00:17:df", 30*time.Hour, 50)
	add("e8:f6:d7:00:17:df", 5*24*time.Hour, 60)
	add("e8:f6:d7:00:17:df", 10*24*time.Hour, 70)
	add("aa:bb:cc:00:00:01", 2*time.Hour, 44)
	add("aa:bb:cc:00:00:01", 3*time.Hour, "null")
	return s
}

func wantStats(t *testing.T, name string, got []stat, want []any) {
	t.Helper()
	for i, w := range want {
		switch w := w.(type) {
		case nil:
			if got[i].OK {
				t.Errorf("%s[%s] = %v, want none", name, windows[i].Label, got[i].V)
			}
		case float64:
			if !got[i].OK || got[i].V != w {
				t.Errorf("%s[%s] = %+v, want %v", name, windows[i].Label, got[i], w)
			}
		}
	}
}

func TestOverview(t *testing.T) {
	sum, cards := testStore(t).overview(testNow)

	if sum.Devices != 2 || sum.Devices24h != 2 {
		t.Errorf("devices = %d, last 24 h = %d; want 2, 2", sum.Devices, sum.Devices24h)
	}
	// The mean of the device maximums. A: 40, 50, 60, 70. B: 44 in each.
	wantStats(t, "avg", sum.Avg, []any{42.0, 47.0, 52.0, 57.0})
	wantStats(t, "max", sum.Max, []any{44.0, 50.0, 60.0, 70.0})

	if len(cards) != 2 || cards[0].Short != "00:00:01" || cards[1].Short != "00:17:df" {
		t.Fatalf("cards = %+v, want 00:00:01 then 00:17:df", cards)
	}
	wantStats(t, "max A", cards[1].Sum.Max, []any{40.0, 50.0, 60.0, 70.0})
	wantStats(t, "max B", cards[0].Sum.Max, []any{44.0, 44.0, 44.0, 44.0})
}

func TestDevice(t *testing.T) {
	s := testStore(t)

	sum, found := s.device("aa:bb:cc:00:00:01", testNow)
	if !found {
		t.Fatal("device B not found")
	}
	// The null reading counts as a reading but not as a temperature.
	if sum.Readings24h != 2 || !sum.LastSeen.Equal(testNow.Add(-2*time.Hour)) {
		t.Errorf("readings = %d, last = %v; want 2, 2 h ago", sum.Readings24h, sum.LastSeen)
	}
	wantStats(t, "max B", sum.Max, []any{44.0, 44.0, 44.0, 44.0})

	sum, _ = s.device("e8:f6:d7:00:17:df", testNow)
	if sum.Devices24h != 1 || sum.Readings24h != 1 {
		t.Errorf("A: devices 24 h = %d, readings 24 h = %d; want 1, 1", sum.Devices24h, sum.Readings24h)
	}

	if _, found := s.device("00:00:00:00:00:00", testNow); found {
		t.Error("unknown device found")
	}
}

func TestQuietDevice(t *testing.T) {
	s := newStore()
	s.add(testNow.Add(-3*24*time.Hour), reading("e8:f6:d7:00:17:df", 55))
	sum, _ := s.overview(testNow)
	if sum.Devices != 1 || sum.Devices24h != 0 {
		t.Errorf("devices = %d, last 24 h = %d; want 1, 0", sum.Devices, sum.Devices24h)
	}
	wantStats(t, "avg", sum.Avg, []any{nil, nil, 55.0, 55.0})
}

// TestHottestSensor: a reading's temperature is its highest "_c" value, and
// keys without the suffix are not temperatures.
func TestHottestSensor(t *testing.T) {
	s := newStore()
	s.add(testNow, []byte(`{"deviceId":"d","sfp_p16000_c":47.5,"gpy_u12000_c":48.875,"ls1046a_a72_cluster_c":45,`+
		`"tmp_u58001_remote_c":0,"sfp_p17000_c":null,"fan1_rpm":2556,"usb_pd_w":120,"label_c":"hot"}`))
	sum, _ := s.device("d", testNow)
	wantStats(t, "max", sum.Max, []any{48.875})
}

// TestIgnored: the unconnected sensor is not kept, so it is in no column or
// chart, and its 0 is never a temperature.
func TestIgnored(t *testing.T) {
	s := newStore()
	s.add(testNow, []byte(`{"deviceId":"d","tmp_u58001_remote_c":0,"tmp_u58001_local_c":-5}`))
	if _, found := s.index["tmp_u58001_remote_c"]; found || len(s.keys) != 1 {
		t.Errorf("keys = %+v, want tmp_u58001_local_c only", s.keys)
	}
	sum, _ := s.device("d", testNow)
	wantStats(t, "max", sum.Max, []any{-5.0})
}

func TestAddRefuses(t *testing.T) {
	s := newStore()
	for _, body := range []string{
		`{"probe":1}`,
		`{"deviceId":""}`,
		`{"deviceId":42}`,
		`[1,2]`,
		`{"deviceId":"` + strings.Repeat("a", maxIDLen+1) + `"}`,
	} {
		if s.add(testNow, []byte(body)) {
			t.Errorf("kept %s", body)
		}
	}
	if len(s.devices) != 0 {
		t.Errorf("devices = %v, want none", s.devices)
	}
}

func TestLoad(t *testing.T) {
	file := strings.Join([]string{
		`{"received_at":"2026-11-05T11:00:00Z","data":{"probe":1}}`,
		`{"received_at":"2026-11-05T11:00:00Z","data":` + string(reading("e8:f6:d7:00:17:df", 41.5)) + `}`,
		`not json`,
		`{"received_at":"yesterday","data":` + string(reading("e8:f6:d7:00:17:df", 99)) + `}`,
		`{"received_at":"2026-11-05T11:15:00Z","data":` + string(reading("e8:f6:d7:00:17:df", 42.5)) + `}`,
	}, "\n") + "\n"

	s := newStore()
	kept, skipped, err := s.load(strings.NewReader(file))
	if err != nil || kept != 2 || skipped != 3 {
		t.Fatalf("load = %d kept, %d skipped, %v; want 2, 3, nil", kept, skipped, err)
	}
	sum, _ := s.device("e8:f6:d7:00:17:df", testNow)
	wantStats(t, "max", sum.Max, []any{42.5})
}

func TestShortID(t *testing.T) {
	for id, want := range map[string]string{
		"e8:f6:d7:00:17:df": "00:17:df",
		"lab-1":             "lab-1",
		"12345678":          "12345678",
	} {
		if got := shortID(id); got != want {
			t.Errorf("shortID(%q) = %q, want %q", id, got, want)
		}
	}
}

func TestReadings(t *testing.T) {
	s := newStore()
	s.add(testNow.Add(-2*time.Hour), []byte(`{"deviceId":"d","timestamp":"x","b_c":45.000,"a_c":null,"fan1_rpm":2556,"note":"hi"}`))
	s.add(testNow.Add(-1*time.Hour), []byte(`{"deviceId":"d","b_c":46.125,"fan1_rpm":2600,"usb_pd_w":12}`))
	s.add(testNow.Add(-8*24*time.Hour), []byte(`{"deviceId":"d","old_c":40}`))
	s.add(testNow, []byte(`{"deviceId":"other","gpy_c":70}`))

	keys, rows := s.readings("d", testNow.Add(-7*24*time.Hour))

	// Keys in the order first seen; a_c (only null), old_c (too old) and
	// gpy_c (another device) have no value in these rows.
	var names []string
	for _, k := range keys {
		names = append(names, fmt.Sprintf("%s:%v", k.Name, k.Decimal))
	}
	if got := strings.Join(names, " "); got != "b_c:true fan1_rpm:false usb_pd_w:false" {
		t.Errorf("keys = %q", got)
	}
	if len(rows) != 2 || !rows[0].At.Equal(testNow.Add(-time.Hour)) {
		t.Fatalf("rows = %+v, want 2, newest first", rows)
	}
	want := [][]any{{46.125, 2600.0, 12.0}, {45.0, 2556.0, nil}}
	for i, r := range rows {
		wantStatsN(t, fmt.Sprintf("row %d", i), r.Values, want[i])
	}
}

func TestKeyCap(t *testing.T) {
	s := newStore()
	var b strings.Builder
	b.WriteString(`{"deviceId":"d"`)
	for i := range maxKeys + 5 {
		fmt.Fprintf(&b, `,"k%d":%d`, i, i)
	}
	b.WriteString(`}`)
	if !s.add(testNow, []byte(b.String())) {
		t.Fatal("refused")
	}
	if len(s.keys) != maxKeys {
		t.Errorf("keys = %d, want %d", len(s.keys), maxKeys)
	}
}

// wantStatsN is wantStats without the window labels.
func wantStatsN(t *testing.T, name string, got []stat, want []any) {
	t.Helper()
	if len(got) != len(want) {
		t.Errorf("%s = %+v, want %v", name, got, want)
		return
	}
	for i, w := range want {
		if w == nil && got[i].OK || w != nil && (!got[i].OK || got[i].V != w.(float64)) {
			t.Errorf("%s[%d] = %+v, want %v", name, i, got[i], w)
		}
	}
}
