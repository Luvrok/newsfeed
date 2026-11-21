// Package hackernews uses the official Hacker News Firebase API.
package hackernews

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"newsfeed/internal/content"
	"newsfeed/internal/model"
	"newsfeed/internal/source"
	"newsfeed/internal/source/httpx"
)

func init() {
	source.Register("hackernews", func() source.Fetcher { return &fetcher{} })
}

// Options:
//
//	list  = "top"   # top | new | best | ask | show | job
//	limit = 40
func (f *fetcher) Fetch(ctx context.Context, opts source.Options) ([]model.Item, error) {
	list := opts.String("list", "top")
	var endpoint string
	switch list {
	case "top", "new", "best", "ask", "show", "job":
		endpoint = "https://hacker-news.firebaseio.com/v0/" + list + "stories.json"
	default:
		endpoint = "https://hacker-news.firebaseio.com/v0/topstories.json"
	}
	limit := opts.Int("limit", 40)
	if limit < 1 {
		limit = 40
	}
	if limit > 200 {
		limit = 200
	}

	client := httpx.Client(20*time.Second, "newsfeed/0.1 (+hackernews)", opts.String("proxy", ""))
	ids := []int64{}
	if _, err := httpx.GetJSON(ctx, client, endpoint, &ids); err != nil {
		return nil, fmt.Errorf("hackernews: %w", err)
	}
	if len(ids) > limit {
		ids = ids[:limit]
	}

	stories := make([]*story, len(ids))
	var wg sync.WaitGroup
	sem := make(chan struct{}, 8)
	for i, id := range ids {
		wg.Add(1)
		go func(i int, id int64) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			var s story
			url := fmt.Sprintf("https://hacker-news.firebaseio.com/v0/item/%d.json", id)
			if _, err := httpx.GetJSON(ctx, client, url, &s); err == nil {
				stories[i] = &s
			}
		}(i, id)
	}
	wg.Wait()

	out := make([]model.Item, 0, len(ids))
	for _, s := range stories {
		if s == nil || s.Deleted || s.Dead || s.Title == "" {
			continue
		}
		if s.Type == "job" && list != "job" {
			continue // job ads are not news
		}
		link := strings.TrimSpace(s.URL)
		if link == "" {
			link = fmt.Sprintf("https://news.ycombinator.com/item?id=%d", s.ID)
		}
		tags := []string{}
		if s.Score > 0 {
			tags = append(tags, fmt.Sprintf("%d %s", s.Score, plural(s.Score, "point", "points")))
		}
		if s.Descendants > 0 {
			tags = append(tags, fmt.Sprintf("%d %s", s.Descendants, plural(s.Descendants, "comment", "comments")))
		}
		body := content.SanitizeHTML(s.Text, link)
		bodyFormat := ""
		if body != "" {
			bodyFormat = "html"
		}
		out = append(out, model.Item{
			ExternalID:    fmt.Sprint(s.ID),
			URL:           link,
			Title:         s.Title,
			Summary:       httpx.Truncate(httpx.Text(s.Text), 300),
			Author:        s.By,
			Content:       body,
			ContentFormat: bodyFormat,
			Published:     time.Unix(s.Time, 0),
			Tags:          tags,
		})
	}
	return out, nil
}

type story struct {
	ID          int64  `json:"id"`
	Type        string `json:"type"`
	By          string `json:"by"`
	Time        int64  `json:"time"`
	Title       string `json:"title"`
	URL         string `json:"url"`
	Text        string `json:"text"`
	Score       int    `json:"score"`
	Descendants int    `json:"descendants"`
	Dead        bool   `json:"dead"`
	Deleted     bool   `json:"deleted"`
}

type fetcher struct{}

func plural(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}
