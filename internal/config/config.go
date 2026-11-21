// Package config loads the TOML subscription list.
package config

import (
	"fmt"
	"strings"
	"time"

	"github.com/BurntSushi/toml"
)

// Duration is a time.Duration written as "10m" in TOML.
type Duration time.Duration

// UnmarshalText implements encoding.TextUnmarshaler.
func (d *Duration) UnmarshalText(text []byte) error {
	v, err := time.ParseDuration(strings.TrimSpace(string(text)))
	if err != nil {
		return err
	}
	*d = Duration(v)
	return nil
}

// Std returns the standard library duration.
func (d Duration) Std() time.Duration { return time.Duration(d) }

// Config is the whole config file.
type Config struct {
	Server   Server   `toml:"server"`
	Database Database `toml:"database"`
	Fetch    Fetch    `toml:"fetch"`
	Sources  []Source `toml:"sources"`

	Path string `toml:"-"`
}

type Server struct {
	Addr          string `toml:"addr"`
	WebDir        string `toml:"web_dir"`
	AllowedOrigin string `toml:"allowed_origin"`
}

type Database struct {
	Path string `toml:"path"`
}

type Fetch struct {
	Timeout       Duration `toml:"timeout"`
	MaxItems      int      `toml:"max_items"`
	RetentionDays int      `toml:"retention_days"`
	Workers       int      `toml:"workers"`
	UserAgent     string   `toml:"user_agent"`
}

// Source is one subscription.
type Source struct {
	ID       string         `toml:"id"`
	Kind     string         `toml:"kind"`
	Label    string         `toml:"label"`
	Site     string         `toml:"site"`
	Group    string         `toml:"group"`
	Interval Duration       `toml:"interval"`
	Enabled  *bool          `toml:"enabled"`
	Options  map[string]any `toml:"options"`
}

// IsEnabled reports whether the subscription should run.
func (s Source) IsEnabled() bool { return s.Enabled == nil || *s.Enabled }

// Load reads a config file and applies defaults.
func Load(path string) (*Config, error) {
	cfg := &Config{
		Server: Server{
			Addr:   "127.0.0.1:8787",
			WebDir: "web/dist",
		},
		Database: Database{Path: "data/newsfeed.db"},
		Fetch: Fetch{
			Timeout:       Duration(40 * time.Second),
			MaxItems:      300,
			RetentionDays: 30,
			Workers:       8,
			UserAgent:     "newsfeed/0.1 (+https://github.com/barnard/newsfeed)",
		},
		Path: path,
	}
	if _, err := toml.DecodeFile(path, cfg); err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	if err := cfg.normalize(); err != nil {
		return nil, err
	}
	return cfg, nil
}

func (c *Config) normalize() error {
	if c.Server.Addr == "" {
		c.Server.Addr = "127.0.0.1:8787"
	}
	if c.Database.Path == "" {
		c.Database.Path = "data/newsfeed.db"
	}
	if c.Fetch.Timeout.Std() <= 0 {
		c.Fetch.Timeout = Duration(40 * time.Second)
	}
	if c.Fetch.MaxItems <= 0 {
		c.Fetch.MaxItems = 300
	}
	if c.Fetch.Workers <= 0 {
		c.Fetch.Workers = 8
	}
	if c.Fetch.RetentionDays <= 0 {
		c.Fetch.RetentionDays = 30
	}
	if c.Fetch.UserAgent == "" {
		c.Fetch.UserAgent = "newsfeed/0.1"
	}

	seen := map[string]bool{}
	for i := range c.Sources {
		s := &c.Sources[i]
		s.ID = strings.TrimSpace(s.ID)
		s.Kind = strings.TrimSpace(s.Kind)
		if s.ID == "" {
			return fmt.Errorf("sources[%d]: id is empty", i)
		}
		if seen[s.ID] {
			return fmt.Errorf("sources[%d]: duplicate id %q", i, s.ID)
		}
		seen[s.ID] = true
		if s.Kind == "" {
			return fmt.Errorf("source %q: kind is empty", s.ID)
		}
		if s.Label == "" {
			s.Label = s.ID
		}
		if s.Group == "" {
			s.Group = "feeds"
		}
		if s.Interval.Std() <= 0 {
			s.Interval = Duration(10 * time.Minute)
		}
		if s.Interval.Std() < 30*time.Second {
			s.Interval = Duration(30 * time.Second)
		}
		if s.Options == nil {
			s.Options = map[string]any{}
		}
	}
	return nil
}
