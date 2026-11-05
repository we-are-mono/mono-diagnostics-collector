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
	cert := flag.String("cert", "", "TLS certificate file (PEM); with -key, serve HTTPS")
	key := flag.String("key", "", "TLS private key file (PEM); with -cert, serve HTTPS")
	dash := flag.String("dash", "127.0.0.1:8081", "dashboard listen address (plain HTTP, no login); empty disables it")
	flag.Parse()

	if *token == "" {
		log.Fatal("missing required -token")
	}

	if *port < 1 || *port > 65535 {
		log.Fatalf("invalid port: %d", *port)
	}

	if (*cert == "") != (*key == "") {
		log.Fatal("-cert and -key must be given together")
	}

	if *dash != "" {
		if _, _, err := net.SplitHostPort(*dash); err != nil {
			log.Fatalf("invalid -dash address: %v", err)
		}
	}

	f, err := os.OpenFile(*out, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		log.Fatal(err)
	}

	st := newStore()
	if in, err := os.Open(*out); err != nil {
		log.Fatal(err)
	} else {
		kept, skipped, err := st.load(in)
		in.Close()
		if err != nil {
			log.Fatalf("reading %s: %v", *out, err)
		}
		log.Printf("loaded %d readings from %s (%d lines skipped)", kept, *out, skipped)
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

		now := time.Now().UTC().Truncate(time.Second)
		line, err := json.Marshal(record{
			ReceivedAt: now.Format(time.RFC3339),
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
		st.add(now, body)

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

	if *dash != "" {
		go func() {
			ds := &http.Server{
				Addr:              *dash,
				Handler:           dashboard(st, time.Now, time.Local),
				ReadHeaderTimeout: 5 * time.Second,
				WriteTimeout:      10 * time.Second,
				IdleTimeout:       60 * time.Second,
			}
			log.Printf("dashboard on http://%s", *dash)
			log.Fatal(ds.ListenAndServe())
		}()
	}

	if *cert != "" {
		log.Printf("listening on https://%s, writing to %s", addr, *out)
		err = srv.ListenAndServeTLS(*cert, *key)
	} else {
		log.Printf("listening on http://%s, writing to %s", addr, *out)
		err = srv.ListenAndServe()
	}
	f.Close()
	if err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Fatal(err)
	}
}
