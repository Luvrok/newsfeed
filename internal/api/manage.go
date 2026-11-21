package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
	"time"

	"newsfeed/internal/model"
	"newsfeed/internal/opml"
	"newsfeed/internal/source"
)

type sourceRequest struct {
	Kind        string         `json:"kind"`
	Label       string         `json:"label"`
	Group       string         `json:"group"`
	Site        string         `json:"site"`
	IntervalSec int            `json:"interval_sec"`
	Enabled     *bool          `json:"enabled"`
	Options     map[string]any `json:"options"`
}

type managedSource struct {
	ID             string         `json:"id"`
	Kind           string         `json:"kind"`
	Label          string         `json:"label"`
	Group          string         `json:"group"`
	Site           string         `json:"site"`
	IntervalSec    int            `json:"interval_sec"`
	Enabled        bool           `json:"enabled"`
	Origin         string         `json:"origin"`
	Options        map[string]any `json:"options"`
	Status         string         `json:"status"`
	Items          int64          `json:"items"`
	LastRunAt      string         `json:"last_run_at,omitempty"`
	LastOKAt       string         `json:"last_ok_at,omitempty"`
	LastError      string         `json:"last_error,omitempty"`
	LastDurationMS int64          `json:"last_duration_ms,omitempty"`
}

func (s *Server) handleKinds(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"kinds": source.Descriptors()})
}

func (s *Server) handleManagedSources(w http.ResponseWriter, r *http.Request) {
	all, err := s.store.ListSources(r.Context(), false)
	if err != nil {
		s.log.Error("list sources", "err", err)
		writeError(w, http.StatusInternalServerError, "could not read sources")
		return
	}
	stats, err := s.store.Stats(r.Context())
	if err != nil {
		s.log.Error("stats", "err", err)
		writeError(w, http.StatusInternalServerError, "could not read source state")
		return
	}
	statusByID := map[string]model.SourceStats{}
	now := time.Now()
	for _, st := range stats.Sources {
		st.Status = sourceStatus(st, now)
		statusByID[st.ID] = st
	}
	out := make([]managedSource, 0, len(all))
	for _, src := range all {
		item := managedSource{
			ID:          src.ID,
			Kind:        src.Kind,
			Label:       src.Label,
			Group:       src.Group,
			Site:        src.SiteURL,
			IntervalSec: int(src.Interval.Seconds()),
			Enabled:     src.Enabled,
			Origin:      src.Origin,
			Options:     redactOptions(src.Kind, src.Options),
			Status:      "paused",
		}
		if src.Enabled {
			if st, ok := statusByID[src.ID]; ok {
				item.Status = st.Status
				item.Items = st.Items
				item.LastError = st.LastError
				item.LastDurationMS = st.LastDurationMS
				if !st.LastRunAt.IsZero() {
					item.LastRunAt = st.LastRunAt.Format(time.RFC3339)
				}
				if !st.LastOKAt.IsZero() {
					item.LastOKAt = st.LastOKAt.Format(time.RFC3339)
				}
			} else {
				item.Status = "never"
			}
		}
		out = append(out, item)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Enabled != out[j].Enabled {
			return out[i].Enabled
		}
		return out[i].Label < out[j].Label
	})
	writeJSON(w, http.StatusOK, map[string]any{"sources": out})
}

