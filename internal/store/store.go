// Package store is the SQLite persistence layer.
package store

import (
	"context"
	"crypto/sha256"
	"database/sql"
	_ "embed"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
	"unicode"

	_ "modernc.org/sqlite"

	"newsfeed/internal/model"
)

//go:embed schema.sql
var schemaSQL string

// Store wraps the database handle.
type Store struct {
	db      *sql.DB
	writeMu sync.Mutex
}

// Open creates the directory, opens the database and applies the schema.
func Open(path string) (*Store, error) {
	if dir := filepath.Dir(path); dir != "" && dir != "." {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return nil, fmt.Errorf("create database dir: %w", err)
		}
	}
	dsn := "file:" + path +
		"?_pragma=busy_timeout(10000)" +
		"&_txlock=immediate" +
		"&_pragma=journal_mode(WAL)" +
		"&_pragma=synchronous(NORMAL)" +
		"&_pragma=foreign_keys(1)"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(4)
	db.SetMaxIdleConns(4)
	db.SetConnMaxLifetime(0)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := db.PingContext(ctx); err != nil {
		db.Close()
		return nil, fmt.Errorf("open %s: %w", path, err)
	}
	if _, err := db.ExecContext(ctx, schemaSQL); err != nil {
		db.Close()
		return nil, fmt.Errorf("apply schema: %w", err)
	}
	st := &Store{db: db}
	if err := st.migrate(ctx); err != nil {
		db.Close()
		return nil, fmt.Errorf("migrate: %w", err)
	}
	return st, nil
}

