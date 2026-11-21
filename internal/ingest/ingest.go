// Package ingest schedules source fetchers and writes their results to the store.
package ingest

import (
	"context"
	"fmt"
	"log/slog"
	"math/rand"
	"sync"
	"time"

	"newsfeed/internal/config"
	"newsfeed/internal/source"
	"newsfeed/internal/store"
)

// Job is one configured subscription bound to its fetcher.
type Job struct {
	Config  config.Source
	fetcher source.Fetcher
	trig    chan struct{}
}

// NewJobs validates the config and builds every fetcher. An unknown kind is a
// startup error, so a typo in the config never fails silently at 3 am.
func NewJobs(cfg *config.Config) ([]*Job, error) {
	jobs := make([]*Job, 0, len(cfg.Sources))
	for _, sc := range cfg.Sources {
		if !sc.IsEnabled() {
			continue
		}
		if cfg.Fetch.Proxy != "" && sc.Options["proxy"] == nil {
			sc.Options["proxy"] = cfg.Fetch.Proxy
		}
		f, err := source.New(sc.Kind)
		if err != nil {
			return nil, fmt.Errorf("source %q: %w", sc.ID, err)
		}
		jobs = append(jobs, &Job{Config: sc, fetcher: f, trig: make(chan struct{}, 1)})
	}
	if len(jobs) == 0 {
		return nil, fmt.Errorf("no enabled sources in %s", cfg.Path)
	}
	return jobs, nil
}

// Scheduler runs one goroutine per source.
type Scheduler struct {
	cfg   *config.Config
	jobs  []*Job
	store *store.Store
	log   *slog.Logger

	mu      sync.Mutex
	running map[string]bool
}

// NewScheduler builds a scheduler over ready jobs.
func NewScheduler(cfg *config.Config, jobs []*Job, st *store.Store, log *slog.Logger) *Scheduler {
	return &Scheduler{cfg: cfg, jobs: jobs, store: st, log: log, running: map[string]bool{}}
}

// Run blocks until ctx is cancelled.
func (s *Scheduler) Run(ctx context.Context) {
	var wg sync.WaitGroup
	for i, job := range s.jobs {
		wg.Add(1)
		go func(i int, job *Job) {
			defer wg.Done()
			// Stagger the first poll a little so twenty feeds do not all hit
			// the network in the same millisecond.
			initial := time.Duration(i*120)*time.Millisecond + jitter(time.Second)
			s.loop(ctx, job, initial)
		}(i, job)
	}
	wg.Add(1)
	go func() {
		defer wg.Done()
		s.maintenance(ctx)
	}()
	wg.Wait()
}

func (s *Scheduler) loop(ctx context.Context, job *Job, initial time.Duration) {
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
		err := s.run(ctx, job)
		dur := time.Since(start)

		delay := job.Config.Interval.Std()
		if err != nil {
			backoff++
			if backoff > 4 {
				backoff = 4
			}
			mult := time.Duration(1 << backoff)
			delay = minDuration(delay*mult, 6*time.Hour)
			s.log.Warn("fetch failed", "source", job.Config.ID, "kind", job.Config.Kind,
				"err", err, "retry_in", delay.Round(time.Second), "took", dur.Round(time.Millisecond))
		} else {
			backoff = 0
		}
		// A manual refresh that arrived during the fetch is already served by
		// this run; drop it instead of polling twice in a row.
		select {
		case <-job.trig:
		default:
		}
		timer.Reset(jitter(delay))
	}
}