func (s *Server) handleCreateSource(w http.ResponseWriter, r *http.Request) {
	var req sourceRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if _, ok := source.DescriptorFor(req.Kind); !ok {
		writeError(w, http.StatusBadRequest, "unknown source kind")
		return
	}
	if req.Options == nil {
		req.Options = map[string]any{}
	}
	if err := source.ValidateOptions(req.Kind, source.Options(req.Options), s.cfg.Fetch.AllowPrivate); err != nil {
		writeError(w, http.StatusUnprocessableEntity, err.Error())
		return
	}
	all, err := s.store.ListSources(r.Context(), true)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not read sources")
		return
	}
	if existing := findDuplicate(all, req.Kind, req.Options, ""); existing != "" {
		writeError(w, http.StatusConflict, "this source already exists as "+existing)
		return
	}
	enabled := true
	if req.Enabled != nil {
		enabled = *req.Enabled
	}
	interval := clampInterval(req.IntervalSec)
	src := model.Source{
		ID:       uniqueSourceID(all, req.Kind, req.Label),
		Kind:     req.Kind,
		Label:    defaultLabel(req.Kind, req.Label),
		SiteURL:  strings.TrimSpace(req.Site),
		Group:    defaultGroup(req.Group),
		Interval: time.Duration(interval) * time.Second,
		Enabled:  enabled,
		Origin:   "ui",
		Options:  req.Options,
	}
	if err := s.store.CreateSource(r.Context(), src); err != nil {
		s.log.Error("create source", "err", err)
		writeError(w, http.StatusInternalServerError, "could not save the source")
		return
	}
	if enabled {
		if err := s.mgr.Add(src); err != nil {
			_ = s.store.UpdateSource(r.Context(), model.Source{
				ID: src.ID, Kind: src.Kind, Label: src.Label, SiteURL: src.SiteURL,
				Group: src.Group, Interval: src.Interval, Enabled: false, Options: src.Options,
			})
			writeError(w, http.StatusUnprocessableEntity, "source saved but did not start: "+err.Error())
			return
		}
	}
	writeJSON(w, http.StatusCreated, map[string]any{"source": s.managedOne(r.Context(), src)})
}

func (s *Server) handleUpdateSource(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	existing, err := s.store.SourceByID(r.Context(), id)
	if errors.Is(err, sql.ErrNoRows) {
		writeError(w, http.StatusNotFound, "source not found")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not read the source")
		return
	}
	var req sourceRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	updated := existing
	if req.Kind != "" {
		updated.Kind = req.Kind
	}
	if req.Label != "" {
		updated.Label = req.Label
	}
	if req.Group != "" {
		updated.Group = defaultGroup(req.Group)
	}
	if req.Site != "" {
		updated.SiteURL = req.Site
	}
	if req.IntervalSec > 0 {
		updated.Interval = time.Duration(clampInterval(req.IntervalSec)) * time.Second
	}
	if req.Enabled != nil {
		updated.Enabled = *req.Enabled
	}
	if req.Options != nil {
		updated.Options = req.Options
	}
	if updated.Options == nil {
		updated.Options = map[string]any{}
	}
	if _, ok := source.DescriptorFor(updated.Kind); !ok {
		writeError(w, http.StatusBadRequest, "unknown source kind")
		return
	}
	if err := source.ValidateOptions(updated.Kind, source.Options(updated.Options), s.cfg.Fetch.AllowPrivate); err != nil {
		writeError(w, http.StatusUnprocessableEntity, err.Error())
		return
	}
	all, err := s.store.ListSources(r.Context(), true)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not read sources")
		return
	}
	if dup := findDuplicate(all, updated.Kind, updated.Options, id); dup != "" {
		writeError(w, http.StatusConflict, "this source already exists as "+dup)
		return
	}
	if err := s.store.UpdateSource(r.Context(), updated); err != nil {
		s.log.Error("update source", "id", id, "err", err)
		writeError(w, http.StatusInternalServerError, "could not save the source")
		return
	}
	if err := s.mgr.Update(updated); err != nil {
		writeError(w, http.StatusUnprocessableEntity, "saved but did not restart: "+err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"source": s.managedOne(r.Context(), updated)})
}

