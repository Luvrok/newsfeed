// Package api serves the JSON feed and the built web app.
package api

import (
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"newsfeed/internal/config"
	"newsfeed/internal/content"
	"newsfeed/internal/ingest"
	"newsfeed/internal/model"
	"newsfeed/internal/source/httpx"
	"newsfeed/internal/store"
)

// Server holds the API dependencies.
type Server struct {
	cfg     *config.Config
	store   *store.Store
	sched   *ingest.Scheduler
	log     *slog.Logger
	version string
	started time.Time

	lastRefresh atomic.Int64

	imageDir    string
	imageClient *http.Client
	imageLocks  sync.Map
}

// New builds the HTTP layer.
func New(cfg *config.Config, st *store.Store, sched *ingest.Scheduler, log *slog.Logger, version string) *Server {
	imageProxy := cfg.Fetch.Proxy
	if imageProxy == "" {
		// A Telegram-only tunnel is a common setup; images should follow the
		// first source proxy when no global one is configured.
		for _, src := range cfg.Sources {
			if !src.IsEnabled() {
				continue
			}
			if proxy, ok := src.Options["proxy"].(string); ok && strings.TrimSpace(proxy) != "" {
				imageProxy = proxy
				break
			}
		}
	}
	s := &Server{
		cfg:         cfg,
		store:       st,
		sched:       sched,
		log:         log,
		version:     version,
		started:     time.Now(),
		imageDir:    cfg.Server.ImageCache,
		imageClient: httpx.Client(20*time.Second, "newsfeed/0.1 (+image proxy)", imageProxy),
	}
	if s.imageDir != "" {
		if err := os.MkdirAll(s.imageDir, 0o755); err != nil {
			log.Error("image cache directory", "dir", s.imageDir, "err", err)
			s.imageDir = ""
		}
	}
	return s
}

// Handler returns the full router with middleware applied.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()

	mux.HandleFunc("GET /api/v1/feed", s.handleFeed)
	mux.HandleFunc("GET /api/v1/items", s.handleItems)
	mux.HandleFunc("GET /api/v1/story/{id}", s.handleStory)
	mux.HandleFunc("GET /api/v1/image", s.handleImage)
	mux.HandleFunc("GET /api/v1/sources", s.handleSources)
	mux.HandleFunc("POST /api/v1/refresh", s.handleRefresh)
	mux.HandleFunc("POST /api/v1/sources/{id}/refresh", s.handleRefreshOne)
	mux.HandleFunc("GET /api/v1/health", s.handleHealth)
	mux.HandleFunc("/api", handleAPINotFound)
	mux.HandleFunc("/api/", handleAPINotFound)

	mux.Handle("/", s.staticHandler())

	return s.withMiddleware(mux)
}

func (s *Server) withMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}

		rec.Header().Set("X-Content-Type-Options", "nosniff")
		rec.Header().Set("Referrer-Policy", "strict-origin-when-cross-origin")
		if origin := r.Header.Get("Origin"); origin != "" && s.originAllowed(origin) {
			rec.Header().Set("Access-Control-Allow-Origin", origin)
			rec.Header().Set("Vary", "Origin")
			rec.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
			rec.Header().Set("Access-Control-Allow-Headers", "Content-Type")
		}
		if r.Method == http.MethodOptions {
			rec.WriteHeader(http.StatusNoContent)
			return
		}

		defer func() {
			if v := recover(); v != nil {
				s.log.Error("panic", "path", r.URL.Path, "panic", v)
				http.Error(rec, "internal error", http.StatusInternalServerError)
			}
			if rec.status >= 400 {
				s.log.Warn("http", "method", r.Method, "path", r.URL.Path, "status", rec.status,
					"took", time.Since(start).Round(time.Millisecond))
			} else {
				s.log.Debug("http", "method", r.Method, "path", r.URL.Path, "status", rec.status,
					"took", time.Since(start).Round(time.Millisecond))
			}
		}()
		next.ServeHTTP(rec, r)
	})
}

