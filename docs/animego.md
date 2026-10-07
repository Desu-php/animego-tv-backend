# AnimeGO search investigation

Inspected on **2026-10-04, Asia/Dushanbe**, using live HTTP requests and the
site's delivered HTML and JavaScript. No endpoint or selector was inferred
from an older scraper or a similarly named mirror.

## Origin and flow

1. [`https://animego.org/`](https://animego.org/) returned `301` to
   [`https://animego.me/`](https://animego.me/), which returned `200` HTML.
2. The navbar contains a GET form with `action="/search/all"` and `name="q"`.
3. The live Stimulus `site-search` controller in `/build/866.d65997d8.js`
   performs autocomplete with `q` and `navbar=true`, requesting JSON.
   A confirmed request to `/search/all?q=solo&navbar=true` with
   `Accept: application/json` and `X-Requested-With: XMLHttpRequest` returned
   `{"status":"success","data":{"content":"...HTML fragment..."}}`.
   `Accept: application/json` alone returned a full HTML document.
4. The full search page links to the anime-only tab
   [`/search/anime?q=solo`](https://animego.me/search/anime?q=solo).
   Its own form has `action="/search/anime"`, `method="GET"`, and input `q`.
5. The backend uses that **anime-only full-page HTML** flow with
   `Accept: text/html`. It avoids navbar results, which mix entity types and
   showed only six anime for `solo`; the anime tab showed thirteen.

Requests use `net/url.Values` encoding, preserving Cyrillic and other Unicode
queries. No cookies, login, browser execution, or session bootstrap were needed
for the verified full-page search requests. The client sets a project User-Agent.

## Verified HTML contract

| Purpose | Observed selector / field |
| --- | --- |
| Search root | `.search-entities` |
| Confirm anime-only search | `form.search-form[action="/search/anime"] input[name="q"]` |
| Result collection | `.ani-list` |
| Individual card | `.ani-grid__item` |
| Title and detail link | `.ani-grid__item-title a` (text and `href`) |
| Alternative title | `.ani-grid__item-body > .fw-lighter` |
| Poster | `.ani-grid__item-picture img` (`src`) |
| Year | Four-digit year among `.ani-grid__item-genres__link` values |
| Explicit empty state | `.alert.alert-warning[role="alert"]` with `ничего не найдено` |

The first `solo` result had:

- Detail path: `/anime/podnyatie-urovnya-v-odinochku-s1-2477`.
- Title: `Поднятие уровня в одиночку`.
- Alternative title: `Ore dake Level Up na Ken`.
- Poster: `https://img.cdngos.com/v/250x350/anime/67/676487b86d5c0521559278`.
- Year: `2024`.

Only a detail-page slug is exposed as the normalized ID. It is independent of
the displayed title and can be appended to `/anime/` in a future detail provider.
Status was absent from the inspected search cards and is omitted rather than
inferred. Relative poster URLs are resolved against the final response URL;
only HTTP(S) poster URLs and same-origin anime detail links are accepted.

The request
[`/search/anime?q=zxqvnonexistent739182`](https://animego.me/search/anime?q=zxqvnonexistent739182)
returned `200 text/html` with a search form, no result list, and a warning saying
the query found nothing. The parser requires that explicit empty state;
missing selectors alone do not mean zero results. The echoed query inside
`strong` is removed before checking the empty-state phrase.

## Assumptions and limits

- The base origin can change. Configuration changes only the origin; a mirror
  with different routes or markup requires a new investigation and parser update.
- Form actions, CSS classes, `img src`, and the Russian empty-state phrase can
  change. Such changes can produce `502` until the integration is updated.
- Optional fields can disappear without breaking a card. Cards missing a valid
  detail link or title are skipped; all malformed cards cause a parsing failure.
- The slug includes a numeric identifier but can change if the site's URLs are
  renamed. IDs are stable within the observed upstream catalog, not a promised
  globally permanent identifier.
- Search ranking and matching belong to AnimeGO. This MVP returns the current
  HTML response's results. It does not implement pagination; no pagination was
  present in the inspected thirteen-result `solo` page.
- CAPTCHA, anti-bot HTML, login walls, non-HTML bodies, non-2xx responses, network
  failures, and responses over 4 MiB are upstream errors. There is no browser
  automation fallback or retry loop.
- Phase 1 did not parse details, episodes, translations, player scripts, or
  streams. The Phase 2 and Phase 3 extensions are documented below.

Minimal excerpts of the confirmed positive and empty responses are in
`internal/provider/animego/testdata/`. Unit tests never use the live site.

## Phase 2: details, episodes, translations, and players

Investigated on **2026-10-04, Asia/Dushanbe**, starting from the live `solo`
search result `podnyatie-urovnya-v-odinochku-s1-2477`. The current delivered
`/build/866.d65997d8.js` was inspected again, including the
`anime-player-loader`, `anime-player-episodes`, and `anime-player` controllers.
No browser or third-party player execution was needed.

### Observed request chain

`Search → anime HTML → player bootstrap JSON/HTML → episode-page JSON/HTML → episode-video JSON/HTML → translation/player choice`

| Method | Endpoint | Parameters | Important headers | Response and fields used |
| --- | --- | --- | --- | --- |
| GET | `/search/anime` | `q` | `Accept: text/html`, project User-Agent | Full search HTML, as documented above |
| GET | `/anime/{slug}` | The search result's slug in the path | `Accept: text/html`, project User-Agent | Full detail HTML and supplementary `application/ld+json` |
| GET | `/player/{numericAnimeID}` | Numeric ID from the detail page's loader attribute | `Accept: application/json`, `X-Requested-With: XMLHttpRequest` | `status: success`, `data.content`: player HTML with episode URL, total/page-size/range attributes, and initial menu |
| GET | `/player/{numericAnimeID}/episodes` | `page` (zero-based), or `number` to locate the page containing a numbered episode | `Accept: application/json`, `X-Requested-With: XMLHttpRequest` | `status: success`; `data.content`, `page`, `pageSize`, `total`, `ranges` |
| GET | `/player/videos/{episodeID}` | Stable numeric episode ID in the path | `Accept: application/json`, `X-Requested-With: XMLHttpRequest` | `status: success`; `data.numVideos`, `data.content` (menu), `data.episode_information` (optional HTML) |

The implementation also sends a same-origin anime-page `Referer` on episode
and video requests. Verified requests worked without session cookies or a
Referer. The JSON player endpoints require the AJAX header: an episode request
with `Accept: application/json` alone returned a not-found response. No cookie
jar or temporary hardcoded cookies were therefore added.

The backend skips the player-bootstrap request: after extracting the validated
`/player/{numericAnimeID}` loader path, it directly calls the independently
verified `/player/{numericAnimeID}/episodes` route. It does not parse the initial
bootstrap's selected episode, watched state, or personalized menu.

### Anime metadata

The page contains one `.entity`, its title at `.entity__title h1`, its poster
at `.entity__poster img`, and a canonical anime path in `link[rel="canonical"]`.
These anchor the detail response and distinguish it from challenges or unrelated pages.

Supplementary JSON-LD exposed `@type: TVSeries`, `alternateName`, `description`,
`genre`, `datePublished`, and `numberOfEpisodes`. The description is complete,
unlike the truncated SEO description meta tag. A `Movie` JSON-LD type is also
recognized. The normalized type is `tv`/`movie` when identified by JSON-LD;
otherwise the visible type label is retained.

Visible metadata under `.entity__info .entity-field` consists of adjacent label
and value divs. HTML fallback uses `Тип`, `Эпизоды`, `Жанры`, and `Сезон`.
Status comes from the path segment of its `/anime/status/{status}` link;
the inspected finished title used `released`. Optional metadata may be absent.
JSON-LD's alternative title here was `Solo Leveling`; search used its romanized
Japanese title. They are both upstream-provided alternative titles.

The loader is `[data-controller~="anime-player-loader"]` with
`data-anime-player-loader-url-value="/player/2477"`. The numeric ID must match
the suffix of the canonical detail slug. Arbitrary loader URLs are rejected.
An existing `#player` section without its expected loader is a structural error;
a valid catalog page without an online player section returns no available episodes.

### Episodes and pagination

The episode JSON contains an HTML fragment. Each
`.player-video-bar__item[data-anime-player-episodes-target~="episode"]` provides:

- `data-episode`: stable numeric record ID, e.g. `32410`.
- `data-episode-number`: displayed series number, e.g. `1`.
- Other attributes include page/index/type/watched state; these remain provider details.

No title was supplied on the inspected list cards. The optional title appears
separately in a video response's `.episode-info__name .episode-info__value`
(episode 32410 showed `I'm Used to It`). The backend omits list titles rather
than issuing one video request per episode. The visible schedule on the anime
page is not used as availability: it is separately paginated and describes release dates.

Confirmed examples:

- Solo Leveling: `total=12`, `page=0`, `pageSize=100`; episode IDs 32410–32421.
- One Piece, search ID `van-pis-s1-65`: `total=1180`, `pageSize=100`;
  pages zero and one were independently inspected, and the API fetched all twelve pages.
- An out-of-range page request for Solo Leveling was clamped back to page zero.
  The backend checks returned page numbers and fails on unexpected repeats.

The backend validates declared counts, pagination consistency, and unique IDs,
then sorts episodes by number. It bounds a complete operation by
`ANIMEGO_TIMEOUT`, with at most 10,000 episodes and 100 pages. Changing totals
mid-request cause a structural error instead of silently returning partial data.

### Translation/player relationship

The episode's video JSON contains a menu with two related sets of buttons:

| Button | Attributes used |
| --- | --- |
| `[data-anime-player-target~="translation"]` | `data-translation-id`; display name from `span.text-truncate` |
| `[data-anime-player-target~="provider"]` | `data-provider`, `data-provider-slug`, `data-provider-title`, `data-translation-id`, `data-ptranslation`, `data-player` |

The two translation attributes on a provider button must agree and refer to
an existing translation. Available names are read from the response; no dub
list is hardcoded. A translation has several player choices, so the public
model is `Translation { id, name, players: [...] }`. The inspected menu did not
contain a reliable voice/subtitle type field; `type` remains omitted.

For episode `32410`, the live response contained **14 translations and 25 player
variants**. Observed provider slugs were `cvh`, `aniboom`, and `kodik`. The menu
included same-origin `/cdn-iframe/...` references for CVH, AniBoom embed
references, and Kodik series-player references. These are iframe/player URLs,
not final playable streams. Phase 2 inspected these as attributes only;
Phase 3 ordinary-request investigation and supported resolution are documented below.

A public player ID combines the episode record ID, translation record ID, and
provider record ID, e.g. `32410:67:19`. Clients treat this as opaque. Inside the
AnimeGO package, a `playerReference` retains that normalized reference, the
iframe URL, and the component IDs. Phase 3 re-reads the current menu and
select that tuple; iframe URLs are not cached, exposed in API JSON, or logged.
Duplicate tuples currently cause an error because the inspected menu had one
player reference per tuple. New provider behavior may require richer identifiers.

`numVideos=0` is handled as no available translations; malformed envelopes or
positive counts without matching menu choices cause `502`. The provider checks
episode membership in the requested anime's full episode list before requesting
its video menu. A foreign or missing episode returns `404` without that request.

### Errors, trust boundaries, and Phase 3 limits

- A confirmed missing anime URL returned HTTP 404 HTML; a missing episode-video
  URL returned HTTP 404 JSON. Resource operations map these to normalized `404` errors.
  A broken known episode-list endpoint remains an upstream `502`, not an empty list.
- Search behavior remains unchanged, except malformed slugs are skipped so IDs
  returned by search can safely be accepted by the detail endpoint.
- User IDs are validated in both the service and provider before constructing
  fixed route paths. No arbitrary URL endpoint exists.
- All HTTP requests and redirects stay on the configured origin. A changed
  origin, including configuring the older `animego.org` redirect, requires setting
  `ANIMEGO_BASE_URL` to the current final origin.
- One reusable client handles HTML and JSON, headers, status checks, bounded
  bodies, and cancellation. Logs include provider, operation, anime/episode IDs,
  upstream status, and duration, without response HTML, cookies, or iframe references.
- Phase 2 had no database, cache, authentication, stream resolution, player
  execution, or third-party HTTP request. Phase 3 adds the resolver described
  below with explicit host validation; it adds no storage or player execution.
- Fixtures retain minimal inspected structure; iframe paths are replaced with
  synthetic test references. Automated parser, provider-flow, service, and handler
  tests use local fixtures/fakes/`httptest` only. Empty lists and metadata-free
  catalog entries are modeled locally and do not require a particular live title.

## Playback resolution

Investigated on **2026-10-04, Asia/Dushanbe** with ordinary HTTPS GET requests,
starting from Solo Leveling, anime slug `podnyatie-urovnya-v-odinochku-s1-2477`,
episode `32410`, translation `67` (Amber). No WebView, browser automation,
login, cookies, protected API calls, decryption, or access-control bypass was used.

### Current request chain

`AnimeGO anime → episodes → video menu → selected translation/provider tuple → AniBoom embed HTML → direct HLS master (or DASH MPD) → Android media CDN connection`

1. Re-read `/anime/{slug}` and `/player/2477/episodes?page=0` using the Phase 2
   client, verifying that the episode belongs to this anime.
2. GET `/player/videos/32410` with the existing AJAX headers. The current response
   still contained 14 translations and 25 player choices, including `cvh`,
   `aniboom`, and `kodik`. The selected AniBoom tuple is `32410:67:24`;
   `data-provider="24"` identifies AniBoom. `data-player` is an iframe URL,
   not a media URL. Exact availability is title/episode dependent.
3. GET the selected HTTPS `aniboom.one/embed/{opaqueID}` URL with
   `Referer: https://animego.me/`. Without Referer, or with Origin alone, it
   returned 404; either the origin Referer or the anime-page Referer returned
   200. This is an ordinary public embed requirement, with no credentials.
   The inspected query carried `episode`, provider-specific `translation`, `episodes`, and
   `parent`; it is used as delivered, never reconstructed from guessed IDs.
   The response is HTML. One `video#video[data-parameters]` contains ordinary
   JSON in an HTML attribute. Its `hls` and `dash` fields are themselves JSON
   strings containing `src` and `type`; no JavaScript execution is needed.
4. The verified configuration exposed `application/x-mpegURL` with a
   `https://ella.ya-ligh123.site/lk/{opaqueID}/master.m3u8` URL, and
   `application/dash+xml` with a `.mpd` URL on that same host. The backend
   prefers HLS, or selects DASH when no HLS source is supplied.
   A follow-up check on 2026-10-04 confirmed that CDN selection varies by
   episode/translation: episode `32419`, translation `67`, uses
   `kira.ya-low1.site`; episode `32420`, translation `81`, uses
   `graham.ya-low1.site`. Both expose the same HLS/DASH configuration shape.
   Restricting the list to the first episode's CDN caused the logged
   `untrusted AniBoom media URL` failures. The resolver now discovers URLs
   from this trusted configuration without a CDN allowlist or hostname regex.
   A separate media HTTP client checks HTTPS syntax and public DNS addresses
   on each connection, including redirects. New CDN domains need no code change.
5. GET the chosen manifest to validate its structure and reject explicit
   protection markers/untrusted references. Return one normalized source with
   `quality: "auto"`; Android fetches all subsequent media directly.
   Neither source URLs nor manifests are cached or stored by the backend.

The HLS master returned HTTP 200 with `application/vnd.apple.mpegurl` and
adaptive variants at 360p, 480p, 720p, and 1080p, using AVC/AAC codec declarations.
An ordinary GET of one rendition playlist returned HTTP 200, `#EXTINF` entries,
and no `#EXT-X-KEY`. The master had no KEY or SESSION-KEY markers. No segments
or initialization media were requested. DASH returned HTTP 200,
`application/dash+xml`, an MPD with video representations and relative
SegmentTemplate references, and no ContentProtection element.

These observations establish usable manifest metadata; Android device playback
and every rendition/segment were not tested. The runtime only reads the chosen
top-level manifest. It rejects any HLS KEY/SESSION-KEY or DASH ContentProtection
encountered there and never obtains keys, DRM licenses, or session credentials.

### Headers, quality, and expiration

The HLS master worked with no playback headers, with Referer alone, and with
Origin alone. DASH and the inspected HLS rendition also worked with no extra
headers. No session cookies or special User-Agent were needed for these checks.
Accordingly the response omits `headers`. Only embed HTML requests carry the
verified AnimeGO origin Referer (from trusted `ANIMEGO_BASE_URL` configuration).
Manifest requests carry the project's ordinary User-Agent without Referer;
redirects reset Referer according to the target host's policy. No upstream
cookies or unrelated headers are forwarded.
This result applies to the inspected source and may change with provider policy.

The URLs supplied no reliable expiration timestamp. An opaque media path does
not establish permanence or signing; `expires_at` is omitted instead of guessed.
Playback lookup is fresh on every request, with `Cache-Control: no-store`.
HLS contains adaptive variants, so the backend returns its master rather than
separate rendition URLs. DASH also remains a direct manifest. No independent
MP4 source was exposed by the inspected AniBoom configuration. CVH's direct MP4
path is described below.

### Kodik and CVH resolution

| Provider | Ordinary-request observation | Resolution boundary |
| --- | --- | --- |
| AniBoom (`aniboom`, 24) | Embed HTML exposes plain JSON HLS/DASH sources | Exact embed host, dynamically discovered public HTTPS media URLs, and verified configuration; protected manifests/access-denied responses are unavailable |
| CVH (`cvh`, 26) | Wrapper supplies title, publisher, episode/season and selected voice; playlist/video API supplies HLS/DASH or MP4 | Exact selection, adaptive manifest validation, or MP4 HEAD metadata check |
| Kodik (`kodik`, 19) | Episode/movie iframe supplies current signed parameters; public bundle defines the source lookup endpoint | Ordinary form POST with issued parameters; HLS URLs per quality, validated without downloading segments |
| Other provider slugs | May appear on other anime/episodes | No registered resolver; returns unsupported |

Both protocols were verified through ordinary public requests on 2026-10-04.
Kodik reads `urlParams` from the selected `/seria/{id}/{hash}/{quality}` or
`/video/...` embed and obtains the same-origin source endpoint from the current
`app.player_single` bundle. It sends a bounded form POST using the issued
domain/referrer signatures unchanged. The player's URL serialization is ASCII
ROT18 followed by Base64 decoding; no JavaScript is executed and no media keys
or signatures are generated. The returned 720/480/360 HLS playlists are media
playlists rather than adaptive masters; each is checked, and sources are ordered
by quality. An adaptive `link` response is also supported.

CVH reads AnimeGO's `/cdn-iframe/...` wrapper through the catalog client's
configured-origin HTTP policy. It requests `/api/v1/player/sv/playlist` with
`pub`, `id`, optional `aggr`, `ep`, `sn`, and `vstu`, then selects the exact
episode/season and `priority-voice` (or explicit `only-voice`). It never silently
chooses a different voice. A missing choice returns unavailable; ambiguity is a
parsing failure. The selected `vkId` is passed to `/api/v1/player/sv/video/{vkId}`.
HLS is preferred, then DASH; if both are absent, explicitly supplied MP4 quality
URLs are returned after HEAD verifies HTTP success and `video/mp4`, without
reading the video body. Provider blocked tags and access-denied responses are
unavailable. Protected manifests do not fall back to MP4.

Live checks covered Kodik and CVH for episodes `32410`/`32419`, translation `67`,
and `32420`, translation `81`: Kodik returned 720/480/360 HLS, CVH returned
adaptive HLS for the first two and direct MP4 for the last. AniBoom was rechecked
as well. These checks validate source/manifest metadata, not Android device
playback. Some Kodik/CVH URLs carry expiration and public-IP binding parameters;
the TV may need the same public egress address as the resolving backend.

### API and resolver separation

`GET /api/v1/anime/:id/episodes/:episodeID/players/:playerID/playback`

The scoped endpoint fits Phase 2's nested player choices and validates anime,
episode, and player tuple identifiers before I/O. Missing/foreign resources
return 404; unsupported or inaccessible/protected playback returns 422;
supported-provider HTTP/structure failures return 502; unexpected failures
return 500. Error responses contain generic messages.

`AnimeProvider.GetPlayer` discovers a fresh internal `PlayerSelection`. The
registry selects an isolated resolver by provider slug. The AniBoom resolver
parses its own embed configuration and returns `VideoSource`; AnimeGO parsing
contains no third-party source extraction. The public `Playback` identifies
anime, episode, translation, and selected player by ID without duplicating the
translation metadata or exposing the embed URL. See README for full sample JSON
and the manual search-to-playback sequence.

### HTTP security and fragile assumptions

- AniBoom/Kodik embed requests are restricted to their exact HTTPS hosts
  `aniboom.one` and `kodikplayer.com`, each with its own HTTP client. Embed
  redirects cannot leave the provider host; form redirects cannot leave the
  original origin or forward signed parameters to another host. CVH wrappers
  stay on the configured AnimeGO origin; its API is restricted to
  `plapi.cdnvideohub.com`. AniBoom paths match `/embed/{id}`; Kodik paths match
  the verified scoped episode/movie shape.
  Media URLs are obtained only from its configuration; there is no manually
  maintained CDN list, naming-pattern assumption, or client-supplied URL endpoint.
  A separate media client accepts new public HTTPS DNS hosts and public HTTPS
  redirects, without credentials, fragments, nonstandard ports, or local names.
- Every third-party redirect is revalidated with a five-request redirect bound.
  DNS answers must all be public; mixed public/private answers fail. Connections
  dial the checked IP directly, retaining normal hostname TLS verification.
  IPv4-mapped IPv6, private, loopback, link-local, shared, metadata, documentation,
  and transition/special-use ranges are rejected. No environment proxy is used.
- Player HTML, scripts and source API JSON are capped at 2 MiB, manifests at
  256 KiB, and source lookup form bodies at 64 KiB. MP4 checks are HEAD only.
  Contexts, finite client
  and stage timeouts, TLS/header timeouts, and body closure bound operations.
  No retry or fallback attempts to bypass 401/403 responses.
- HLS URI lines and quoted URI attributes, and DASH BaseURL/Location plus
  media/initialization/sourceURL/href attributes, must resolve to valid HTTPS
  media URLs; references may use newly discovered CDN domains. Reference
  validation checks syntax and rejects literal/local hosts without fetching
  referenced media or resolving every reference's DNS. This is not a complete
  general manifest interpreter. Later client-side redirects, manifests, and
  media remain outside backend I/O and connection-time DNS validation.
- Existing AnimeGO requests retain their configured-origin redirect policy;
  `ANIMEGO_BASE_URL` is trusted operator configuration, not external player input.
- Structured resolution logs include provider, `stage` (`animego` or `player`),
  IDs, media type, duration, and upstream status (zero when none is available).
  HTTP errors discard net/http's full-URL wrapper. Complete URLs, HTML, manifests,
  tokens, and cookies are never logged.
- CDN host rotation is supported automatically. Configuration/selector/MIME changes, new tuple duplication,
  required session headers, source protection, or changed playlist structure
  fail closed until re-investigated. DNS/CDN geography and device codec support
  can affect playback independently of successful metadata resolution.

Minimal sanitized AniBoom HTML/HLS/DASH fixtures use synthetic media IDs and
paths. Resolver, HTTP security, registry, service, handler, and full-flow tests
are entirely offline. Live requests are optional manual checks only.