func (s *Server) handleDeleteSource(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if err := s.store.SoftDeleteSource(r.Context(), id); errors.Is(err, sql.ErrNoRows) {
		writeError(w, http.StatusNotFound, "source not found")
		return
	} else if err != nil {
		s.log.Error("delete source", "id", id, "err", err)
		writeError(w, http.StatusInternalServerError, "could not delete the source")
		return
	}
	s.mgr.Remove(id)
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleProbe(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Kind    string         `json:"kind"`
		Options map[string]any `json:"options"`
	}
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if _, ok := source.DescriptorFor(req.Kind); !ok {
		writeError(w, http.StatusBadRequest, "unknown source kind")
		return
	}
	if req.Options == nil {
		req.Options = map[string]any{}
	}
	if err := source.ValidateOptions(req.Kind, source.Options(req.Options), s.cfg.Fetch.AllowPrivate); err != nil {
		writeError(w, http.StatusUnprocessableEntity, err.Error())
		return
	}
	fetcher, err := source.New(req.Kind)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()
	items, err := fetcher.Fetch(ctx, source.Options(req.Options))
	if err != nil {
		writeJSON(w, http.StatusOK, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	titles := make([]string, 0, 3)
	for _, item := range items {
		if len(titles) == 3 {
			break
		}
		titles = append(titles, item.Title)
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "count": len(items), "titles": titles})
}

func (s *Server) handleImportOPML(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseMultipartForm(6 << 20); err != nil {
		writeError(w, http.StatusBadRequest, "could not read the OPML file")
		return
	}
	file, _, err := r.FormFile("file")
	if err != nil {
		writeError(w, http.StatusBadRequest, "file field is required")
		return
	}
	defer file.Close()
	feeds, err := opml.Parse(file)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	all, err := s.store.ListSources(r.Context(), true)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not read sources")
		return
	}
	active, err := s.store.ListSources(r.Context(), false)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not read sources")
		return
	}
	known := map[string]bool{}
	for _, src := range active {
		if u, ok := src.Options["url"].(string); ok {
			known[strings.ToLower(strings.TrimSpace(u))] = true
		}
	}
	added, skipped := 0, 0
	failures := make([]string, 0, 4)
	for _, feed := range feeds {
		key := strings.ToLower(strings.TrimSpace(feed.XMLURL))
		if known[key] {
			skipped++
			continue
		}
		opts := map[string]any{"url": feed.XMLURL, "limit": 25}
		if err := source.ValidateOptions("rss", source.Options(opts), s.cfg.Fetch.AllowPrivate); err != nil {
			failures = append(failures, feed.XMLURL+": "+err.Error())
			continue
		}
		group := strings.TrimSpace(feed.Category)
		if group == "" {
			group = "Imported"
		}
		src := model.Source{
			ID:       uniqueSourceID(all, "rss", feed.Title),
			Kind:     "rss",
			Label:    defaultLabel("rss", feed.Title),
			SiteURL:  feed.HTMLURL,
			Group:    group,
			Interval: 15 * time.Minute,
			Enabled:  true,
			Origin:   "import",
			Options:  opts,
		}
		if err := s.store.CreateSource(r.Context(), src); err != nil {
			failures = append(failures, feed.XMLURL+": "+err.Error())
			continue
		}
		all = append(all, src)
		known[key] = true
		if err := s.mgr.Add(src); err != nil {
			s.log.Warn("imported source did not start", "source", src.ID, "err", err)
		}
		added++
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"added": added, "skipped": skipped, "failed": len(failures), "errors": failures,
	})
}

func (s *Server) managedOne(ctx context.Context, src model.Source) managedSource {
	stats, _ := s.store.Stats(ctx)
	item := managedSource{
		ID: src.ID, Kind: src.Kind, Label: src.Label, Group: src.Group, Site: src.SiteURL,
		IntervalSec: int(src.Interval.Seconds()), Enabled: src.Enabled, Origin: src.Origin,
		Options: redactOptions(src.Kind, src.Options), Status: "paused",
	}
	if !src.Enabled {
		return item
	}
	for _, st := range stats.Sources {
		if st.ID != src.ID {
			continue
		}
		st.Status = sourceStatus(st, time.Now())
		item.Status = st.Status
		item.Items = st.Items
		item.LastError = st.LastError
		item.LastDurationMS = st.LastDurationMS
		if !st.LastRunAt.IsZero() {
			item.LastRunAt = st.LastRunAt.Format(time.RFC3339)
		}
		if !st.LastOKAt.IsZero() {
			item.LastOKAt = st.LastOKAt.Format(time.RFC3339)
		}
	}
	if item.Status == "" {
		item.Status = "never"
	}
	return item
}

