package main

import (
	"bufio"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestHub(t *testing.T) {
	s := newStore()
	ch, cancel := s.events.subscribe()
	s.add(testNow, reading("e8:f6:d7:00:17:df", 50))
	select {
	case id := <-ch:
		if id != "e8:f6:d7:00:17:df" {
			t.Errorf("id = %q", id)
		}
	default:
		t.Fatal("no event for a stored reading")
	}

	s.add(testNow, []byte(`{"probe":1}`)) // not kept, so no event
	cancel()
	s.add(testNow, reading("d", 50)) // after cancel
	select {
	case id := <-ch:
		t.Errorf("event %q after a refused reading or after cancel", id)
	default:
	}
}

// TestHubSlowReader: a reader that does not read never holds up add.
func TestHubSlowReader(t *testing.T) {
	s := newStore()
	_, cancel := s.events.subscribe()
	defer cancel()
	done := make(chan struct{})
	go func() {
		for range 100 {
			s.add(testNow, reading("d", 50))
		}
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("add blocked on a full subscriber")
	}
}

// TestEventStream: an open /events stream gets one "reading" event with the
// deviceId as a JSON string for each stored reading.
func TestEventStream(t *testing.T) {
	s := newStore()
	srv := httptest.NewServer(dashboard(s, func() time.Time { return testNow }, time.UTC))
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/events")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if ct := resp.Header.Get("Content-Type"); resp.StatusCode != http.StatusOK || ct != "text/event-stream" {
		t.Fatalf("status %d, content type %q", resp.StatusCode, ct)
	}

	lines := make(chan string)
	go func() {
		sc := bufio.NewScanner(resp.Body)
		for sc.Scan() {
			lines <- sc.Text()
		}
		close(lines)
	}()
	next := func() string {
		select {
		case l, open := <-lines:
			if !open {
				t.Fatal("stream ended")
			}
			return l
		case <-time.After(2 * time.Second):
			t.Fatal("no line from the stream")
		}
		return ""
	}

	if l := next(); l != "retry: 5000" {
		t.Fatalf("first line = %q", l)
	}
	next() // blank line

	// The handler subscribes before its first write, so this add is seen.
	s.add(testNow, reading("e8:f6:d7:00:17:df", 50))
	if got := next() + "|" + next(); got != `event: reading|data: "e8:f6:d7:00:17:df"` {
		t.Errorf("event = %q", got)
	}

	// A deviceId with a line break stays on one data line.
	s.add(testNow, reading("a\nevent: x", 50))
	next() // blank line after the first event
	if got := next() + "|" + next(); got != `event: reading|data: "a\nevent: x"` {
		t.Errorf("event = %q", got)
	}
}

func TestLiveUpdateScript(t *testing.T) {
	s := testStore(t)
	_, body := get(t, s, "/")
	mustContain(t, body, `new EventSource("/events")`, `var device = "";`,
		"The page updates when a reading comes in.", `<meta http-equiv="refresh" content="300">`)
	_, body = get(t, s, "/?device=e8:f6:d7:00:17:df")
	mustContain(t, body, `new EventSource("/events")`, `var device = "e8:f6:d7:00:17:df";`)
	if !strings.Contains(body, `id !== device`) {
		t.Error("device page does not filter events by device")
	}
}
