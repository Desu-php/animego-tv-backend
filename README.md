# AnimeGO TV backend

Go backend for a personal Android / Google TV anime client. It provides health
checks, normalized AnimeGO search, anime details, episodes, and each episode's
translations/player choices, and on-demand direct playback sources for AniBoom,
Kodik, and CVH.
The TV client consumes normalized HLS/DASH/MP4 sources through Media3 without a WebView.

## Architecture

`Gin HTTP handlers → AnimeService → AnimeProvider → AnimeGO HTTP client / HTML parser`

- `cmd/api`: configuration, dependency wiring, structured `slog` logging, graceful shutdown.
- `internal/domain`: normalized catalog models, `Playback`, `VideoSource`, and typed errors.
- `internal/service`: query/identifier validation, use-case-owned ports, and provider orchestration.
- `internal/cache`: bounded TTL/LRU storage and duplicate-load suppression; mutable results are cloned.
- `internal/provider`: the catalog interface and upstream error categories.
- `internal/provider/animego`: bounded HTTP requests and isolated goquery parsing.
- `internal/player`: resolver registry, HTTP host/DNS restrictions, manifest checks, and MP4 metadata verification.
- `internal/player/aniboom`, `kodik`, `cvh`: isolated provider protocols.
- `internal/transport/http`: JSON responses, error mapping, request logging, panic recovery.

