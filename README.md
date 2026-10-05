# mono-collector

A small HTTP collector. It accepts JSON readings over HTTP and appends each one
as a line to a JSONL file. Every request must carry a shared secret token.

## Format

Send any valid JSON value. The collector wraps it in one line:

```
{"received_at":"2025-10-05T19:00:00Z","data":{"uniqueId":"aefc987fa","temperatures":{"cpu-temp":61.2}}}
```

One line per request. The body limit is 64 KB.

## Usage

```
make run TOKEN=secret
```

or directly:

```
go run . -ip 127.0.0.1 -port 8080 -out diag.jsonl -token secret
```

Send a reading:

```
curl -sS -o /dev/null -w '%{http_code}\n' -X POST http://127.0.0.1:8080/d \
  -H 'X-Diag-Token: secret' -H 'Content-Type: application/json' \
  -d '{"uniqueId":"aefc987fa","temperatures":{"cpu-temp":61.2},"voltages":{"vcore":1.2}}'
```

A successful send returns `204`. Build the binary with `make build`.

## CLI parameters

| Parameter | Default       | Description                                        |
|-----------|---------------|----------------------------------------------------|
| `-ip`     | `127.0.0.1`   | Listen IP address. Use `0.0.0.0` for all interfaces.|
| `-port`   | `8080`        | Listen port, range 1 to 65535.                     |
| `-out`    | `diag.jsonl`  | Output JSONL file, opened in append mode.          |
| `-token`  | none          | Required shared secret. Empty value stops startup. |

The token is compared against the `X-Diag-Token` header. A missing or wrong
token returns `401` and the request is not stored.

## Responses

| Code | Meaning                    |
|------|----------------------------|
| 204  | Stored                     |
| 400  | Body is not valid JSON     |
| 401  | Missing or wrong token     |
| 405  | Method is not POST         |
| 413  | Body is larger than 64 KB  |
