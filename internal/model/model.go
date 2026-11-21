// Package model holds the values shared by every layer of the service.
package model

import "time"

// Item is one story as delivered by a source. Sources fill as much as they
// can: Title and URL are the only mandatory fields.
type Item struct {
	ExternalID    string
	URL           string
	Title         string
	Summary       string
	Author        string
	Image         string
	Content       string
	ContentFormat string
	Published     time.Time
	Tags          []string
}

// Source is a subscription declared in the config file.
type Source struct {
	ID       string
	Kind     string
	Label    string
	SiteURL  string
	Group    string
	Interval time.Duration
}

// FeedItem is an item enriched with its source, ready for the API.
type FeedItem struct {
	ID            int64     `json:"id"`
	SourceID      string    `json:"source_id"`
	Source        string    `json:"source"`
	Kind          string    `json:"kind"`
	Group         string    `json:"group"`
	URL           string    `json:"url"`
	Title         string    `json:"title"`
	Summary       string    `json:"summary"`
	Author        string    `json:"author"`
	Image         string    `json:"image"`
	Content       string    `json:"content,omitempty"`
	ContentFormat string    `json:"content_format,omitempty"`
	PublishedAt   time.Time `json:"published_at"`
	Tags          []string  `json:"tags"`
}

// Cursor points at the last item of a page.
type Cursor struct {
	PublishedAt int64
	ID          int64
}

// FeedQuery describes one page request.
type FeedQuery struct {
	Limit   int
	Before  *Cursor
	Sources []string
	Kinds   []string
	Search  string
}

// SourceStats is the health and volume of one configured source.
type SourceStats struct {
	ID             string    `json:"id"`
	Kind           string    `json:"kind"`
	Label          string    `json:"label"`
	Group          string    `json:"group"`
	SiteURL        string    `json:"site_url,omitempty"`
	IntervalSec    int       `json:"interval_sec"`
	Items          int64     `json:"items"`
	LastRunAt      time.Time `json:"last_run_at,omitempty"`
	LastOKAt       time.Time `json:"last_ok_at,omitempty"`
	LastError      string    `json:"last_error,omitempty"`
	LastDurationMS int64     `json:"last_duration_ms"`
	LastNew        int64     `json:"last_new"`
	Runs           int64     `json:"runs"`
	Errors         int64     `json:"errors"`
	Status         string    `json:"status"`
}
