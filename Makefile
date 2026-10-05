BINARY ?= mono-collector
IP     ?= 127.0.0.1
PORT   ?= 8080
OUT    ?= diag.jsonl
TOKEN  ?= # required; set with TOKEN=secret

.PHONY: build run clean

build:
	go build -o $(BINARY) .

run:
	go run . -ip $(IP) -port $(PORT) -out $(OUT) -token $(TOKEN)

clean:
	rm -f $(BINARY)
