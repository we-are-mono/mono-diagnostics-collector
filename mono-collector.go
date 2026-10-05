package main

import (
	"crypto/subtle"
	"encoding/json"
	"errors"
	"flag"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"strconv"
	"sync"
	"time"
)

const maxBody = 64 << 10 // 64 KB

const tokenHeader = "X-Diag-Token"

var mu sync.Mutex

type record struct {
	ReceivedAt string          `json:"received_at"`
	Data       json.RawMessage `json:"data"`
}

func main() {
	ip := flag.String("ip", "127.0.0.1", "listen IP address")
	port := flag.Int("port", 8080, "listen port")
	out := flag.String("out", "diag.jsonl", "output JSONL file")
	token := flag.String("token", "", "shared secret required in the X-Diag-Token header")
	flag.Parse()

	if *token == "" {
		log.Fatal("missing required -token")
	}

	if *port < 1 || *port > 65535 {
		log.Fatalf("invalid port: %d", *port)
	}

	f, err := os.OpenFile(*out, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		log.Fatal(err)
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/d", func(w http.ResponseWriter, r *http.Request) {
		got := r.Header.Get(tokenHeader)
		if subtle.ConstantTimeCompare([]byte(got), []byte(*token)) != 1 {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}

		if r.Method != http.MethodPost {
			http.Error(w, "POST only", http.StatusMethodNotAllowed)
			return
		}

		body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxBody))
		if err != nil {
			var maxErr *http.MaxBytesError
			if errors.As(err, &maxErr) {
				http.Error(w, "too large", http.StatusRequestEntityTooLarge)
				return
			}
			http.Error(w, "read failed", http.StatusBadRequest)
			return
		}

		if !json.Valid(body) {
			http.Error(w, "bad json", http.StatusBadRequest)
			return
		}

		line, err := json.Marshal(record{
			ReceivedAt: time.Now().UTC().Format(time.RFC3339),
			Data:       body,
		})
		if err != nil {
			http.Error(w, "encode failed", http.StatusInternalServerError)
			return
		}
		line = append(line, '\n')

		mu.Lock()
		_, err = f.Write(line)
		mu.Unlock()
		if err != nil {
			http.Error(w, "write failed", http.StatusInternalServerError)
			return
		}

		w.WriteHeader(http.StatusNoContent)
	})

	addr := net.JoinHostPort(*ip, strconv.Itoa(*port))
	srv := &http.Server{
		Addr:              addr,
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       10 * time.Second,
		WriteTimeout:      10 * time.Second,
		IdleTimeout:       60 * time.Second,
	}

	log.Printf("listening on %s, writing to %s", addr, *out)
	err = srv.ListenAndServe()
	f.Close()
	if err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Fatal(err)
	}
}
