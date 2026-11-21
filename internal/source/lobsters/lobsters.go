// Package lobsters reads the public JSON API of lobste.rs.
package lobsters

import (
	"context"
	"fmt"
	"strings"
	"time"

	"newsfeed/internal/model"
	"newsfeed/internal/source"
	"newsfeed/internal/source/httpx"
)

func init() {
	source.Register("lobsters", func() source.Fetcher { return &fetcher{} })
}

// Options:
//
//	tag   = ""      # optional tag feed, e.g. "go"
//	limit = 30
func (f *fetcher) Fetch(ctx context.Context, opts source.Options) ([]model.Item, error) {
	tag := strings.TrimSpace(opts.String("tag", ""))
	url := "https://lobste.rs/hottest.json"
	if tag != "" {
		url = "https://lobste.rs/t/" + tag + ".json"
	}
	limit := opts.Int("limit", 30)

	client := httpx.Client(20*time.Second, "newsfeed/0.1 (+lobsters)", opts.String("proxy", ""))
	var raw []story
	if _, err := httpx.GetJSON(ctx, client, url, &raw); err != nil {
		return nil, fmt.Errorf("lobsters: %w", err)
	}
	if len(raw) > limit {
		raw = raw[:limit]
	}

	out := make([]model.Item, 0, len(raw))
	for _, s := range raw {
		if s.Title == "" {
			continue
		}
		link := s.URL
		if link == "" {
			link = s.CommentsURL
		}
		if link == "" {
			continue
		}
		published, err := time.Parse(time.RFC3339, s.CreatedAt)
		if err != nil {
			published = time.Now()
		}
		tags := append([]string{}, s.Tags...)
		if s.Score > 0 {
			tags = append(tags, fmt.Sprintf("%d %s", s.Score, plural(s.Score, "point", "points")))
		}
		if s.CommentCount > 0 {
			tags = append(tags, fmt.Sprintf("%d %s", s.CommentCount, plural(s.CommentCount, "comment", "comments")))
		}
		body := httpx.Text(s.Description)
		bodyFormat := ""
		if body != "" {
			bodyFormat = "text"
		}
		out = append(out, model.Item{
			ExternalID:    s.ShortID,
			URL:           link,
			Title:         s.Title,
			Summary:       httpx.Truncate(body, 300),
			Author:        s.SubmitterUser,
			Content:       body,
			ContentFormat: bodyFormat,
			Published:     published,
			Tags:          tags,
		})
	}
	return out, nil
}

type story struct {
	ShortID       string   `json:"short_id"`
	CreatedAt     string   `json:"created_at"`
	Title         string   `json:"title"`
	URL           string   `json:"url"`
	Description   string   `json:"description"`
	Score         int      `json:"score"`
	CommentCount  int      `json:"comment_count"`
	SubmitterUser string   `json:"submitter_user"`
	Tags          []string `json:"tags"`
	CommentsURL   string   `json:"comments_url"`
}

type fetcher struct{}

func plural(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}