func redactOptions(kind string, options map[string]any) map[string]any {
	out := make(map[string]any, len(options))
	for key, value := range options {
		out[key] = value
	}
	desc, ok := source.DescriptorFor(kind)
	if !ok {
		return out
	}
	for _, field := range desc.Fields {
		if field.Type == source.FieldSecret {
			if _, exists := out[field.Key]; exists {
				out[field.Key] = "***"
			}
		}
	}
	return out
}

func findDuplicate(sources []model.Source, kind string, options map[string]any, excludeID string) string {
	key := sourceKey(kind, options)
	if key == "" {
		return ""
	}
	for _, src := range sources {
		if src.ID == excludeID || src.DeletedAt != 0 {
			continue
		}
		if sourceKey(src.Kind, src.Options) == key {
			return src.ID
		}
	}
	return ""
}

func sourceKey(kind string, options map[string]any) string {
	opts := source.Options(options)
	switch kind {
	case "rss":
		urls := opts.Strings("urls")
		if u := opts.String("url", ""); u != "" {
			urls = append(urls, u)
		}
		if len(urls) == 0 {
			return ""
		}
		return "rss:" + strings.ToLower(strings.TrimSpace(urls[0]))
	case "telegram":
		channels := opts.Strings("channels")
		if len(channels) == 0 {
			if c := opts.String("channel", ""); c != "" {
				channels = []string{c}
			}
		}
		if len(channels) == 0 {
			return ""
		}
		return "telegram:" + strings.ToLower(strings.TrimPrefix(strings.TrimSpace(channels[0]), "@"))
	case "reddit":
		subs := opts.Strings("subreddits")
		if len(subs) == 0 {
			if s := opts.String("subreddit", ""); s != "" {
				subs = []string{s}
			}
		}
		if len(subs) == 0 {
			return ""
		}
		sort.Strings(subs)
		return "reddit:" + strings.ToLower(strings.Join(subs, ",")) + ":" + opts.String("sort", "hot") + ":" + opts.String("time", "day")
	case "mastodon":
		return "mastodon:" + opts.String("instance", "") + ":" + opts.String("tag", "")
	case "github":
		repos := opts.Strings("repos")
		sort.Strings(repos)
		return "github:" + strings.Join(repos, ",")
	default:
		return ""
	}
}

func uniqueSourceID(sources []model.Source, kind, label string) string {
	existing := map[string]bool{}
	for _, src := range sources {
		existing[src.ID] = true
	}
	base := slug(kind + "-" + defaultLabel(kind, label))
	if base == "" {
		base = kind
	}
	if len(base) > 70 {
		base = base[:70]
	}
	id := base
	for n := 2; existing[id]; n++ {
		id = fmt.Sprintf("%s-%d", base, n)
	}
	return id
}

func defaultLabel(kind, label string) string {
	if label = strings.TrimSpace(label); label != "" {
		return label
	}
	return strings.ToUpper(kind[:1]) + kind[1:]
}

func defaultGroup(group string) string {
	if group = strings.TrimSpace(group); group != "" {
		return group
	}
	return "Imported"
}

func clampInterval(sec int) int {
	if sec < 30 {
		return 600
	}
	if sec > 24*3600 {
		return 24 * 3600
	}
	return sec
}

func slug(value string) string {
	var b strings.Builder
	lastDash := false
	for _, r := range strings.ToLower(value) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
			lastDash = false
		case r == '-' || r == '_' || r == ' ' || r == '.':
			if !lastDash && b.Len() > 0 {
				b.WriteByte('-')
				lastDash = true
			}
		}
	}
	return strings.Trim(b.String(), "-")
}

func decodeJSON(r *http.Request, dst any) error {
	defer r.Body.Close()
	decoder := json.NewDecoder(io.LimitReader(r.Body, 1<<20))
	if err := decoder.Decode(dst); err != nil {
		return fmt.Errorf("invalid JSON: %w", err)
	}
	return nil
}
