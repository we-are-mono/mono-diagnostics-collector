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

A successful send returns `204`. Build the binary with `make build`. Run the
tests with `make test`.

## Serve HTTPS

The Verso plugin (`verso-plugin-diag`) sends only over HTTPS. Use a certificate
that the routers' `ca-bundle` trusts (for example from Let's Encrypt), issued
for the name in the plugin's `COLLECTOR_URL`:

```
go run . -token secret -ip 0.0.0.0 -port 443 \
  -cert /etc/letsencrypt/live/diag.example.com/fullchain.pem \
  -key /etc/letsencrypt/live/diag.example.com/privkey.pem
```

or `make run TOKEN=secret CERT=fullchain.pem KEY=privkey.pem`.

## Dashboard

The collector also serves a dashboard, by default on `http://127.0.0.1:8081`.
It shows:

- "Combined stats": the number of devices that sent a reading in the last 24
  hours, and the average of the device maximums over the last 24 hours,
  2 days, 7 days and all time;
- a card for each device, with the last 3 bytes of its MAC address
  (`00:17:df`), its maximum over 24 hours, 2 days and 7 days, and when its last
  reading came. Cards of devices with no reading in 24 hours are dimmed.

A device's maximum for a period is the highest value of all its temperature
sensors (every key that ends in `_c`) over all its readings in that period.
The average for a period is the mean of these maximums, one per device that
sent a temperature in the period. Other keys (`fan1_rpm`, `usb_pd_w`) and
`null` values are not temperatures.

Click a card to see that device only. The device page shows:

- "Viewing 00:17:df" and a button back to all devices;
- its readings in the last 24 hours, its last reading, and its maximum over
  24 hours, 2 days, 7 days and all time (the average of one device's maximum
  is that maximum, so the device page shows no average);
- a line chart of the hottest sensor of each reading in the last 7 days;
- a line chart with one line per temperature sensor (its value in each
  reading of the last 7 days) and a legend;
- a table of all its readings in the last 7 days, newest first: one row per
  reading, one column per numeric key (temperatures, fan and power), as the
  router sent them. A dash means that the reading had no value.

In the charts, each reading is one point at the time the collector received
it. A line joins readings up to 45 minutes apart; a longer gap breaks it. The
time axis ends now. It starts 7 days before, or, for a device with less than
7 days of readings, at its first reading, so that a few readings do not crowd
into a few pixels (at least 30 minutes). Point at a chart to see the nearest
reading's values, hottest first (a small script in the page).

The page shows all times in the time zone of the computer that runs the
collector (for example CET), and names the zone under the title. Set `TZ` to
use another zone, for example `TZ=UTC go run . ...`. The JSONL file keeps UTC.

The page shortens sensor names. It drops `ls1046a_` and the `_c` at the end,
and shows:

| Key | Name on the page |
|---|---|
| `ls1046a_ddr_controller_c` | `ddr_ctrl` |
| `ls1046a_a72_cluster_c` | `cpu` |
| `tmp_u58000_local_c`, `tmp_u58000_remote_c` | `tmp0_local`, `tmp0_remote` |
| `tmp_u58001_local_c`, `tmp_u58001_remote_c` | `tmp1_local`, `tmp1_remote` |
| `gpy_u11000_c`, `gpy_u12000_c`, `gpy_u13000_c` | `phy1`, `phy2`, `phy3` |
| `sfp_p16000_c`, `sfp_p17000_c` | `sfp1`, `sfp2` |

Point at a table column name to see the full key. The JSONL file keeps the
full keys.

The dashboard leaves out `tmp_u58001_remote_c` (`tmp1_remote`): that sensor
is not connected on the Mono Gateway and always reads 0. It is in no chart,
table or maximum. The JSONL file still has it.

The periods use the time when the collector received each reading, not the
router's clock.

At startup the collector reads the whole `-out` file into memory: every
numeric value of every reading (about 32 MB for 100 routers over a month). Each stored
reading is then added as it comes, so the dashboard needs no restart.

The page uses the Catppuccin Mocha colors.

The dashboard has no login and uses plain HTTP. Keep it on `127.0.0.1` and open
it through an SSH tunnel from another computer:

```
ssh -L 8081:127.0.0.1:8081 user@collector-host
```

Then open `http://localhost:8081`. Use `-dash ""` to turn the dashboard off.

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
| `-cert`   | none          | TLS certificate file (PEM). With `-key`, serve HTTPS. |
| `-key`    | none          | TLS private key file (PEM). With `-cert`, serve HTTPS. |
| `-dash`   | `127.0.0.1:8081` | Dashboard address, plain HTTP with no login. Empty value turns it off. |

Give `-cert` and `-key` together, or neither. Without them the collector serves
plain HTTP.

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
