CREATE TABLE IF NOT EXISTS sources (
    id           TEXT PRIMARY KEY,
    kind         TEXT NOT NULL,
    label        TEXT NOT NULL,
    site_url     TEXT NOT NULL DEFAULT '',
    grp          TEXT NOT NULL DEFAULT '',
    options      TEXT NOT NULL DEFAULT '{}',
    interval_sec INTEGER NOT NULL DEFAULT 600,
    enabled      INTEGER NOT NULL DEFAULT 1,
    origin       TEXT NOT NULL DEFAULT 'ui',
    created_at   INTEGER NOT NULL DEFAULT 0,
    updated_at   INTEGER NOT NULL DEFAULT 0,
    deleted_at   INTEGER NOT NULL DEFAULT 0
);

CREATE TABLE IF NOT EXISTS items (
    id           INTEGER PRIMARY KEY AUTOINCREMENT,
    source_id    TEXT NOT NULL REFERENCES sources(id) ON DELETE CASCADE,
    external_id  TEXT NOT NULL DEFAULT '',
    url          TEXT NOT NULL,
    url_hash     TEXT NOT NULL,
    title        TEXT NOT NULL,
    title_lc     TEXT NOT NULL DEFAULT '',
    title_hash   TEXT NOT NULL,
    summary      TEXT NOT NULL DEFAULT '',
    summary_lc   TEXT NOT NULL DEFAULT '',
    author       TEXT NOT NULL DEFAULT '',
    image        TEXT NOT NULL DEFAULT '',
    content      TEXT NOT NULL DEFAULT '',
    content_format TEXT NOT NULL DEFAULT '',
    published_at INTEGER NOT NULL,
    fetched_at   INTEGER NOT NULL,
    tags         TEXT NOT NULL DEFAULT '[]'
);

CREATE UNIQUE INDEX IF NOT EXISTS idx_items_url ON items(url_hash);
CREATE UNIQUE INDEX IF NOT EXISTS idx_items_ext ON items(source_id, external_id) WHERE external_id <> '';
CREATE INDEX IF NOT EXISTS idx_items_pub ON items(published_at DESC, id DESC);
CREATE INDEX IF NOT EXISTS idx_items_source ON items(source_id, published_at DESC);
CREATE INDEX IF NOT EXISTS idx_items_title ON items(title_hash);

CREATE TABLE IF NOT EXISTS source_state (
    source_id        TEXT PRIMARY KEY REFERENCES sources(id) ON DELETE CASCADE,
    last_run_at      INTEGER NOT NULL DEFAULT 0,
    last_ok_at       INTEGER NOT NULL DEFAULT 0,
    last_error       TEXT NOT NULL DEFAULT '',
    last_duration_ms INTEGER NOT NULL DEFAULT 0,
    last_new         INTEGER NOT NULL DEFAULT 0,
    total_runs       INTEGER NOT NULL DEFAULT 0,
    total_errors     INTEGER NOT NULL DEFAULT 0
);