Playback uses `PlaybackService → AnimeProvider.GetPlayer → resolver registry →
provider resolver → VideoSource`. The provider rechecks episode ownership and
reads the current menu; private iframe URLs remain internal. Android connects
directly to the returned media CDN. The backend fetches configuration,
manifests and MP4 HEAD metadata only; it never fetches video segments,
proxies media, or stores URLs.
See [the investigation notes](docs/animego.md#playback-resolution) for the observed flow.

## Run locally

Requires Go 1.25 or later and network access to AnimeGO.

```sh
go mod download
go run ./cmd/api
```

| Environment variable | Default | Meaning |
| --- | --- | --- |
| `HTTP_ADDR` | `:8080` | Listen address with a numeric port |
| `ANIMEGO_BASE_URL` | `https://animego.me` | Current HTTP(S) origin; no path, credentials, query, or fragment; redirects to another origin are blocked |
| `ANIMEGO_TIMEOUT` | `10s` | Positive Go duration bounding each complete operation, including episode pagination |
| `PLAYER_TIMEOUT` | `10s` | Positive Go duration bounding the complete embed/manifest resolution stage |
| `CATALOG_CACHE_CAPACITY` | `128` | Maximum entries per catalog cache; `0` disables caching |
| `CATALOG_DETAILS_TTL` | `2m` | Positive TTL for parsed anime details |
| `CATALOG_EPISODES_TTL` | `15s` | Positive TTL for parsed episode lists |
| `CATALOG_CONCURRENCY` | `16` | Maximum simultaneous catalog API operations |
| `PLAYBACK_CONCURRENCY` | `4` | Maximum simultaneous playback API operations |

Set `GIN_MODE=release` to suppress Gin's development startup output. Application
logs use JSON on stdout. Invalid configuration stops startup with an error.

## Run with Docker

```sh
docker compose up --build
```

Optional overrides can be set in the shell or a local `.env` file:

```dotenv
ANIMEGO_BASE_URL=https://animego.me
ANIMEGO_TIMEOUT=10s
PLAYER_TIMEOUT=10s
```

The multi-stage image contains a static binary and trusted CA certificates,
runs as a non-root user, and listens on port 8080. Catalog and playback have independent
concurrency limits; saturated API requests return `503` immediately, while health and docs
remain accessible. Actual AnimeGO requests have a separate shared limit equal to
`CATALOG_CONCURRENCY + PLAYBACK_CONCURRENCY`; detached cache loads retain their
slots until the upstream response is closed. Compose uses a read-only
filesystem and drops Linux capabilities.

```sh
docker compose down
```

## API

Interactive Swagger documentation is available after starting the server:

- [Swagger UI](http://localhost:8080/swagger/index.html) — endpoint descriptions,
  request parameters, response models, examples, errors, and **Try it out**.
- [OpenAPI JSON](http://localhost:8080/openapi.json) — the same contract for
  importing into API tools or generating a client.
- [Android TV integration guide](docs/android-tv.md) — the client flow,
  networking, optional fields, and playback behavior.

The OpenAPI 3.0.3 contract is maintained in [docs/openapi.json](docs/openapi.json).
Swagger UI assets are embedded in the Go binary, including Docker builds;
opening documentation requires no CDN access or extra services. **Try it out**
uses the server serving the documentation; catalog/playback requests still need
access to the upstream providers. Documentation is also accessible at `/swagger/`.

| Endpoint | Response |
| --- | --- |
| `GET /health` | `200 {"status":"ok"}` (process health; no upstream request) |
| `GET /api/v1/anime/search?q=solo` | `200 {"data":[...]}` |
| `GET /api/v1/anime/:id` | `200 {"data":{...}}` with anime metadata |
| `GET /api/v1/anime/:id/episodes` | `200 {"data":[...]}` with available episode IDs/numbers |
| `GET /api/v1/anime/:id/episodes/:episodeID/translations` | `200 {"data":[...]}` with translations and nested player references |
| `GET /api/v1/anime/:id/episodes/:episodeID/players/:playerID/playback` | `200 {"data":{...}}` with fresh normalized media sources; `Cache-Control: no-store` |

```sh
curl "http://localhost:8080/health"
curl --get "http://localhost:8080/api/v1/anime/search" --data-urlencode "q=solo"
curl --get "http://localhost:8080/api/v1/anime/search" --data-urlencode "q=Поднятие уровня"
```

On Windows PowerShell use `curl.exe` for these examples.

```json
{
  "data": [
    {
      "id": "podnyatie-urovnya-v-odinochku-s1-2477",
      "title": "Поднятие уровня в одиночку",
      "alt_title": "Ore dake Level Up na Ken",
      "poster": "https://img.cdngos.com/v/250x350/anime/67/676487b86d5c0521559278",
      "year": 2024
    }
  ]
}
```

`id` is the provider's detail-page slug, including its numeric identifier.
Optional fields are omitted when unavailable. An empty search returns
`{"data":[]}`. `q` is trimmed and must contain 1–200 valid Unicode code points.

Errors use `{"error":"..."}`: `400` for invalid queries/identifiers, `404` for
missing anime, episodes, or player tuples (including an episode belonging to another anime),
`422` for unsupported players or unavailable/protected playback, `503` with `Retry-After: 1` for API or upstream concurrency limits, `504` for operation timeouts, `502` for upstream
HTTP, response-size, or parsing failures, and `500` for unexpected
internal failures. Details are logged server-side and never sent to clients.

## Verify

```sh
go fmt ./...
go vet ./...
go test ./...
docker build -t animego-backend .
```

Unit tests use minimal HTML fixtures, fake providers, and `httptest` servers;
they do not contact AnimeGO or player providers. The integration test covers
search through playback against local TLS fixture servers. CI requires no upstream availability.

## Search → details → episode → translation/player → playback

For an optional manual live check, start the API and search for `solo`. Copy the
returned `id`, then use it for details and episodes. These example IDs were
verified on 2026-10-04; upstream availability and IDs may change.

```sh
curl "http://localhost:8080/api/v1/anime/search?q=solo"
curl "http://localhost:8080/api/v1/anime/podnyatie-urovnya-v-odinochku-s1-2477"
curl "http://localhost:8080/api/v1/anime/podnyatie-urovnya-v-odinochku-s1-2477/episodes"
curl "http://localhost:8080/api/v1/anime/podnyatie-urovnya-v-odinochku-s1-2477/episodes/32410/translations"
curl "http://localhost:8080/api/v1/anime/podnyatie-urovnya-v-odinochku-s1-2477/episodes/32410/players/32410:67:24/playback"
curl "http://localhost:8080/api/v1/anime/search?q=zxqvnonexistent739182"
```

A translation can have several players:

```json
{
  "data": [
    {
      "id": "67",
      "name": "Amber",
      "players": [
        {"id": "32410:67:26", "provider": "cvh", "name": "CVH"},
        {"id": "32410:67:24", "provider": "aniboom", "name": "AniBoom"},
        {"id": "32410:67:19", "provider": "kodik", "name": "Kodik"}
      ]
    }
  ]
}
```

Translation and player IDs are opaque client identifiers. Select a player with
`provider: "aniboom"`, `"kodik"`, or `"cvh"` from the returned translation's `players`, then pass its
`id` to playback. The backend reselects it from the fresh episode menu.
Iframe references are private resolver input. Translation `type` is omitted because the inspected menu
does not explicitly distinguish voice/subtitles.

Playback JSON (the media path below is synthetic; resolve your own fresh URL):

```json
{
  "data": {
    "anime_id": "podnyatie-urovnya-v-odinochku-s1-2477",
    "episode_id": "32410",
    "translation_id": "67",
    "player_id": "32410:67:24",
    "sources": [
      {
        "type": "hls",
        "url": "https://ella.ya-ligh123.site/sample/master.m3u8",
        "quality": "auto"
      }
    ]
  }
}
```

Media3 only needs `sources[].type`, `url`, and optional `headers`. AniBoom's
inspected manifests worked without Referer, Origin, cookies, or a special
User-Agent, so `headers` is omitted. Reliable expiration metadata was absent,
so `expires_at` is omitted; URLs must be resolved on demand and treated as
potentially temporary. HLS is preferred; DASH is used when the configuration
does not supply HLS. Kodik returns quality-specific HLS sources, ordered by
quality from highest to lowest. CVH prefers adaptive HLS/DASH, and returns direct
MP4 qualities when neither adaptive source is supplied. MP4 metadata is checked
with HEAD without downloading video. `quality: "auto"` denotes an adaptive
manifest; values such as `"720"` and `"1080"` denote explicit quality choices.
CVH selects the wrapper's exact episode, season when supplied, and voice studio;
a missing or ambiguous choice is not replaced with another translation.
Some provider URLs carry expiration and public-IP binding parameters. Resolve
fresh URLs on demand; backend and TV may need the same public egress address.

The backend must supply the public AnimeGO origin Referer when fetching AniBoom
embed HTML; this host-specific header is not needed on the direct media source.

Unknown players return `422`. Unavailable selected sources, encrypted HLS, or DASH with
ContentProtection also returns `422`; there is no protected-playback fallback.
An unexpected provider page/response/manifest or failed upstream request returns `502`;
an operation timeout returns `504`.
For Kodik and CVH, a failed quality is omitted when another checked quality works;
request cancellation and deadline failures still stop the entire operation.
HLS checks include referenced video/audio/subtitle/I-frame playlists with at most 32 manifest
requests and depth 2 per source, using final redirect URLs as reference bases. Video segments
are never downloaded. Unencrypted `EXT-X-KEY:METHOD=NONE` is accepted.

`episode_count` in anime details is catalog metadata, which can describe a
planned season. The episodes endpoint returns the actual player episode list,
gathering all pages and sorting by number. Episode titles are omitted because
the list response only supplies IDs and numbers; fetching every episode's
video menu for titles would add unnecessary upstream requests.

Valid anime IDs are ASCII lowercase letter/digit/hyphen slugs of at most 300
characters, without leading/trailing hyphens. Episode IDs are positive numeric
identifiers of at most 18 digits. Invalid IDs never reach the upstream.

Empty episode or translation lists return `{"data":[]}`. A valid catalog entry
without an online player section has an empty episode list. Unexpected markup
inside an existing player section produces `502`. Requests are bounded to
4 MiB per response, 10,000 episodes, 100 pages, and the configured operation
timeout. Parsed anime details and episode lists use bounded in-memory TTL/LRU caches.
Concurrent misses for the same key share a bounded upstream load; cancelling one caller
never cancels another caller. Returned values are cloned to prevent accidental cache mutation.
Episode ownership is checked against the catalog list, refreshing upstream when
the requested episode is missing. Successful refreshes update the cache and are
shared for one second to bound repeated probes of nonexistent IDs. Removed
episodes can remain listed until the episode TTL expires.
Search results, translation menus, embed configuration, and signed media URLs are not cached.

External embed requests require HTTPS and the exact hosts `aniboom.one` or
`kodikplayer.com`; the CVH wrapper stays on the configured AnimeGO origin,
and CVH API requests use `plapi.cdnvideohub.com`. Media URLs are
discovered from trusted provider responses; CDN hosts are not
listed or inferred from a naming pattern. New subdomains and entirely new CDN
domains work automatically when the provider supplies them. A separate media HTTP
client requires HTTPS, standard ports, no credentials/fragments, validated DNS
names, and public DNS answers pinned at connection time. Each redirect repeats
these checks. Private, loopback, link-local, metadata, and special-use addresses
are rejected. Media requests carry no embed Referer or credentials. No arbitrary
URL resolution endpoint or environment proxy is used. Manifest references use
the same media URL syntax checks; subsequent Android requests are outside the
backend's connection-time DNS validation.
Resolution logs record provider, stage, identifiers, media type, duration, and
upstream status without complete media URLs, tokens, cookies, HTML, or manifests.
