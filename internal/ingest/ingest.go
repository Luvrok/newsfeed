// Package ingest owns the running subscriptions. The database is the source
// of truth; the manager starts, restarts and stops one goroutine per source
// while the service is alive.
package ingest

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math/rand"
	"net/url"
	"reflect"
	"strings"
	"sync"
	"time"

	"newsfeed/internal/config"
	"newsfeed/internal/model"
	"newsfeed/internal/source"
	"newsfeed/internal/store"
)

// Job is one running subscription.
type Job struct {
	Source  model.Source
	fetcher source.Fetcher
	trig    chan struct{}
	cancel  context.CancelFunc
	done    chan struct{}
}

// Manager keeps the job map and the per-source lifecycle.
type Manager struct {
	cfg   *config.Config
	store *store.Store
	log   *slog.Logger

	mu      sync.Mutex
	jobs    map[string]*Job
	running map[string]bool
	root    context.Context
	cancel  context.CancelFunc
	started bool
}

// NewManager builds an empty manager. Call Start before serving requests.
func NewManager(cfg *config.Config, st *store.Store, log *slog.Logger) *Manager {
	return &Manager{
		cfg:     cfg,
		store:   st,
		log:     log,
		jobs:    map[string]*Job{},
		running: map[string]bool{},
	}
}

// Start loads enabled subscriptions from the database and starts their
// goroutines. It returns once every source has a job (or a recorded error).
func (m *Manager) Start(ctx context.Context) error {
	m.mu.Lock()
	if m.started {
		m.mu.Unlock()
		return errors.New("manager already started")
	}
	m.root, m.cancel = context.WithCancel(ctx)
	m.started = true
	m.mu.Unlock()

	sources, err := m.store.ListSources(ctx, false)
	if err != nil {
		return err
	}
	hostNext := map[string]time.Duration{}
	for i, src := range sources {
		if !src.Enabled {
			continue
		}
		initial := time.Duration(i*120)*time.Millisecond + jitter(time.Second)
		if host := sourceHost(src); host != "" {
			if next := hostNext[host]; next > initial {
				initial = next
			}
			hostNext[host] = initial + hostGap(src)
		}
		if err := m.start(src, initial); err != nil {
			m.log.Error("cannot start source", "source", src.ID, "kind", src.Kind, "err", err)
			_ = m.store.MarkRun(ctx, src.ID, 0, 0, err)
		}
	}
	go m.maintenance(m.root)
	return nil
}

// Close stops every goroutine and waits for them, with a bounded grace period.
func (m *Manager) Close() {
	m.mu.Lock()
	if !m.started {
		m.mu.Unlock()
		return
	}
	m.started = false
	if m.cancel != nil {
		m.cancel()
	}
	jobs := make([]*Job, 0, len(m.jobs))
	for _, job := range m.jobs {
		jobs = append(jobs, job)
	}
	m.jobs = map[string]*Job{}
	m.mu.Unlock()

	deadline := time.After(12 * time.Second)
	for _, job := range jobs {
		select {
		case <-job.done:
		case <-deadline:
			m.log.Warn("source did not stop in time", "source", job.Source.ID)
			return
		}
	}
}

// Add starts a newly created subscription. The source must already be stored.
func (m *Manager) Add(src model.Source) error {
	m.mu.Lock()
	started := m.started
	_, exists := m.jobs[src.ID]
	m.mu.Unlock()
	if !started {
		return errors.New("manager is not started")
	}
	if exists {
		return fmt.Errorf("source %q is already running", src.ID)
	}
	return m.start(src, jitter(700*time.Millisecond))
}

// Update applies an edited subscription. Metadata changes do not interrupt a
// job; a changed kind, interval or option set restarts it.
func (m *Manager) Update(src model.Source) error {
	m.mu.Lock()
	job, exists := m.jobs[src.ID]
	m.mu.Unlock()

	if !src.Enabled {
		if exists {
			m.stop(job)
		}
		return nil
	}
	if !exists {
		return m.Add(src)
	}

	m.mu.Lock()
	changed := job.Source.Kind != src.Kind ||
		job.Source.Interval != src.Interval ||
		!reflect.DeepEqual(job.Source.Options, src.Options)
	if !changed {
		job.Source = src
	}
	m.mu.Unlock()

	if changed {
		m.stop(job)
		return m.start(src, jitter(700*time.Millisecond))
	}
	return nil
}

// Remove stops a subscription. The database row is soft-deleted by the API.
func (m *Manager) Remove(id string) {
	m.mu.Lock()
	job, ok := m.jobs[id]
	if ok {
		delete(m.jobs, id)
	}
	m.mu.Unlock()
	if ok {
		m.stop(job)
	}
}

