BINARY ?= mono-collector
IP     ?= 127.0.0.1
PORT   ?= 8080
OUT    ?= diag.jsonl
TOKEN  ?= # required; set with TOKEN=secret
CERT   ?= # optional, for local tests; with KEY, serve HTTPS (production: nginx)
KEY    ?=
# Dashboard address (plain HTTP, no login); DASH= turns it off.
DASH   ?= 127.0.0.1:8081

.PHONY: test build run clean

test:
	go test ./...

build:
	go build -o $(BINARY) .

run:
	go run . -ip $(IP) -port $(PORT) -out $(OUT) -token $(TOKEN) -dash "$(DASH)" $(if $(CERT),-cert $(CERT) -key $(KEY))

clean:
	rm -f $(BINARY)
