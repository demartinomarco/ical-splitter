# iCal Splitter

A small Go service that fetches two subscribed iCalendar feeds and exposes three generated feeds:

- `/first-only.ics`: appointments present only in `FIRST_ICAL_URL`.
- `/second-only.ics`: appointments present only in `SECOND_ICAL_URL`.
- `/<SHARED_ENDPOINT_SECRET>.ics`: appointments present in both feeds (secret URL path).

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
curl -i http://127.0.0.1:8080/$(printf %s "$SHARED_ENDPOINT_SECRET").ics
```

The container is published only on host loopback (`127.0.0.1:8080`). Put nginx in front of it or change the bind address intentionally if remote access is needed. Refreshing happens every 10 minutes by default; the first refresh is attempted immediately at startup.
## Publish to GitHub Container Registry (GHCR)

The workflow at `.github/workflows/publish-ghcr.yml` runs formatting checks, tests, `go vet`, and a build on pushes and pull requests. On pushes (and manual workflow runs), it builds and publishes a multi-platform image for `linux/amd64` and `linux/arm64` to `ghcr.io/<owner>/<repository>`.

Image tags are generated automatically:

- `latest` for the repository's default branch.
- The branch name for branch pushes (for example, `main`).
- The Git tag for tag pushes (for example, `v1.2.3`).
- `sha-<short-commit>` for an immutable commit-specific reference.

No manually configured registry password is needed in Actions; the workflow uses `GITHUB_TOKEN` with `packages: write`. The first successful publish creates the container package. Check the package's visibility in GitHub. For pulls without authentication, make the package public; for a private package, log in to GHCR on the deployment machine with a GitHub token that has `read:packages` permission.

## Deploy the published image with Compose

On the deployment machine, copy `.env.example` to `.env` and edit it:

```bash
cp .env.example .env
$EDITOR .env
```

Set `GHCR_IMAGE` to the published package name, e.g. `ghcr.io/your-github-owner/your-repository:latest`, and set both iCal URLs. Keep `.env` private; do not commit it.

For a public image, no registry login is required. For a private image, authenticate first:

```bash
echo "$GHCR_TOKEN" | docker login ghcr.io -u YOUR_GITHUB_USERNAME --password-stdin
```

Use a GitHub personal access token with `read:packages` for `GHCR_TOKEN`; do not put the token in the Compose file or `.env`.

Start the published image (this deployment file does not build source locally):

```bash
docker compose -f compose.ghcr.yaml pull
docker compose -f compose.ghcr.yaml up -d
docker compose -f compose.ghcr.yaml ps
docker compose -f compose.ghcr.yaml logs -f
```

To update to the newest image referenced by `GHCR_IMAGE`:

```bash
docker compose -f compose.ghcr.yaml pull && \
  docker compose -f compose.ghcr.yaml up -d
```

The service binds to `127.0.0.1:8080` on the deployment host, ready for a host nginx reverse proxy. The Compose file passes the feed URLs to the container from `.env` and requires them to be set before starting.

## Tests

```bash
make check
```

The tests cover shared events with different DTSTAMPs, first-only and second-only events, changed event content, folded lines, timezone merging, duplicate records within a feed, malformed calendars and HTTP endpoint behavior.


## Secret calendar endpoint paths

All three calendar routes use secret URL paths. Configure `FIRST_ENDPOINT_SECRET`, `SECOND_ENDPOINT_SECRET`, and `SHARED_ENDPOINT_SECRET` with different, randomly generated URL-safe tokens. The service exposes the first-only feed at `/<FIRST_ENDPOINT_SECRET>.ics`, the second-only feed at `/<SECOND_ENDPOINT_SECRET>.ics`, and the shared feed at `/<SHARED_ENDPOINT_SECRET>.ics`.

Generate tokens with:

```sh
openssl rand -hex 32
openssl rand -hex 32
```

The service requires each token to be 32-128 characters, using only letters, digits, `-` or `_`; all three values must differ. Do not commit real tokens to source control. Anyone who knows a secret endpoint URL can access that calendar, so use HTTPS and avoid logging full request paths.
