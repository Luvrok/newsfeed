# Dispatch

A self-hosted news collector with a fast JSON API and a quiet, editorial web reader.
No RSS output, no accounts, no tracking: it pulls from public feeds, official APIs and
Telegram's public preview, deduplicates the pile and serves one clean stream with an
in-site reading view.

```
sources (one goroutine each)  ->  SQLite  ->  /api/v1/feed  ->  React reader
```

## Features

- **Config-driven subscriptions.** Add or remove a source by editing one TOML block.
- **One goroutine per source**, its own interval, jitter and exponential backoff on errors.
- **Modular fetchers.** Each source kind is a small package behind a shared interface;
  a new kind is ~100 lines and one import line.
- **Deduplication** by canonical URL, by `(source, external id)` and by identical
  normalized headline seen from another source within 36 hours.
- **Fast reads.** SQLite in WAL mode, one composite index for the feed, cursor
  pagination, parallel polling and conditional GET (`ETag` / `If-Modified-Since`) for RSS.
- **Source health** in the API and the UI, with a clickable status drawer, per-source
  retry and a banner when something goes stale. The scheduler runs without the browser.
- **Reading view**, not redirects: the detail page shows the article text the feed or API
  delivered. No page scraping, so publisher redesigns cannot break the reader.
- **Image proxy** with disk cache and server-side resizing: Telegram, Reddit and CDN
  pictures load from the local service, through the same proxy as the fetchers, even in a
  network where the source CDN is blocked. Feed thumbs are 640 px, articles up to 1600 px.
- **Reader state in the browser**: read marks, bookmarks, keyboard navigation, search,
  infinite scroll, dark and light themes in a soft Gruvbox palette with sharp corners.
- **Single binary in production.** Go serves both the API and the built React app.

## Quick start

Requirements: Go 1.26+ and Node 22+.

```bash
# 1. build the web app
cd web
npm install
npm run build
cd ..

# 2. fetch everything once (writes data/newsfeed.db)
go run ./cmd/newsd fetch

# 3. serve API + web on http://127.0.0.1:8787
go run ./cmd/newsd serve
```

During development run the API and Vite separately; Vite proxies `/api` to the backend:

```bash
go run ./cmd/newsd serve      # terminal 1
cd web && npm run dev         # terminal 2, http://localhost:5173
```

Or use the Makefile:

```bash
make build     # web build + bin/newsd
make run       # go run ./cmd/newsd serve
make fetch     # one-shot poll of every source
make test      # store tests: URL canonicalization, dedupe, keyset pagination
```

`newsd fetch [source-id]` is also useful from cron or a systemd timer if you would rather
not keep the scheduler running.

### Run as a service

[`deploy/newsd.service`](deploy/newsd.service) is a minimal systemd unit. Copy the project to
`/opt/dispatch`, build with `make build`, create a `dispatch` user that owns `data/`, adjust
the paths in the unit and install it:

```bash
sudo useradd --system --home /opt/dispatch --shell /usr/sbin/nologin dispatch
sudo chown -R dispatch:dispatch /opt/dispatch/data
sudo cp deploy/newsd.service /etc/systemd/system/
sudo systemctl enable --now newsd
```

Keep the service on `127.0.0.1` or put it behind a reverse proxy. The API has no
authentication by design.

## Configuration

Everything lives in [`config/newsfeed.toml`](config/newsfeed.toml). The service treats the
file as the source of truth: a source removed from the file is removed from the database
together with its items.

```toml
[server]
addr = "127.0.0.1:8787"
web_dir = "web/dist"
image_cache = "data/images"
allowed_origin = ""        # empty: localhost dev only, "*": any origin

[database]
path = "data/newsfeed.db"

[fetch]
timeout = "40s"
workers = 8
max_items = 300            # hard cap per source per poll
retention_days = 30        # older items are pruned every six hours
user_agent = "newsfeed/0.1 (+https://github.com/barnard/newsfeed)"
proxy = ""                 # "socks5://127.0.0.1:1080"; NEWSFEED_PROXY also works

[[sources]]
id = "bbc-world"           # stable key, used by filters and state
kind = "rss"               # which fetcher handles it
label = "BBC World"        # shown in the UI
site = "https://www.bbc.com/news/world"
group = "world"            # sidebar group: world, tech, russia, social, official
interval = "10m"
enabled = true             # optional, defaults to true
[sources.options]
url = "https://feeds.bbci.co.uk/news/world/rss.xml"
limit = 25
```

### Source kinds