func (s *Server) originAllowed(origin string) bool {
	allow := strings.TrimSpace(s.cfg.Server.AllowedOrigin)
	if allow == "*" || allow == origin {
		return true
	}
	// Vite dev server moves between ports; allow localhost variants by default.
	return allow == "" && (strings.HasPrefix(origin, "http://localhost:") || strings.HasPrefix(origin, "http://127.0.0.1:"))
}

func (s *Server) handleFeed(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	limit := 40
	if v := q.Get("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 && n <= 100 {
			limit = n
		}
	}
	feedQuery := model.FeedQuery{
		Limit:   limit,
		Sources: splitList(q.Get("source")),
		Kinds:   splitList(q.Get("kind")),
		Search:  strings.TrimSpace(q.Get("q")),
	}
	if cur := q.Get("cursor"); cur != "" {
		before, err := decodeCursor(cur)
		if err != nil {
			writeError(w, http.StatusBadRequest, "bad cursor")
			return
		}
		feedQuery.Before = before
	}

	items, hasMore, err := s.store.Feed(r.Context(), feedQuery)
	if err != nil {
		s.log.Error("feed", "err", err)
		writeError(w, http.StatusInternalServerError, "could not read the feed")
		return
	}
	for i := range items {
		items[i].Image = content.ImageURL(items[i].Image, 640)
	}
	next := ""
	if hasMore && len(items) > 0 {
		last := items[len(items)-1]
		next = encodeCursor(model.Cursor{PublishedAt: last.PublishedAt.Unix(), ID: last.ID})
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"items":        items,
		"next_cursor":  next,
		"has_more":     hasMore,
		"generated_at": time.Now().UTC(),
	})
}

func (s *Server) handleItems(w http.ResponseWriter, r *http.Request) {
	raw := strings.Split(r.URL.Query().Get("ids"), ",")
	if len(raw) == 0 {
		writeError(w, http.StatusBadRequest, "ids is required")
		return
	}
	ids := make([]int64, 0, len(raw))
	for _, value := range raw {
		id, err := strconv.ParseInt(strings.TrimSpace(value), 10, 64)
		if err != nil || id <= 0 {
			continue
		}
		ids = append(ids, id)
		if len(ids) == 200 {
			break
		}
	}
	if len(ids) == 0 {
		writeError(w, http.StatusBadRequest, "no valid ids")
		return
	}
	items, err := s.store.ByIDs(r.Context(), ids)
	if err != nil {
		s.log.Error("items by id", "err", err)
		writeError(w, http.StatusInternalServerError, "could not read the items")
		return
	}
	for i := range items {
		items[i].Image = content.ImageURL(items[i].Image, 640)
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

func (s *Server) handleStory(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		writeError(w, http.StatusBadRequest, "bad story id")
		return
	}
	item, err := s.store.ByID(r.Context(), id)
	if errors.Is(err, sql.ErrNoRows) {
		writeError(w, http.StatusNotFound, "story not found")
		return
	}
	if err != nil {
		s.log.Error("story", "id", id, "err", err)
		writeError(w, http.StatusInternalServerError, "could not read the story")
		return
	}
	item.Image = content.ImageURL(item.Image, 1600)
	writeJSON(w, http.StatusOK, map[string]any{"item": item})
}

func (s *Server) handleSources(w http.ResponseWriter, r *http.Request) {
	stats, err := s.store.Stats(r.Context())
	if err != nil {
		s.log.Error("stats", "err", err)
		writeError(w, http.StatusInternalServerError, "could not read source state")
		return
	}
	now := time.Now()
	for i := range stats.Sources {
		src := &stats.Sources[i]
		for _, sc := range s.cfg.Sources {
			if sc.ID == src.ID {
				src.IntervalSec = int(sc.Interval.Std().Seconds())
				break
			}
		}
		status := "ok"
		switch {
		case src.LastRunAt.IsZero():
			status = "never"
		case src.LastError != "":
			status = "error"
		case src.IntervalSec > 0 && now.Sub(src.LastOKAt) > 3*time.Duration(src.IntervalSec)*time.Second:
			status = "stale"
		}
		src.Status = status
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"items_total":  stats.ItemsTotal,
		"items_24h":    stats.Items24h,
		"last_item_at": stats.LastItemAt,
		"sources":      stats.Sources,
		"activity":     stats.Activity,
		"fetched_at":   now.UTC(),
		"version":      s.version,
	})
}