// TriggerAll asks every source to poll now.
func (m *Manager) TriggerAll() int {
	m.mu.Lock()
	jobs := make([]*Job, 0, len(m.jobs))
	for _, job := range m.jobs {
		jobs = append(jobs, job)
	}
	m.mu.Unlock()
	n := 0
	for _, job := range jobs {
		select {
		case job.trig <- struct{}{}:
			n++
		default:
		}
	}
	return n
}

// TriggerOne asks one source to poll now.
func (m *Manager) TriggerOne(id string) bool {
	m.mu.Lock()
	job, ok := m.jobs[id]
	m.mu.Unlock()
	if !ok {
		return false
	}
	select {
	case job.trig <- struct{}{}:
		return true
	default:
		return false
	}
}

func (m *Manager) start(src model.Source, initial time.Duration) error {
	fetcher, err := source.New(src.Kind)
	if err != nil {
		return err
	}
	src.Options = m.withProxy(src.Options)

	m.mu.Lock()
	if m.root == nil {
		m.mu.Unlock()
		return errors.New("manager is not started")
	}
	if _, exists := m.jobs[src.ID]; exists {
		m.mu.Unlock()
		return fmt.Errorf("source %q is already running", src.ID)
	}
	ctx, cancel := context.WithCancel(m.root)
	job := &Job{
		Source:  src,
		fetcher: fetcher,
		trig:    make(chan struct{}, 1),
		cancel:  cancel,
		done:    make(chan struct{}),
	}
	m.jobs[src.ID] = job
	m.mu.Unlock()

	go m.loop(ctx, job, initial)
	return nil
}

func (m *Manager) stop(job *Job) {
	job.cancel()
	select {
	case <-job.done:
	case <-time.After(12 * time.Second):
		m.log.Warn("source did not stop in time", "source", job.Source.ID)
	}
}

func (m *Manager) withProxy(options map[string]any) map[string]any {
	out := make(map[string]any, len(options)+1)
	for key, value := range options {
		out[key] = value
	}
	if m.cfg.Fetch.Proxy != "" && out["proxy"] == nil {
		out["proxy"] = m.cfg.Fetch.Proxy
	}
	return out
}

func (m *Manager) loop(ctx context.Context, job *Job, initial time.Duration) {
	defer close(job.done)
	timer := time.NewTimer(initial)
	defer timer.Stop()

	backoff := 0
	for {
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
		case <-job.trig:
			if !timer.Stop() {
				select {
				case <-timer.C:
				default:
				}
			}
		}

		start := time.Now()
		err := m.run(ctx, job)
		took := time.Since(start)

		delay := job.Source.Interval
		if err != nil {
			backoff++
			if backoff > 4 {
				backoff = 4
			}
			delay = minDuration(delay*time.Duration(1<<backoff), 6*time.Hour)
			m.log.Warn("fetch failed", "source", job.Source.ID, "kind", job.Source.Kind,
				"err", err, "retry_in", delay.Round(time.Second), "took", took.Round(time.Millisecond))
		} else {
			backoff = 0
		}
		// A manual refresh that arrived during the fetch is already served.
		select {
		case <-job.trig:
		default:
		}
		timer.Reset(jitter(delay))
	}
}

func (m *Manager) run(ctx context.Context, job *Job) error {
	if !m.begin(job.Source.ID) {
		return nil
	}
	defer m.end(job.Source.ID)

	timeout := m.cfg.Fetch.Timeout.Std()
	if job.Source.Kind == "reddit" {
		timeout = 3 * time.Minute
	}
	fctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	start := time.Now()
	items, err := job.fetcher.Fetch(fctx, source.Options(job.Source.Options))
	if len(items) > m.cfg.Fetch.MaxItems {
		items = items[:m.cfg.Fetch.MaxItems]
	}
	newItems := 0
	if err == nil {
		newItems, err = m.store.InsertItems(ctx, job.Source.ID, items)
	}
	elapsed := time.Since(start)
	if markErr := m.store.MarkRun(ctx, job.Source.ID, elapsed, newItems, err); markErr != nil {
		m.log.Error("record source state", "source", job.Source.ID, "err", markErr)
	}
	if err != nil {
		return err
	}
	m.log.Info("fetched", "source", job.Source.ID, "kind", job.Source.Kind,
		"items", len(items), "new", newItems, "took", elapsed.Round(time.Millisecond))
	return nil
}

func (m *Manager) begin(id string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.running[id] {
		return false
	}
	m.running[id] = true
	return true
}

func (m *Manager) end(id string) {
	m.mu.Lock()
	delete(m.running, id)
	m.mu.Unlock()
}

