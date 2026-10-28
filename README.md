# iCal Splitter

Split two iCalendar (iCal) feeds into three disjoint calendars: events found only in feed A, events found only in feed B, and events shared by both feeds.

The service fetches both subscribed feeds on startup and refreshes them periodically. It serves the generated calendars over HTTP, intended to sit behind a reverse proxy such as nginx.

## Calendar endpoints

All three calendar URLs use independently configured secret paths; there are no fixed `/first-only.ics`, `/second-only.ics`, or `/shared.ics` routes.

| Calendar    | Contents                                                | URL path                        |
| ----------- | ------------------------------------------------------- | ------------------------------- |
| First-only  | Events in `FIRST_ICAL_URL` but not in `SECOND_ICAL_URL` | `/<FIRST_ENDPOINT_SECRET>.ics`  |
| Second-only | Events in `SECOND_ICAL_URL` but not in `FIRST_ICAL_URL` | `/<SECOND_ENDPOINT_SECRET>.ics` |
| Shared      | Events present in both feeds                            | `/<SHARED_ENDPOINT_SECRET>.ics` |

Replace each placeholder with the corresponding secret's actual value to form the complete URL, for example `https://calendar.example.com/<secret>.ics`.

Two diagnostic endpoints are also available:

- `/healthz` — liveness check. Returns HTTP 200 when the service's HTTP handler is running.
- `/readyz` — returns HTTP 200 after a successful refresh; returns HTTP 503 before the first successful refresh.

Until the first successful refresh, calendar endpoints return HTTP 503. If a later refresh fails, the service keeps serving the last successfully generated calendars and logs the refresh failure.

## Configuration

Create a private `.env` file from the example:

```sh
cp .env.example .env
```

Set these values in `.env`:

| Variable                 | Required? | Description                                                                                         |
| ------------------------ | --------- | --------------------------------------------------------------------------------------------------- |
| `FIRST_ICAL_URL`         | Yes       | Absolute `http://` or `https://` URL for the first source calendar.                                 |
| `SECOND_ICAL_URL`        | Yes       | Absolute `http://` or `https://` URL for the second source calendar.                                |
| `FIRST_ENDPOINT_SECRET`  | Yes       | Secret path token for the first-only calendar.                                                      |
| `SECOND_ENDPOINT_SECRET` | Yes       | Secret path token for the second-only calendar.                                                     |
| `SHARED_ENDPOINT_SECRET` | Yes       | Secret path token for the shared calendar.                                                          |
| `REFRESH_INTERVAL`       | No        | Refresh frequency; defaults to `10m`. Accepts a positive Go duration such as `5m`, `30m`, or `1h`.  |
| `ADDR`                   | No        | HTTP listen address; defaults to `:8080`. The Compose files set it to `:8080` inside the container. |

Generate three different endpoint tokens. Run this command three times and use one output per secret variable:

```sh
openssl rand -hex 32
```

Each token must be 32–128 characters and may contain only letters, digits, `-`, and `_`. All three values must be different. Keep `.env` private and never commit real feed URLs or endpoint secrets to source control. Anyone who learns a calendar URL can access that feed, so use HTTPS and avoid logging full request paths.

## Run with Docker Compose (build from source)

Use this when running the code in the checked-out project. The development Compose file reads feed URLs and endpoint secrets from `.env` and builds the image locally:

```sh
cp .env.example .env
$EDITOR .env
docker compose -f docker-compose.dev.yaml up --build -d
```

Inspect the service and logs:

```sh
docker compose -f docker-compose.dev.yaml ps
docker compose -f docker-compose.dev.yaml logs -f
curl -i http://127.0.0.1:8080/healthz
curl -i http://127.0.0.1:8080/readyz
```

The service is published only on host loopback at `127.0.0.1:8080`. This allows a host nginx instance to proxy requests to it without exposing the container port directly to the network.

## Run the published GHCR image

The default `docker-compose.yml` runs `ghcr.io/marcodemartino/ical-splitter:latest` and does not build source locally. It reads the same values from `.env`:

```sh
cp .env.example .env
$EDITOR .env
docker compose pull
docker compose up -d
```

To update the deployed image after a new version is published:

```sh
docker compose pull && docker compose up -d
```

## How matching works

Events are compared using their content after unfolding iCalendar lines and normalizing property order and parameter order. The event-level `DTSTAMP` property is ignored for matching, so otherwise-identical events that differ only in `DTSTAMP` count as shared. `UID`, `DTSTART`/`DTEND`, `SUMMARY`, `RRULE`, `EXDATE`, `URL`, `LOCATION`, `DESCRIPTION`, alarms, and all other event properties remain part of the identity. Matching `UID` values alone is not sufficient when other content differs.

Repeated identical events within a single source are collapsed. If copies differ only in `DTSTAMP`, the copy with the lexically newest `DTSTAMP` is kept for that source. Shared events are emitted from the first source. The chosen event block's original folded lines and properties are preserved; output line endings are normalized to CRLF.

The first feed's calendar-level properties are used for all outputs. `VTIMEZONE` components are merged from both feeds; if the same `TZID` appears in both, the first definition wins. This service is designed for appointment feeds containing `VEVENT` and `VTIMEZONE` components; other top-level component types are omitted.

## Development and tests

Go 1.23 or newer is required for building locally. Run the test suite and static checks with:

```sh
make check
go test ./...
go vet ./...
go build ./...
```

The tests cover shared events with different `DTSTAMP` values, first-only and second-only events, changed event content, folded lines, timezone merging, duplicate records within a feed, malformed calendars, and HTTP endpoint behavior.
