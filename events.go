package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"sync"
	"time"
)

// eventPing is how often the event stream sends a comment, so that a closed
// connection is noticed and proxies keep it open.
const eventPing = 30 * time.Second

// hub tells the dashboard pages that are open which device just sent a
// reading.
type hub struct {
	mu   sync.Mutex
	subs map[chan string]struct{}
}

// subscribe returns a channel that receives the deviceId of each new reading,
// and a function that ends the subscription. A slow reader misses readings
// rather than holding up the collector.
func (h *hub) subscribe() (<-chan string, func()) {
	ch := make(chan string, 8)
	h.mu.Lock()
	if h.subs == nil {
		h.subs = map[chan string]struct{}{}
	}
	h.subs[ch] = struct{}{}
	h.mu.Unlock()
	return ch, func() {
		h.mu.Lock()
		delete(h.subs, ch)
		h.mu.Unlock()
	}
}

// publish sends a deviceId to every subscriber that has room for it.
func (h *hub) publish(id string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for ch := range h.subs {
		select {
		case ch <- id:
		default:
		}
	}
}

// events serves a Server-Sent Events stream: one "reading" event, with the
// deviceId as a JSON string, for each reading the store keeps.
func events(s *store) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rc := http.NewResponseController(w)
		// The stream stays open: no write deadline (the server's WriteTimeout
		// would end it after 10 s). Not every writer supports this.
		_ = rc.SetWriteDeadline(time.Time{})

		ch, cancel := s.events.subscribe()
		defer cancel()

		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-store")
		fmt.Fprint(w, "retry: 5000\n\n")
		if rc.Flush() != nil {
			return
		}

		ping := time.NewTicker(eventPing)
		defer ping.Stop()
		for {
			select {
			case <-r.Context().Done():
				return
			case id := <-ch:
				data, _ := json.Marshal(id) // a JSON string has no line break
				fmt.Fprintf(w, "event: reading\ndata: %s\n\n", data)
			case <-ping.C:
				fmt.Fprint(w, ": ping\n\n")
			}
			if rc.Flush() != nil {
				return
			}
		}
	})
}