func (s *Server) handleRefresh(w http.ResponseWriter, r *http.Request) {
	now := time.Now().Unix()
	last := s.lastRefresh.Load()
	if now-last < 10 {
		w.Header().Set("Retry-After", "10")
		writeError(w, http.StatusTooManyRequests, "refresh was already requested, wait a few seconds")
		return
	}
	s.lastRefresh.Store(now)
	n := s.sched.Trigger()
	writeJSON(w, http.StatusAccepted, map[string]any{"triggered": n})
}

func (s *Server) handleRefreshOne(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if !s.sched.TriggerOne(id) {
		writeError(w, http.StatusConflict, "source is unknown or already fetching")
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]any{"triggered": 1, "source": id})
}

func handleAPINotFound(w http.ResponseWriter, r *http.Request) {
	writeError(w, http.StatusNotFound, "unknown endpoint")
}

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	enabled := 0
	for _, sc := range s.cfg.Sources {
		if sc.IsEnabled() {
			enabled++
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"status":  "ok",
		"version": s.version,
		"uptime":  time.Since(s.started).Round(time.Second).String(),
		"sources": enabled,
	})
}

func (s *Server) staticHandler() http.Handler {
	dir := s.cfg.Server.WebDir
	fs := http.FileServer(http.Dir(dir))
	index := filepath.Join(dir, "index.html")
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" && !strings.HasPrefix(r.URL.Path, "/assets/") {
			if fi, err := os.Stat(filepath.Join(dir, filepath.Clean(r.URL.Path))); err != nil || fi.IsDir() {
				r.URL.Path = "/"
			}
		}
		if _, err := os.Stat(index); err != nil {
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			w.WriteHeader(http.StatusServiceUnavailable)
			fmt.Fprint(w, `<!doctype html><meta charset="utf-8"><title>newsfeed</title>
<body style="font:16px/1.6 ui-sans-serif,system-ui;max-width:40rem;margin:12vh auto;padding:0 1.5rem">
<h1>newsfeed</h1>
<p>The API is running, but the web app is not built yet.</p>
<pre style="background:#f3f3f3;padding:1rem;border-radius:8px">cd web
npm install
npm run build</pre>
<p>Or run the dev server with <code>npm run dev</code> and open <a href="http://localhost:5173">localhost:5173</a>.</p>
<p>The API lives under <a href="/api/v1/feed">/api/v1/feed</a>.</p>
</body>`)
			return
		}
		if strings.HasPrefix(r.URL.Path, "/assets/") {
			w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
		}
		fs.ServeHTTP(w, r)
	})
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	_ = enc.Encode(v)
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]any{"error": msg})
}

func encodeCursor(c model.Cursor) string {
	return base64.RawURLEncoding.EncodeToString([]byte(fmt.Sprintf("%d:%d", c.PublishedAt, c.ID)))
}

func decodeCursor(s string) (*model.Cursor, error) {
	raw, err := base64.RawURLEncoding.DecodeString(s)
	if err != nil {
		return nil, err
	}
	left, right, ok := strings.Cut(string(raw), ":")
	if !ok {
		return nil, fmt.Errorf("malformed cursor")
	}
	ts, err := strconv.ParseInt(left, 10, 64)
	if err != nil {
		return nil, err
	}
	id, err := strconv.ParseInt(right, 10, 64)
	if err != nil {
		return nil, err
	}
	return &model.Cursor{PublishedAt: ts, ID: id}, nil
}

func splitList(v string) []string {
	if strings.TrimSpace(v) == "" {
		return nil
	}
	seen := map[string]bool{}
	out := make([]string, 0, 8)
	for _, part := range strings.Split(v, ",") {
		part = strings.TrimSpace(part)
		if part == "" || seen[part] {
			continue
		}
		seen[part] = true
		out = append(out, part)
		if len(out) == 50 {
			break
		}
	}
	return out
}

type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (r *statusRecorder) WriteHeader(code int) {
	r.status = code
	r.ResponseWriter.WriteHeader(code)
}
