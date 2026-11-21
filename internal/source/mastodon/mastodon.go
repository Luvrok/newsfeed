// Package mastodon reads public timelines from any Mastodon instance.
package mastodon

import (
	"context"
	"fmt"
	"net/url"
	"strings"
	"time"

	"newsfeed/internal/model"
	"newsfeed/internal/source"
	"newsfeed/internal/source/httpx"
)

func init() {
	source.Register("mastodon", func() source.Fetcher { return &fetcher{} })
}

// Options:
//
//	instance = "mastodon.social"
//	tag      = "nixos"   # hashtag timeline; "local" for the local timeline
//	limit    = 40
func (f *fetcher) Fetch(ctx context.Context, opts source.Options) ([]model.Item, error) {
	instance := strings.TrimSpace(opts.String("instance", "mastodon.social"))
	instance = strings.TrimPrefix(instance, "https://")
	instance = strings.TrimPrefix(instance, "http://")
	instance = strings.Trim(instance, "/")
	if instance == "" {
		return nil, fmt.Errorf("mastodon: options.instance is empty")
	}
	tag := strings.TrimPrefix(strings.TrimSpace(opts.String("tag", "")), "#")
	limit := opts.Int("limit", 40)
	if limit < 1 || limit > 40 {
		limit = 40
	}

	var endpoint string
	switch {
	case tag == "local":
		endpoint = fmt.Sprintf("https://%s/api/v1/timelines/public?local=true&limit=%d", instance, limit)
	case tag != "":
		endpoint = fmt.Sprintf("https://%s/api/v1/timelines/tag/%s?limit=%d", instance, url.PathEscape(tag), limit)
	default:
		return nil, fmt.Errorf("mastodon: options.tag is required (a hashtag or \"local\")")
	}

	client := httpx.Client(20*time.Second, "newsfeed/0.1 (+mastodon)")
	var raw []status
	if _, err := httpx.GetJSON(ctx, client, endpoint, &raw); err != nil {
		return nil, fmt.Errorf("mastodon/%s: %w", instance, err)
	}

	out := make([]model.Item, 0, len(raw))
	for i := range raw {
		s := raw[i].effective()
		if s.ID == "" || s.URL == "" {
			continue
		}
		text := httpx.Text(s.Content)
		title, summary := split(text, 160, 380)
		if title == "" {
			continue
		}
		image := ""
		for _, m := range s.MediaAttachments {
			if m.PreviewURL != "" {
				image = m.PreviewURL
				break
			}
			if m.URL != "" {
				image = m.URL
				break
			}
		}
		tags := []string{}
		for _, t := range s.Tags {
			if t.Name != "" && len(tags) < 4 {
				tags = append(tags, "#"+t.Name)
			}
		}
		published := s.CreatedAt
		if published.IsZero() {
			published = time.Now()
		}
		out = append(out, model.Item{
			ExternalID: s.ID,
			URL:        s.URL,
			Title:      title,
			Summary:    summary,
			Author:     "@" + s.Account.Acct,
			Image:      image,
			Published:  published,
			Tags:       tags,
		})
	}
	return out, nil
}

type status struct {
	ID               string    `json:"id"`
	URL              string    `json:"url"`
	Content          string    `json:"content"`
	CreatedAt        time.Time `json:"created_at"`
	Account          account   `json:"account"`
	MediaAttachments []media   `json:"media_attachments"`
	Tags             []tag     `json:"tags"`
	Reblog           *status   `json:"reblog"`
}

func (s status) effective() status {
	if s.Reblog != nil {
		return *s.Reblog
	}
	return s
}

type account struct {
	Acct        string `json:"acct"`
	DisplayName string `json:"display_name"`
}

type media struct {
	Type       string `json:"type"`
	URL        string `json:"url"`
	PreviewURL string `json:"preview_url"`
}

type tag struct {
	Name string `json:"name"`
}

// split turns a post into a headline and the remaining text, so a card does
// not print the same sentence twice.
func split(text string, titleRunes, summaryRunes int) (string, string) {
	text = strings.TrimSpace(text)
	if text == "" {
		return "", ""
	}
	first, rest, _ := strings.Cut(text, "\n")
	first = strings.TrimSpace(first)
	if first == "" {
		first, rest, _ = strings.Cut(text, ". ")
		first += "."
	}
	title := httpx.Truncate(first, titleRunes)
	rest = strings.TrimSpace(rest)
	if rest == "" && len([]rune(text)) > titleRunes {
		rest = strings.TrimSpace(strings.TrimPrefix(text, first))
	}
	if rest == title {
		rest = ""
	}
	return title, httpx.Truncate(rest, summaryRunes)
}

type fetcher struct{}
