# mono-collector

A small HTTP collector. It accepts JSON readings over HTTP and appends each one
as a line to a JSONL file. Every request must carry a shared secret token.

## Format

Send any valid JSON value. The collector wraps it in one line:

```
{"received_at":"2025-10-05T19:00:00Z","data":{"deviceId":"aa:bb:cc:dd:ee:ff","temperatures":{"cpu-temp":61.2}}}
```

One line per request. The body limit is 64 KB.

## Device ID

The logger sends the raw `eth0` MAC address as a JSON string field named
`deviceId`. The logger runs only on OpenWrt, so `eth0` is assumed to exist. The
value is read once at startup and reused in every payload.

Example value: `aa:bb:cc:dd:ee:ff`.

Rules:

- Source: the contents of `/sys/class/net/eth0/address`. The kernel prints
  lowercase hex with colons. Command substitution drops the trailing newline.
- Read once at startup. Every payload carries the same value.
- Missing or empty file: the logger exits with code 1. It does not send a
  placeholder ID.
- Lab override: set `DEVICE_ID` in the environment. The default stays the file
  read.
- Change the MAC on `eth0` (for example in `/etc/config/network`) and the device
  gets a new ID.

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
  -d '{"deviceId":"aa:bb:cc:dd:ee:ff","temperatures":{"cpu-temp":61.2},"voltages":{"vcore":1.2}}'
```

A successful send returns `204`. Build the binary with `make build`.

## Run the collector and the logger in pair

The collector stores the readings. The logger runs on the Mono Gateway and
sends the readings to the collector. Start the collector first. Then start the
logger.

1. Start the collector on the host. Use port 8888 and listen on all interfaces,
   so the gateway can reach it:

```
go run . -token SuperSecretKey -ip 0.0.0.0 -port 8888
```

2. Start the logger on the Mono Gateway. Point `HOST` to the host IP address and
   to the collector port. Use the same token:

```
DIAG_TOKEN=SuperSecretKey HOST=192.168.99.201:8888 ./thermal_push.sh
```

The logger sends one reading each `INTERVAL` second. Each stored reading is one
line in `diag.jsonl`. Replace `192.168.99.201` with the IP address of your host.

Note: this repository holds the logger script as `thermal_logger.sh`. The
gateway copy is named `thermal_push.sh`. Copy or rename the file before you run
the command above.

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