func (s *Scheduler) run(ctx context.Context, job *Job) error {
	if !s.begin(job.Config.ID) {
		return nil // already running because of a manual refresh
	}
	defer s.end(job.Config.ID)

	fctx, cancel := context.WithTimeout(ctx, s.cfg.Fetch.Timeout.Std())
	defer cancel()

	start := time.Now()
	items, err := job.fetcher.Fetch(fctx, source.Options(job.Config.Options))
	if len(items) > s.cfg.Fetch.MaxItems {
		items = items[:s.cfg.Fetch.MaxItems]
	}
	newItems := 0
	if err == nil {
		newItems, err = s.store.InsertItems(ctx, job.Config.ID, items)
	}
	elapsed := time.Since(start)
	if err := s.store.MarkRun(ctx, job.Config.ID, elapsed, newItems, err); err != nil {
		s.log.Error("record source state", "source", job.Config.ID, "err", err)
	}
	if err != nil {
		return err
	}
	s.log.Info("fetched", "source", job.Config.ID, "kind", job.Config.Kind,
		"items", len(items), "new", newItems, "took", elapsed.Round(time.Millisecond))
	return nil
}

func (s *Scheduler) begin(id string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.running[id] {
		return false
	}
	s.running[id] = true
	return true
}

func (s *Scheduler) end(id string) {
	s.mu.Lock()
	delete(s.running, id)
	s.mu.Unlock()
}

// Trigger asks every source to poll now. Sources already polling are skipped.
// It returns how many subscriptions were signalled.
func (s *Scheduler) Trigger() int {
	n := 0
	for _, job := range s.jobs {
		select {
		case job.trig <- struct{}{}:
			n++
		default:
		}
	}
	return n
}

// TriggerOne asks a single source to poll now.
func (s *Scheduler) TriggerOne(id string) bool {
	for _, job := range s.jobs {
		if job.Config.ID == id {
			select {
			case job.trig <- struct{}{}:
				return true
			default:
				return false
			}
		}
	}
	return false
}

func (s *Scheduler) maintenance(ctx context.Context) {
	prune := func() {
		n, err := s.store.Prune(ctx, s.cfg.Fetch.RetentionDays)
		if err != nil {
			s.log.Error("prune", "err", err)
			return
		}
		if n > 0 {
			s.log.Info("pruned old items", "count", n, "retention_days", s.cfg.Fetch.RetentionDays)
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

// Result is the outcome of a single one-shot fetch.
type Result struct {
	SourceID string        `json:"source_id"`
	Kind     string        `json:"kind"`
	Fetched  int           `json:"fetched"`
	New      int           `json:"new"`
	Duration time.Duration `json:"duration"`
	Err      error         `json:"-"`
}

// RunBatch fetches selected jobs once, with a worker pool. Used by
// "newsd fetch" so the same code path is exercised from cron and from serve.
func RunBatch(ctx context.Context, cfg *config.Config, st *store.Store, jobs []*Job, filter string, log *slog.Logger) []Result {
	selected := make([]*Job, 0, len(jobs))
	for _, job := range jobs {
		if filter == "" || job.Config.ID == filter {
			selected = append(selected, job)
		}
	}
	workers := cfg.Fetch.Workers
	if workers < 1 {
		workers = 1
	}
	if workers > len(selected) {
		workers = len(selected)
	}
	queue := make(chan *Job)
	results := make([]Result, 0, len(selected))
	var mu sync.Mutex
	var wg sync.WaitGroup

	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for job := range queue {
				start := time.Now()
				fctx, cancel := context.WithTimeout(ctx, cfg.Fetch.Timeout.Std())
				items, err := job.fetcher.Fetch(fctx, source.Options(job.Config.Options))
				cancel()
				if len(items) > cfg.Fetch.MaxItems {
					items = items[:cfg.Fetch.MaxItems]
				}
				newItems := 0
				if err == nil {
					newItems, err = st.InsertItems(ctx, job.Config.ID, items)
				}
				if mErr := st.MarkRun(ctx, job.Config.ID, time.Since(start), newItems, err); mErr != nil && log != nil {
					log.Error("record source state", "source", job.Config.ID, "err", mErr)
				}
				mu.Lock()
				results = append(results, Result{
					SourceID: job.Config.ID,
					Kind:     job.Config.Kind,
					Fetched:  len(items),
					New:      newItems,
					Duration: time.Since(start),
					Err:      err,
				})
				mu.Unlock()
			}
		}()
	}
	for _, job := range selected {
		queue <- job
	}
	close(queue)
	wg.Wait()
	return results
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