// migrate adds columns introduced after the first schema version. New
// databases get everything from schema.sql.
func (s *Store) migrate(ctx context.Context) error {
	cols, err := s.columns(ctx, "items")
	if err != nil {
		return err
	}
	added := map[string]bool{}
	for _, col := range []struct{ name, ddl string }{
		{"title_lc", `ALTER TABLE items ADD COLUMN title_lc TEXT NOT NULL DEFAULT ''`},
		{"summary_lc", `ALTER TABLE items ADD COLUMN summary_lc TEXT NOT NULL DEFAULT ''`},
		{"content", `ALTER TABLE items ADD COLUMN content TEXT NOT NULL DEFAULT ''`},
		{"content_format", `ALTER TABLE items ADD COLUMN content_format TEXT NOT NULL DEFAULT ''`},
	} {
		if cols[col.name] {
			continue
		}
		if _, err := s.db.ExecContext(ctx, col.ddl); err != nil {
			return err
		}
		added[col.name] = true
	}
	if !added["title_lc"] && !added["summary_lc"] {
		return nil
	}

	type row struct {
		id      int64
		title   string
		summary string
	}
	var all []row
	old, err := s.db.QueryContext(ctx, `SELECT id, title, summary FROM items`)
	if err != nil {
		return err
	}
	for old.Next() {
		var r row
		if err := old.Scan(&r.id, &r.title, &r.summary); err != nil {
			old.Close()
			return err
		}
		all = append(all, r)
	}
	old.Close()
	if err := old.Err(); err != nil {
		return err
	}
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	upd, err := tx.PrepareContext(ctx, `UPDATE items SET title_lc = ?, summary_lc = ? WHERE id = ?`)
	if err != nil {
		return err
	}
	defer upd.Close()
	for _, r := range all {
		if _, err := upd.ExecContext(ctx, strings.ToLower(r.title), strings.ToLower(r.summary), r.id); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (s *Store) columns(ctx context.Context, table string) (map[string]bool, error) {
	rows, err := s.db.QueryContext(ctx, `PRAGMA table_info(`+table+`)`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]bool{}
	for rows.Next() {
		var (
			cid     int
			name    string
			ctype   string
			notNull int
			dflt    sql.NullString
			pk      int
		)
		if err := rows.Scan(&cid, &name, &ctype, &notNull, &dflt, &pk); err != nil {
			return nil, err
		}
		out[name] = true
	}
	return out, rows.Err()
}

// Close releases the database.
func (s *Store) Close() error { return s.db.Close() }

// SyncSources makes the config file the source of truth: unknown sources are
// removed together with their items, configured ones are upserted.
func (s *Store) SyncSources(ctx context.Context, sources []model.Source) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	now := time.Now().Unix()
	upsert, err := tx.PrepareContext(ctx, `
		INSERT INTO sources (id, kind, label, site_url, grp, updated_at)
		VALUES (?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET
			kind = excluded.kind,
			label = excluded.label,
			site_url = excluded.site_url,
			grp = excluded.grp,
			updated_at = excluded.updated_at`)
	if err != nil {
		return err
	}
	defer upsert.Close()

	state, err := tx.PrepareContext(ctx, `INSERT INTO source_state (source_id) VALUES (?) ON CONFLICT DO NOTHING`)
	if err != nil {
		return err
	}
	defer state.Close()

	ids := make([]string, 0, len(sources))
	args := make([]any, 0, len(sources))
	for _, src := range sources {
		if _, err := upsert.ExecContext(ctx, src.ID, src.Kind, src.Label, src.SiteURL, src.Group, now); err != nil {
			return fmt.Errorf("upsert source %s: %w", src.ID, err)
		}
		if _, err := state.ExecContext(ctx, src.ID); err != nil {
			return fmt.Errorf("init source state %s: %w", src.ID, err)
		}
		ids = append(ids, src.ID)
		args = append(args, src.ID)
	}

	if len(ids) == 0 {
		if _, err := tx.ExecContext(ctx, `DELETE FROM sources`); err != nil {
			return err
		}
	} else {
		placeholders := strings.TrimRight(strings.Repeat("?,", len(ids)), ",")
		if _, err := tx.ExecContext(ctx, `DELETE FROM sources WHERE id NOT IN (`+placeholders+`)`, args...); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// InsertItems stores a fetched batch and reports how many rows were new.
// Rows are deduplicated by canonical URL, by (source, external id) and by
// identical normalized title seen from another source in the last 36 hours.
func (s *Store) InsertItems(ctx context.Context, sourceID string, items []model.Item) (int, error) {
	if len(items) == 0 {
		return 0, nil
	}
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	now := time.Now()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()

	insert, err := tx.PrepareContext(ctx, `
		INSERT INTO items
			(source_id, external_id, url, url_hash, title, title_lc, title_hash, summary, summary_lc, author, image, content, content_format, published_at, fetched_at, tags)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT DO NOTHING`)
	if err != nil {
		return 0, err
	}
	defer insert.Close()

	backfill, err := tx.PrepareContext(ctx, `
		UPDATE items
		SET content = CASE WHEN content = '' THEN ? ELSE content END,
		    content_format = CASE WHEN content_format = '' THEN ? ELSE content_format END,
		    image = CASE WHEN image = '' THEN ? ELSE image END
		WHERE url_hash = ? AND (content = '' OR image = '')`)
	if err != nil {
		return 0, err
	}
	defer backfill.Close()

	titleSeen, err := tx.PrepareContext(ctx, `
		SELECT 1 FROM items
		WHERE title_hash = ? AND source_id <> ? AND published_at >= ?
		LIMIT 1`)
	if err != nil {
		return 0, err
	}
	defer titleSeen.Close()

	newCount := 0
	for _, it := range items {
		link, ok := canonicalURL(it.URL)
		if !ok {
			continue
		}
		title := strings.TrimSpace(it.Title)
		if title == "" {
			continue
		}
		if len([]rune(title)) > 500 {
			title = string([]rune(title)[:500])
		}
		published := it.Published
		if published.IsZero() || published.Year() < 1990 || published.After(now.Add(48*time.Hour)) {
			published = now
		}
		tags, _ := json.Marshal(cleanTags(it.Tags))
		titleHash := titleDigest(title)

		var exists int
		err := titleSeen.QueryRowContext(ctx, titleHash, sourceID, now.Add(-36*time.Hour).Unix()).Scan(&exists)
		switch {
		case err == nil:
			continue // the same headline already came from another source
		case err != sql.ErrNoRows:
			return newCount, err
		}

		summary := strings.TrimSpace(it.Summary)
		res, err := insert.ExecContext(ctx,
			sourceID,
			strings.TrimSpace(it.ExternalID),
			link,
			digest(link),
			title,
			strings.ToLower(title),
			titleHash,
			summary,
			strings.ToLower(summary),
			strings.TrimSpace(it.Author),
			strings.TrimSpace(it.Image),
			it.Content,
			it.ContentFormat,
			published.Unix(),
			now.Unix(),
			string(tags),
		)
		if err != nil {
			return newCount, err
		}
		if n, _ := res.RowsAffected(); n > 0 {
			newCount++
		} else if it.Content != "" || it.Image != "" {
			if _, err := backfill.ExecContext(ctx, it.Content, it.ContentFormat, it.Image, digest(link)); err != nil {
				return newCount, err
			}
		}
	}
	return newCount, tx.Commit()
}

// Feed returns one page ordered newest first.
func (s *Store) Feed(ctx context.Context, q model.FeedQuery) ([]model.FeedItem, bool, error) {
	limit := q.Limit
	if limit <= 0 || limit > 200 {
		limit = 40
	}
	var (
		where []string
		args  []any
	)
	if q.Before != nil {
		where = append(where, `(i.published_at < ? OR (i.published_at = ? AND i.id < ?))`)
		args = append(args, q.Before.PublishedAt, q.Before.PublishedAt, q.Before.ID)
	}
	if len(q.Sources) > 0 {
		where = append(where, `i.source_id IN (`+placeholders(len(q.Sources))+`)`)
		for _, v := range q.Sources {
			args = append(args, v)
		}
	}
	if len(q.Kinds) > 0 {
		where = append(where, `s.kind IN (`+placeholders(len(q.Kinds))+`)`)
		for _, v := range q.Kinds {
			args = append(args, v)
		}
	}
	if search := strings.TrimSpace(q.Search); search != "" {
		like := "%" + escapeLike(strings.ToLower(search)) + "%"
		where = append(where, `(i.title_lc LIKE ? ESCAPE '\' OR i.summary_lc LIKE ? ESCAPE '\')`)
		args = append(args, like, like)
	}

	query := feedSelect
	if len(where) > 0 {
		query += " WHERE " + strings.Join(where, " AND ")
	}
	query += " ORDER BY i.published_at DESC, i.id DESC LIMIT ?"
	args = append(args, limit+1)

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, false, err
	}
	defer rows.Close()

	out, err := scanFeed(rows, limit)
	if err != nil {
		return nil, false, err
	}
	hasMore := len(out) > limit
	if hasMore {
		out = out[:limit]
	}
	return out, hasMore, nil
}

const feedSelect = `
	SELECT i.id, i.source_id, s.label, s.kind, s.grp, i.url, i.title, i.summary, i.author, i.image, i.published_at, i.tags
	FROM items i
	JOIN sources s ON s.id = i.source_id`

// ByIDs returns the requested stories, newest first. The reader uses it to
// restore bookmarks that fell out of the current page window.
func (s *Store) ByIDs(ctx context.Context, ids []int64) ([]model.FeedItem, error) {
	if len(ids) == 0 {
		return []model.FeedItem{}, nil
	}
	if len(ids) > 200 {
		ids = ids[:200]
	}
	args := make([]any, len(ids))
	for i, id := range ids {
		args[i] = id
	}
	query := feedSelect + " WHERE i.id IN (" + placeholders(len(ids)) + ") ORDER BY i.published_at DESC, i.id DESC"
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanFeed(rows, len(ids))
}

const storySelect = `
	SELECT i.id, i.source_id, s.label, s.kind, s.grp, i.url, i.title, i.summary, i.author, i.image, i.published_at, i.tags, i.content, i.content_format
	FROM items i
	JOIN sources s ON s.id = i.source_id`

// ByID returns one story with the article body stored at ingestion time.
func (s *Store) ByID(ctx context.Context, id int64) (model.FeedItem, error) {
	var (
		it   model.FeedItem
		tags string
		ts   int64
	)
	err := s.db.QueryRowContext(ctx, storySelect+` WHERE i.id = ?`, id).Scan(
		&it.ID, &it.SourceID, &it.Source, &it.Kind, &it.Group, &it.URL, &it.Title,
		&it.Summary, &it.Author, &it.Image, &ts, &tags, &it.Content, &it.ContentFormat,
	)
	if err != nil {
		return it, err
	}
	it.PublishedAt = time.Unix(ts, 0).UTC()
	_ = json.Unmarshal([]byte(tags), &it.Tags)
	if it.Tags == nil {
		it.Tags = []string{}
	}
	return it, nil
}

func scanFeed(rows *sql.Rows, capacity int) ([]model.FeedItem, error) {
	out := make([]model.FeedItem, 0, capacity)
	for rows.Next() {
		var (
			it   model.FeedItem
			tags string
			ts   int64
		)
		if err := rows.Scan(&it.ID, &it.SourceID, &it.Source, &it.Kind, &it.Group, &it.URL, &it.Title, &it.Summary, &it.Author, &it.Image, &ts, &tags); err != nil {
			return nil, err
		}
		it.PublishedAt = time.Unix(ts, 0).UTC()
		_ = json.Unmarshal([]byte(tags), &it.Tags)
		if it.Tags == nil {
			it.Tags = []string{}
		}
		out = append(out, it)
	}
	return out, rows.Err()
}

// Bucket is one hour of publishing activity.
type Bucket struct {
	T     int64 `json:"t"`
	Count int64 `json:"count"`
}

// Stats is the dashboard payload.
type Stats struct {
	ItemsTotal int64               `json:"items_total"`
	Items24h   int64               `json:"items_24h"`
	LastItemAt time.Time           `json:"last_item_at"`
	Sources    []model.SourceStats `json:"sources"`
	Activity   []Bucket            `json:"activity"`
}

// Stats aggregates counts and source health in one round trip.
func (s *Store) Stats(ctx context.Context) (Stats, error) {
	var out Stats
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM items`).Scan(&out.ItemsTotal); err != nil {
		return out, err
	}
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM items WHERE published_at >= ?`, time.Now().Add(-24*time.Hour).Unix()).Scan(&out.Items24h); err != nil {
		return out, err
	}
	var last int64
	if err := s.db.QueryRowContext(ctx, `SELECT COALESCE(MAX(published_at), 0) FROM items`).Scan(&last); err != nil {
		return out, err
	}
	if last > 0 {
		out.LastItemAt = time.Unix(last, 0).UTC()
	}

	buckets := map[int64]int64{}
	hourStart := time.Now().UTC().Truncate(time.Hour).Add(-23 * time.Hour)
	activityRows, err := s.db.QueryContext(ctx,
		`SELECT (published_at / 3600) * 3600 AS b, COUNT(*) FROM items WHERE published_at >= ? GROUP BY b`,
		hourStart.Unix())
	if err != nil {
		return out, err
	}
	for activityRows.Next() {
		var t, n int64
		if err := activityRows.Scan(&t, &n); err != nil {
			activityRows.Close()
			return out, err
		}
		buckets[t] = n
	}
	activityRows.Close()
	for i := 0; i < 24; i++ {
		t := hourStart.Add(time.Duration(i) * time.Hour).Unix()
		out.Activity = append(out.Activity, Bucket{T: t, Count: buckets[t]})
	}

	rows, err := s.db.QueryContext(ctx, `
		SELECT s.id, s.kind, s.label, s.grp, s.site_url,
		       COALESCE(st.last_run_at, 0), COALESCE(st.last_ok_at, 0), COALESCE(st.last_error, ''),
		       COALESCE(st.last_duration_ms, 0), COALESCE(st.last_new, 0),
		       COALESCE(st.total_runs, 0), COALESCE(st.total_errors, 0),
		       (SELECT COUNT(*) FROM items i WHERE i.source_id = s.id)
		FROM sources s
		LEFT JOIN source_state st ON st.source_id = s.id
		ORDER BY s.grp, s.label`)
	if err != nil {
		return out, err
	}
	defer rows.Close()
	for rows.Next() {
		var (
			st      model.SourceStats
			lastRun int64
			lastOK  int64
		)
		if err := rows.Scan(&st.ID, &st.Kind, &st.Label, &st.Group, &st.SiteURL,
			&lastRun, &lastOK, &st.LastError, &st.LastDurationMS, &st.LastNew,
			&st.Runs, &st.Errors, &st.Items); err != nil {
			return out, err
		}
		if lastRun > 0 {
			st.LastRunAt = time.Unix(lastRun, 0).UTC()
		}
		if lastOK > 0 {
			st.LastOKAt = time.Unix(lastOK, 0).UTC()
		}
		out.Sources = append(out.Sources, st)
	}
	return out, rows.Err()
}

// MarkRun records the outcome of one fetch.
func (s *Store) MarkRun(ctx context.Context, sourceID string, duration time.Duration, newItems int, fetchErr error) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	now := time.Now().Unix()
	msg := ""
	okAt := now
	errDelta := 0
	if fetchErr != nil {
		msg = fetchErr.Error()
		if len(msg) > 500 {
			msg = msg[:500]
		}
		okAt = 0
		errDelta = 1
	}
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO source_state
			(source_id, last_run_at, last_ok_at, last_error, last_duration_ms, last_new, total_runs, total_errors)
		VALUES (?, ?, ?, ?, ?, ?, 1, ?)
		ON CONFLICT(source_id) DO UPDATE SET
			last_run_at = excluded.last_run_at,
			last_ok_at = CASE WHEN excluded.last_error = '' THEN excluded.last_ok_at ELSE source_state.last_ok_at END,
			last_error = excluded.last_error,
			last_duration_ms = excluded.last_duration_ms,
			last_new = excluded.last_new,
			total_runs = source_state.total_runs + 1,
			total_errors = source_state.total_errors + ?`,
		sourceID, now, okAt, msg, duration.Milliseconds(), newItems, errDelta, errDelta)
	return err
}

// Prune drops items older than the retention window.
func (s *Store) Prune(ctx context.Context, days int) (int64, error) {
	if days <= 0 {
		return 0, nil
	}
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	cutoff := time.Now().AddDate(0, 0, -days).Unix()
	res, err := s.db.ExecContext(ctx, `DELETE FROM items WHERE published_at < ?`, cutoff)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

func canonicalURL(raw string) (string, bool) {
	raw = strings.TrimSpace(raw)
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return "", false
	}
	u.Scheme = strings.ToLower(u.Scheme)
	if u.Scheme != "http" && u.Scheme != "https" {
		return "", false
	}
	u.Host = strings.ToLower(u.Host)
	u.Host = strings.TrimSuffix(u.Host, ":80")
	u.Host = strings.TrimSuffix(u.Host, ":443")
	u.Fragment = ""
	if u.Path == "/" {
		u.Path = ""
	} else {
		u.Path = strings.TrimRight(u.Path, "/")
	}
	q := u.Query()
	for k := range q {
		lk := strings.ToLower(k)
		if strings.HasPrefix(lk, "utm_") || lk == "fbclid" || lk == "gclid" || lk == "mc_cid" || lk == "mc_eid" || lk == "igshid" {
			q.Del(k)
		}
	}
	u.RawQuery = q.Encode()
	return u.String(), true
}

func digest(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:16])
}

func titleDigest(title string) string {
	var b strings.Builder
	lastSpace := false
	for _, r := range strings.ToLower(title) {
		switch {
		case unicode.IsLetter(r) || unicode.IsNumber(r):
			b.WriteRune(r)
			lastSpace = false
		case !lastSpace:
			b.WriteByte(' ')
			lastSpace = true
		}
	}
	return digest(strings.TrimSpace(b.String()))
}

func cleanTags(tags []string) []string {
	out := make([]string, 0, len(tags))
	seen := map[string]bool{}
	for _, t := range tags {
		t = strings.TrimSpace(t)
		if t == "" || len([]rune(t)) > 40 || seen[strings.ToLower(t)] {
			continue
		}
		seen[strings.ToLower(t)] = true
		out = append(out, t)
		if len(out) == 5 {
			break
		}
	}
	return out
}

func placeholders(n int) string {
	return strings.TrimRight(strings.Repeat("?,", n), ",")
}

func escapeLike(s string) string {
	s = strings.ReplaceAll(s, `\`, `\\`)
	s = strings.ReplaceAll(s, `%`, `\%`)
	s = strings.ReplaceAll(s, `_`, `\_`)
	return s
}