| kind | options | notes |
|---|---|---|
| `rss` | `url` or `urls`, `limit`, `summary = true/false` | Any RSS/Atom feed. Sends `ETag` and `If-Modified-Since`. `summary = false` is useful for Google News, whose description is a list of related links. |
| `telegram` | `channels`, `limit` | Reads `t.me/s/<channel>`. No bot token, no account. Public channels only. |
| `hackernews` | `list` (`top`/`new`/`best`/`ask`/`show`/`job`), `limit` | Official Firebase API. |
| `lobsters` | `tag` (optional), `limit` | Public JSON. |
| `mastodon` | `instance`, `tag` (a hashtag or `local`), `limit` | Public instance API, no token. |
| `usgs` | `feed` (`significant`/`4.5`/`2.5`/`1.0`/`all`), `period` (`hour`/`day`/`week`), `limit` | Official GeoJSON. |
| `github` | `repos`, `limit`, `prerelease = false` | Public releases API, unauthenticated (60 requests/hour per IP). |
| `reddit` | `subreddits`, `sort`, `time`, `limit`, `include_nsfw`, `include_stickied` | Official OAuth API. Needs `REDDIT_CLIENT_ID` / `REDDIT_CLIENT_SECRET`; shipped disabled. |
| `gdelt` | `query`, `timespan`, `max`, `language` | Free article index, but rate-limited to one request per five seconds; shipped disabled. |

Any source can override the global proxy with `proxy` in its `[sources.options]`. This is
the usual setup for Telegram in Russia: tunnel only Telegram, keep everything else direct.
`proxy` accepts `http://`, `https://` and `socks5://` URLs. Empty means `NEWSFEED_PROXY`,
then `HTTP_PROXY` / `HTTPS_PROXY`, then a direct connection. The image proxy follows the
global proxy, or the first per-source proxy when only one source is tunneled.

Defaults are deliberately conservative: feeds are polled every 10–30 minutes, fast wires
(Hacker News, USGS, Telegram) every 5 minutes. Every poll runs in parallel with an
independent timer, so one slow host never delays another.

## API

The API is read-only except for the refresh triggers.

```http
GET /api/v1/feed?limit=40&cursor=<opaque>&source=tg-tass,bbc-world&kind=rss&q=climate
```

- `limit` 1–100 (default 40)
- `cursor` from the previous response's `next_cursor`
- `source` comma-separated source ids
- `kind` comma-separated kinds
- `q` case-insensitive substring over title and summary (Unicode-aware, so Cyrillic
  works in any case)

```json
{
  "items": [
    {
      "id": 1240,
      "source_id": "bbc-world",
      "source": "BBC World",
      "kind": "rss",
      "group": "world",
      "url": "https://www.bbc.com/news/...",
      "title": "…",
      "summary": "…",
      "author": "…",
      "image": "https://…",
      "published_at": "2026-11-22T00:01:00Z",
      "tags": ["…"]
    }
  ],
  "next_cursor": "MTc5NTI5MzQ1NTo2Nw",
  "has_more": true,
  "generated_at": "2026-11-22T00:02:11Z"
}
```

```http
GET  /api/v1/sources          # totals, activity buckets, per-source health
GET  /api/v1/items?ids=1,2,3  # restore bookmarks / specific stories by id
GET  /api/v1/story/{id}       # full story: stored article body, image and metadata
GET  /api/v1/image?u=…&w=640  # cached image proxy with server-side resize
GET  /api/v1/health           # liveness and version
POST /api/v1/refresh          # ask every source to poll now; 10 s client cooldown
POST /api/v1/sources/{id}/refresh
```

## Adding a source

A source kind is a package that registers a factory. The shared contract lives in
[`internal/source/source.go`](internal/source/source.go):

```go
type Fetcher interface {
    Fetch(ctx context.Context, opts source.Options) ([]model.Item, error)
}
```

1. Create `internal/source/example/example.go`:

```go
package example

import (
    "context"
    "newsfeed/internal/model"
    "newsfeed/internal/source"
)

func init() {
    source.Register("example", func() source.Fetcher { return &fetcher{} })
}

type fetcher struct{}

func (f *fetcher) Fetch(ctx context.Context, opts source.Options) ([]model.Item, error) {
    endpoint := opts.String("endpoint", "")
    // http get, decode, map to model.Item
    return items, nil
}
```

2. Import it for its side effect in [`internal/source/all/all.go`](internal/source/all/all.go):

```go
_ "newsfeed/internal/source/example"
```

3. Add a block to the config:

```toml
[[sources]]
id = "my-source"
kind = "example"
label = "My source"
group = "tech"
interval = "10m"
[sources.options]
endpoint = "https://example.com/api"
```

