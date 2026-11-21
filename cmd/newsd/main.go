// Command newsd runs the news aggregator: fetch, store, serve.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"sort"
	"strings"
	"syscall"
	"time"

	"newsfeed/internal/api"
	"newsfeed/internal/config"
	"newsfeed/internal/ingest"
	"newsfeed/internal/model"
	"newsfeed/internal/source"
	_ "newsfeed/internal/source/all"
	"newsfeed/internal/store"
)

// version is overridden at build time with -ldflags "-X main.version=...".
var version = "dev"

func main() {
	log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo}))
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}
	switch os.Args[1] {
	case "serve":
		os.Exit(cmdServe(os.Args[2:], log))
	case "fetch":
		os.Exit(cmdFetch(os.Args[2:], log))
	case "sources":
		os.Exit(cmdSources())
	case "version":
		fmt.Println("newsfeed", version)
	default:
		usage()
		os.Exit(2)
	}
}

func usage() {
	fmt.Fprintf(os.Stderr, `newsfeed — an extensible, self-hosted news collector

Usage:
  newsd serve   [-config config/newsfeed.toml] [-addr 127.0.0.1:8787] [-db data/newsfeed.db]
  newsd fetch   [-config config/newsfeed.toml] [source-id]
  newsd sources
  newsd version

Every configured source runs in its own goroutine and keeps its own interval.
`)
}

func cmdServe(args []string, log *slog.Logger) int {
	fs := flag.NewFlagSet("serve", flag.ExitOnError)
	cfgPath := fs.String("config", defaultConfigPath(), "path to TOML config")
	addr := fs.String("addr", "", "override server listen address")
	dbPath := fs.String("db", "", "override database path")
	fs.Parse(args)

	cfg := mustConfig(*cfgPath)
	if *addr != "" {
		cfg.Server.Addr = *addr
	}
	if *dbPath != "" {
		cfg.Database.Path = *dbPath
	}

	st, err := store.Open(cfg.Database.Path)
	if err != nil {
		log.Error("open database", "err", err)
		return 1
	}
	defer st.Close()

	jobs, err := ingest.NewJobs(cfg)
	if err != nil {
		log.Error("config", "err", err)
		return 1
	}
	sources := make([]model.Source, 0, len(jobs))
	for _, job := range jobs {
		sources = append(sources, model.Source{
			ID:       job.Config.ID,
			Kind:     job.Config.Kind,
			Label:    job.Config.Label,
			SiteURL:  job.Config.Site,
			Group:    job.Config.Group,
			Interval: job.Config.Interval.Std(),
		})
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if err := st.SyncSources(ctx, sources); err != nil {
		log.Error("sync sources", "err", err)
		return 1
	}

	sched := ingest.NewScheduler(cfg, jobs, st, log)
	go sched.Run(ctx)

	handler := api.New(cfg, st, sched, log, version).Handler()
	srv := &http.Server{
		Addr:              cfg.Server.Addr,
		Handler:           handler,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       15 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       90 * time.Second,
	}

	errCh := make(chan error, 1)
	go func() {
		log.Info("serving", "addr", "http://"+cfg.Server.Addr, "sources", len(jobs), "db", cfg.Database.Path)
		errCh <- srv.ListenAndServe()
	}()

	select {
	case <-ctx.Done():
		log.Info("shutting down")
	case err := <-errCh:
		if !errors.Is(err, http.ErrServerClosed) {
			log.Error("http server", "err", err)
			return 1
		}
	}
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_ = srv.Shutdown(shutdownCtx)
	return 0
}

func cmdFetch(args []string, log *slog.Logger) int {
	fs := flag.NewFlagSet("fetch", flag.ExitOnError)
	cfgPath := fs.String("config", defaultConfigPath(), "path to TOML config")
	dbPath := fs.String("db", "", "override database path")
	fs.Parse(args)
	filter := strings.TrimSpace(fs.Arg(0))

	cfg := mustConfig(*cfgPath)
	if *dbPath != "" {
		cfg.Database.Path = *dbPath
	}
	st, err := store.Open(cfg.Database.Path)
	if err != nil {
		log.Error("open database", "err", err)
		return 1
	}
	defer st.Close()

	jobs, err := ingest.NewJobs(cfg)
	if err != nil {
		log.Error("config", "err", err)
		return 1
	}
	sources := make([]model.Source, 0, len(jobs))
	for _, job := range jobs {
		sources = append(sources, model.Source{
			ID: job.Config.ID, Kind: job.Config.Kind, Label: job.Config.Label,
			SiteURL: job.Config.Site, Group: job.Config.Group, Interval: job.Config.Interval.Std(),
		})
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	if err := st.SyncSources(ctx, sources); err != nil {
		log.Error("sync sources", "err", err)
		return 1
	}

	results := ingest.RunBatch(ctx, cfg, st, jobs, filter, log)
	sort.Slice(results, func(i, j int) bool { return results[i].SourceID < results[j].SourceID })

	ok := 0
	var totalNew int
	for _, r := range results {
		if r.Err != nil {
			fmt.Printf("FAIL  %-22s %-10s %-8s %s\n", r.SourceID, r.Kind, r.Duration.Round(time.Millisecond), r.Err)
			continue
		}
		ok++
		totalNew += r.New
		fmt.Printf("ok    %-22s %-10s %3d items %3d new  %s\n", r.SourceID, r.Kind, r.Fetched, r.New, r.Duration.Round(time.Millisecond))
	}
	if filter != "" && len(results) == 0 {
		fmt.Fprintf(os.Stderr, "unknown source %q\n", filter)
		return 1
	}
	fmt.Printf("\n%d/%d sources ok, %d new items\n", ok, len(results), totalNew)
	if len(results) > 0 && ok == 0 {
		return 1
	}
	return 0
}

func cmdSources() int {
	fmt.Println("registered kinds:")
	for _, k := range source.Kinds() {
		fmt.Println("  " + k)
	}
	return 0
}

func mustConfig(path string) *config.Config {
	cfg, err := config.Load(path)
	if err != nil {
		fmt.Fprintln(os.Stderr, "config:", err)
		os.Exit(1)
	}
	return cfg
}

func defaultConfigPath() string {
	if v := os.Getenv("NEWSFEED_CONFIG"); v != "" {
		return v
	}
	return "config/newsfeed.toml"
}
