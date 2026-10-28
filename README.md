# iCal Splitter

A small Go service that fetches two subscribed iCalendar feeds and exposes three generated feeds:

- `/first-only.ics`: appointments present only in `FIRST_ICAL_URL`.
- `/second-only.ics`: appointments present only in `SECOND_ICAL_URL`.
- `/shared.ics`: appointments present in both feeds.

`/healthz` is a liveness endpoint. `/readyz` reports whether at least one successful refresh has completed.

## Matching rules

Events are compared using their content after unfolding iCalendar lines and normalizing property order and parameter order. The `DTSTAMP` property on the event is ignored for matching, so otherwise-identical events that differ only in `DTSTAMP` (as in the supplied example) count as shared. `UID`, DTSTART/DTEND, SUMMARY, RRULE, EXDATE, URL, LOCATION, DESCRIPTION, alarms and every other property remain part of the identity. An event whose UID matches but whose other content differs is not considered a match.

Repeated identical events within a single source are collapsed. If those copies differ only in DTSTAMP, the one with the lexically newest DTSTAMP is used for that source. Shared events are emitted from the first source. Original folded lines and event properties are preserved in the chosen event block. Output line endings are normalized to CRLF.

The first feed's calendar-level properties are used for all outputs. VTIMEZONE components are merged from both feeds; the first definition wins if the same TZID appears in both. This is designed for appointment feeds containing VEVENT and VTIMEZONE components; other top-level component types are omitted.

## Configure

```bash
cp .env.example .env
$EDITOR .env
```

Set `FIRST_ICAL_URL` and `SECOND_ICAL_URL` to the actual subscription URLs. Keep `.env` private and do not commit it: subscription URLs can contain access tokens.

`REFRESH_INTERVAL` is optional and defaults to `10m`. It accepts a Go duration such as `5m`, `30m`, or `1h`.

## Run locally

```bash
make check
FIRST_ICAL_URL='https://your-source/first.ics' \
SECOND_ICAL_URL='https://your-source/second.ics' \
REFRESH_INTERVAL=10m \
make run
```

Once both sources fetch and parse successfully, visit `http://127.0.0.1:8080/readyz` and then one of the `.ics` endpoints. Before the first successful refresh, calendar endpoints return HTTP 503. If a later refresh fails, the service continues serving the last successful output.

## Run in Docker

```bash
docker compose up --build -d
docker compose logs -f
docker compose ps
curl -i http://127.0.0.1:8080/healthz
curl -i http://127.0.0.1:8080/readyz
curl -i http://127.0.0.1:8080/shared.ics
```

The container is published only on host loopback (`127.0.0.1:8080`). Put nginx in front of it or change the bind address intentionally if remote access is needed. Refreshing happens every 10 minutes by default; the first refresh is attempted immediately at startup.

## Tests

```bash
make check
```

The tests cover shared events with different DTSTAMPs, first-only and second-only events, changed event content, folded lines, timezone merging, duplicate records within a feed, malformed calendars and HTTP endpoint behavior.
