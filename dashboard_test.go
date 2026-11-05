package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func get(t *testing.T, s *store, target string) (int, string) {
	t.Helper()
	rec := httptest.NewRecorder()
	dashboard(s, func() time.Time { return testNow }, time.UTC).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, target, nil))
	return rec.Code, rec.Body.String()
}

func mustContain(t *testing.T, body string, parts ...string) {
	t.Helper()
	for _, p := range parts {
		if !strings.Contains(body, p) {
			t.Errorf("page has no %q", p)
		}
	}
}

func TestDashboardOverview(t *testing.T) {
	code, body := get(t, testStore(t), "/")
	if code != http.StatusOK {
		t.Fatalf("code = %d", code)
	}
	mustContain(t, body,
		"Devices, last 24 h", `<span class="value">2</span>`, "of 2 in total",
		"<h2>Combined stats</h2>",
		`Average max, 24 h</span><span class="value">42.0 °C`, `Average max, 2 days</span><span class="value">47.0 °C`,
		`Average max, 7 days</span><span class="value">52.0 °C`, `Average max, all time</span><span class="value">57.0 °C`,
		`href="/?device=e8%3af6%3ad7%3a00%3a17%3adf"`, ">00:17:df<", ">00:00:01<",
		"Max 24 h</dt><dd>40.0 °C", "Max 7 days</dt><dd>60.0 °C",
		"Last reading 1 h ago",
		"Each temperature is the hottest sensor on a device. Averages are across devices.",
		"Times in UTC, as received by the collector. The page reloads every 5 minutes.",
	)
	if strings.Contains(body, "Viewing") || strings.Contains(body, "Max All time") {
		t.Error("overview shows the device header or an all-time max")
	}
}

func TestDashboardDevice(t *testing.T) {
	code, body := get(t, testStore(t), "/?device=e8:f6:d7:00:17:df")
	if code != http.StatusOK {
		t.Fatalf("code = %d", code)
	}
	mustContain(t, body,
		`Viewing <span class="mono">00:17:df</span>`, "e8:f6:d7:00:17:df",
		`<a class="button" href="/">`, "Readings, last 24 h",
		"Maximum temperature", "40.0 °C", "50.0 °C", "60.0 °C", "70.0 °C",
	)
	if strings.Contains(body, `class="card`) || strings.Contains(body, "Average") || strings.Contains(body, "across devices") ||
		!strings.Contains(body, "<h2>Stats</h2>") {
		t.Error("device view shows the device grid or an average")
	}
}

func TestDashboardTable(t *testing.T) {
	s := newStore()
	s.add(testNow.Add(-2*time.Hour), []byte(`{"deviceId":"d","gpy_c":44.432,"fan1_rpm":2556,"usb_pd_w":null}`))
	s.add(testNow.Add(-1*time.Hour), []byte(`{"deviceId":"d","gpy_c":45.000,"fan1_rpm":2600,"usb_pd_w":12.16}`))

	_, body := get(t, s, "/?device=d")
	mustContain(t, body,
		"All readings, last 7 days (2)",
		`<div class="table-wrap full">`, `<table style="min-width: 21.75em">`,
		`<th>Received</th><th class="mono" title="gpy_c">gpy</th><th class="mono" title="fan1_rpm">fan1_<wbr>rpm</th><th class="mono" title="usb_pd_w">usb_<wbr>pd_<wbr>w</th>`,
		`>Nov 5 11:00</th><td>45.000</td><td>2600</td><td>12.160</td></tr>`,
		`>Nov 5 10:00</th><td>44.432</td><td>2556</td><td>–</td></tr>`,
		"Times in UTC, as received by the collector.",
	)
	if strings.Index(body, "Nov 5 11:00</th>") > strings.Index(body, "Nov 5 10:00</th>") {
		t.Error("table is not newest first")
	}
}

func TestDashboardTheme(t *testing.T) {
	for _, target := range []string{"/", "/?device=e8:f6:d7:00:17:df"} {
		_, body := get(t, testStore(t), target)
		mustContain(t, body, "--bg: #1e1e2e", ".s0 { --c: #89b4fa; }", "color-scheme: dark;")
		for _, gone := range []string{"data-theme", "prefers-color-scheme", "localStorage", `id="theme"`} {
			if strings.Contains(body, gone) {
				t.Errorf("%s: page still has %q", target, gone)
			}
		}
	}
}

// TestColumnEscapes: a sensor key from a reading is escaped before the page
// adds its line breaks.
func TestColumnEscapes(t *testing.T) {
	if got := string(column(`a_<b>&"c`)); got != `a_<wbr>&lt;b&gt;&amp;&#34;c` {
		t.Errorf("column = %q", got)
	}
}

// TestDashboardNotes: the times note is the second line under the title, and
// the page has no footer.
func TestDashboardNotes(t *testing.T) {
	for target, first := range map[string]string{
		"/":                          "Each temperature is the hottest sensor on a device. Averages are across devices.",
		"/?device=e8:f6:d7:00:17:df": "Each temperature is the hottest sensor on this device.",
	} {
		_, body := get(t, testStore(t), target)
		mustContain(t, body, `<p class="note muted">`+first+`</p>
<p class="note muted">Times in UTC, as received by the collector. The page reloads every 5 minutes.</p>`)
		if strings.Contains(body, "<footer") {
			t.Errorf("%s: page still has a footer", target)
		}
	}
}

func TestTableCentered(t *testing.T) {
	_, body := get(t, testStore(t), "/?device=e8:f6:d7:00:17:df")
	mustContain(t, body, "th, td { padding: 4px 6px; text-align: center;")
	if strings.Contains(body, "text-align: left") || strings.Contains(body, "td { padding: 4px 6px; text-align: right") {
		t.Error("a table cell is still left or right aligned")
	}
}

func TestDashboardEmpty(t *testing.T) {
	_, body := get(t, newStore(), "/")
	mustContain(t, body, "No readings yet.", "of 0 in total", "–")
}

func TestDashboardNotFound(t *testing.T) {
	s := testStore(t)
	for _, target := range []string{"/?device=00:00:00:00:00:00", "/d", "/favicon.ico"} {
		if code, _ := get(t, s, target); code != http.StatusNotFound {
			t.Errorf("%s: code = %d, want 404", target, code)
		}
	}
}

func TestDashboardEscapes(t *testing.T) {
	s := newStore()
	s.add(testNow, reading("<script>x</script>", 40))
	_, body := get(t, s, "/")
	if strings.Contains(body, "<script>x</script>") {
		t.Error("deviceId is not escaped")
	}
}

func TestAgo(t *testing.T) {
	for d, want := range map[time.Duration]string{
		10 * time.Second:   "just now",
		14 * time.Minute:   "14 min ago",
		3 * time.Hour:      "3 h ago",
		47 * time.Hour:     "47 h ago",
		5 * 24 * time.Hour: "5 days ago",
	} {
		if got := ago(testNow.Add(-d), testNow); got != want {
			t.Errorf("ago(%v) = %q, want %q", d, got, want)
		}
	}
	if got := ago(time.Time{}, testNow); got != "never" {
		t.Errorf("ago(zero) = %q, want never", got)
	}
}
