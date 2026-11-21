// Package gdelt queries the free GDELT DOC 2.0 API, which indexes articles
// from thousands of news sites.
package gdelt

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"newsfeed/internal/model"
	"newsfeed/internal/source"
	"newsfeed/internal/source/httpx"
)

func init() {
	source.Register("gdelt", func() source.Fetcher { return &fetcher{} })
}

// Options:
//
//	query    = "climate change"   # GDELT search expression
//	timespan = "1d"               # 15min | 1h | 24h | 1d | 1w | 1m
//	max      = 50
//	language = ""                 # optional, e.g. "english"
func (f *fetcher) Fetch(ctx context.Context, opts source.Options) ([]model.Item, error) {
	query := strings.TrimSpace(opts.String("query", ""))
	if query == "" {
		return nil, fmt.Errorf("gdelt: options.query is required")
	}
	timespan := opts.String("timespan", "1d")
	max := opts.Int("max", 50)
	if max < 1 || max > 250 {
		max = 50
	}

	// GDELT asks for at most one request every five seconds and bans bursts.
	if err := pace(ctx); err != nil {
		return nil, err
	}

	if lang := strings.TrimSpace(opts.String("language", "")); lang != "" {
		query += " sourcelang:" + lang
	}
	q := url.Values{}
	q.Set("query", query)
	q.Set("mode", "artlist")
	q.Set("format", "json")
	q.Set("sort", "datedesc")
	q.Set("maxrecords", strconv.Itoa(max))
	q.Set("timespan", timespan)
	endpoint := "https://api.gdeltproject.org/api/v2/doc/doc?" + q.Encode()

	client := httpx.Client(30*time.Second, "newsfeed/0.1 (+gdelt)", opts.String("proxy", ""))
	body, _, err := httpx.Get(ctx, client, endpoint, nil)
	if err != nil {
		return nil, fmt.Errorf("gdelt: %w", err)
	}

	trimmed := strings.TrimSpace(string(body))
	if !strings.HasPrefix(trimmed, "{") {
		return nil, fmt.Errorf("gdelt: unexpected response: %s", httpx.Truncate(trimmed, 160))
	}
	var data response
	if err := json.Unmarshal([]byte(trimmed), &data); err != nil {
		return nil, fmt.Errorf("gdelt: decode: %w", err)
	}

	out := make([]model.Item, 0, len(data.Articles))
	for _, a := range data.Articles {
		if a.Title == "" || a.URL == "" {
			continue
		}
		tags := []string{}
		for _, v := range []string{a.Domain, a.SourceCountry, a.Language} {
			if v = strings.TrimSpace(v); v != "" && len(tags) < 4 {
				tags = append(tags, v)
			}
		}
		published := time.Now()
		if v := strings.TrimSpace(a.SeenDate); v != "" {
			if t, err := time.ParseInLocation("20060102150405", v, time.UTC); err == nil {
				published = t
			}
		}
		out = append(out, model.Item{
			ExternalID: a.URL,
			URL:        a.URL,
			Title:      httpx.Truncate(a.Title, 240),
			Author:     a.Domain,
			Image:      a.SocialImage,
			Published:  published,
			Tags:       tags,
		})
	}
	return out, nil
}

var (
	paceMu sync.Mutex
	lastAt time.Time
)

func pace(ctx context.Context) error {
	paceMu.Lock()
	defer paceMu.Unlock()
	if wait := 5*time.Second - time.Since(lastAt); wait > 0 {
		t := time.NewTimer(wait)
		select {
		case <-ctx.Done():
			t.Stop()
			return ctx.Err()
		case <-t.C:
		}
	}
	lastAt = time.Now()
	return nil
}

type response struct {
	Articles []struct {
		URL           string `json:"url"`
		Title         string `json:"title"`
		SeenDate      string `json:"seendate"`
		SocialImage   string `json:"socialimage"`
		Domain        string `json:"domain"`
		Language      string `json:"language"`
		SourceCountry string `json:"sourcecountry"`
	} `json:"articles"`
}

type fetcher struct{}