Nothing else changes. The scheduler, store, dedupe, API and UI discover it from the config.
Options are a `map[string]any` with string, int and bool helpers, so a fetcher only reads
the keys it needs; unknown keys are ignored.

## Layout

```
cmd/newsd/            CLI: serve, fetch, sources, version
internal/api/         JSON API, CORS, image proxy, static SPA serving
internal/config/      TOML loading and defaults
internal/content/     HTML sanitizer, article bodies, image proxy URLs
internal/ingest/      scheduler, per-source goroutines, batch mode
internal/model/       Item, FeedItem, SourceStats
internal/source/      registry, HTTP helpers, one package per kind
internal/store/       SQLite schema, dedupe, feed queries, stats
config/newsfeed.toml  subscriptions
web/                  Vite + React + TypeScript reader
docs/screenshots/     rendered screenshots
```

## Data and deduplication

An item is keyed by a canonical URL: fragment and tracking parameters (`utm_*`, `fbclid`,
`gclid`, …) are dropped, host and scheme lowercased, trailing slash removed. Two unique
indexes back the insert: the URL hash and `(source_id, external_id)`. Before inserting, the
store also looks for the same normalized headline from a different source in the last 36
hours, which keeps one event from filling the stream twice without clustering.

Story bodies are stored once, during ingestion: RSS `content:encoded`, Telegram post text,
Mastodon HTML, Reddit selftext, HN comments and GitHub release notes. The sanitizer keeps a
conservative set of tags and attributes, strips scripts/styles/iframes, resolves relative
links, opens links in a new tab and rewrites every image through the local proxy. The feed
payload stays small; the body is only returned by `/api/v1/story/{id}`.

SQLite runs in WAL mode with a busy timeout and foreign keys. The feed query uses
`ORDER BY published_at DESC, id DESC` and a `(published_at DESC, id DESC)` index; pagination
is a keyset cursor, not an offset.

## Notes and limits

- **No RSS output.** RSS is an input format here. The output is the JSON API and the reader.
- **No page scraping.** The article view is built only from what the source's feed or API
  delivered. When a publisher ships a teaser only, the page says so and links to the
  original. Nothing breaks when a publisher changes its markup.
- **Telegram** uses the public web preview. Channels without a preview, private channels and
  channels that restrict embedding will return nothing. Point `proxy` at a tunnel if
  `t.me` is blocked on your network; the same client fetches channel images.
- **Reddit** uses the official OAuth API. Create a free "script" app at
  <https://www.reddit.com/prefs/apps>, export `REDDIT_CLIENT_ID` and
  `REDDIT_CLIENT_SECRET`, then set `enabled = true` on the `reddit` source. The source is
  shipped disabled so a fresh install never fails on missing credentials.
- **Reuters** has no free official feed any more. The shipped source uses a Google News
  RSS search restricted to `site:reuters.com`; the link opens the original article.
- **GDELT** is shipped disabled: its API allows roughly one request per five seconds and
  bans bursts. Enable it only if you accept the pacing.
- **X/Twitter** is intentionally absent: the official API is paid and scraping is brittle.
- **No LLM, no clustering, no ranking.** This version is the collection and reading layer.
- Items are pruned after `retention_days`; source health and the config remain.

## Design

Soft Gruvbox dark and light, Literata for headlines, Inter for the interface, JetBrains Mono
for metadata. Every corner is square; images use a cut bottom-right corner instead of a
radius. Fonts are self-hosted (no CDN, works offline) and licensed under the SIL Open Font
License. The reader has a lead story, day groups, an in-site reading view with a progress
bar, keyboard navigation (`j`/`k`, `o`, `s`, `/`, `r`, `?`; physical keys work on Cyrillic
layouts), search, source filters, a status drawer, read marks and bookmarks in
`localStorage`, and a live activity chart fed by the API. The scheduler polls in the
background: the page also refreshes its feed every 60 seconds and the status every 30
seconds, so the refresh button is only for an immediate poll.

Screenshots:
[`desktop-dark.png`](docs/screenshots/desktop-dark.png),
[`desktop-light.png`](docs/screenshots/desktop-light.png),
[`mobile.png`](docs/screenshots/mobile.png),
[`desktop-mid.png`](docs/screenshots/desktop-mid.png),
[`article-dark.png`](docs/screenshots/article-dark.png),
[`article-light.png`](docs/screenshots/article-light.png),
[`article-telegram.png`](docs/screenshots/article-telegram.png),
[`article-mobile.png`](docs/screenshots/article-mobile.png),
[`status.png`](docs/screenshots/status.png).