func (m *Manager) maintenance(ctx context.Context) {
	prune := func() {
		n, err := m.store.Prune(ctx, m.cfg.Fetch.RetentionDays)
		if err != nil {
			m.log.Error("prune", "err", err)
			return
		}
		if n > 0 {
			m.log.Info("pruned old items", "count", n, "retention_days", m.cfg.Fetch.RetentionDays)
		}
	}
	prune()
	ticker := time.NewTicker(6 * time.Hour)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			prune()
		}
	}
}

// Result is the outcome of a one-shot fetch.
type Result struct {
	SourceID string        `json:"source_id"`
	Kind     string        `json:"kind"`
	Fetched  int           `json:"fetched"`
	New      int           `json:"new"`
	Duration time.Duration `json:"duration"`
	Err      error         `json:"-"`
}

// RunBatch fetches selected subscriptions once, with a worker pool. Used by
// "newsd fetch" so the same code path runs from cron and from serve.
func RunBatch(ctx context.Context, cfg *config.Config, st *store.Store, sources []model.Source, filter string, log *slog.Logger) []Result {
	selected := make([]model.Source, 0, len(sources))
	for _, src := range sources {
		if !src.Enabled {
			continue
		}
		if filter == "" || src.ID == filter {
			selected = append(selected, src)
		}
	}
	workers := cfg.Fetch.Workers
	if workers < 1 {
		workers = 1
	}
	if workers > len(selected) {
		workers = len(selected)
	}
	queue := make(chan model.Source)
	results := make([]Result, 0, len(selected))
	var mu sync.Mutex
	var wg sync.WaitGroup

	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for src := range queue {
				start := time.Now()
				fetcher, err := source.New(src.Kind)
				if err != nil {
					mu.Lock()
					results = append(results, Result{SourceID: src.ID, Kind: src.Kind, Err: err})
					mu.Unlock()
					continue
				}
				opts := withProxy(cfg, src.Options)
				timeout := cfg.Fetch.Timeout.Std()
				if src.Kind == "reddit" {
					timeout = 3 * time.Minute
				}
				fctx, cancel := context.WithTimeout(ctx, timeout)
				items, err := fetcher.Fetch(fctx, source.Options(opts))
				cancel()
				if len(items) > cfg.Fetch.MaxItems {
					items = items[:cfg.Fetch.MaxItems]
				}
				newItems := 0
				if err == nil {
					newItems, err = st.InsertItems(ctx, src.ID, items)
				}
				if mErr := st.MarkRun(ctx, src.ID, time.Since(start), newItems, err); mErr != nil && log != nil {
					log.Error("record source state", "source", src.ID, "err", mErr)
				}
				mu.Lock()
				results = append(results, Result{
					SourceID: src.ID,
					Kind:     src.Kind,
					Fetched:  len(items),
					New:      newItems,
					Duration: time.Since(start),
					Err:      err,
				})
				mu.Unlock()
			}
		}()
	}
	for _, src := range selected {
		queue <- src
	}
	close(queue)
	wg.Wait()
	return results
}

func withProxy(cfg *config.Config, options map[string]any) map[string]any {
	out := make(map[string]any, len(options)+1)
	for key, value := range options {
		out[key] = value
	}
	if cfg.Fetch.Proxy != "" && out["proxy"] == nil {
		out["proxy"] = cfg.Fetch.Proxy
	}
	return out
}

func jitter(d time.Duration) time.Duration {
	if d <= 0 {
		return time.Second
	}
	delta := time.Duration(rand.Int63n(int64(d/10) + 1))
	return d + delta - d/20
}

func minDuration(a, b time.Duration) time.Duration {
	if a < b {
		return a
	}
	return b
}

// sourceHost groups sources that hit the same upstream, so a seed with eleven
// subreddits does not start them all in the same second.
func sourceHost(src model.Source) string {
	switch src.Kind {
	case "reddit":
		return "reddit.com"
	case "telegram":
		return "t.me"
	}
	raw := source.Options(src.Options).String("url", "")
	if raw == "" {
		raw = source.Options(src.Options).String("instance", "")
	}
	if raw == "" {
		return ""
	}
	if !strings.Contains(raw, "://") {
		raw = "https://" + raw
	}
	u, err := url.Parse(raw)
	if err != nil {
		return ""
	}
	return strings.ToLower(u.Hostname())
}

func hostGap(src model.Source) time.Duration {
	switch src.Kind {
	case "reddit":
		return 30 * time.Second
	case "telegram":
		return 2 * time.Second
	case "mastodon":
		return 5 * time.Second
	case "rss":
		return 2 * time.Second
	default:
		return time.Second
	}
}
