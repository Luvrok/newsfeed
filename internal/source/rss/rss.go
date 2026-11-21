// Package rss subscribes to any RSS or Atom feed.
package rss

import (
	"context"
	"fmt"
	"net/http"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/mmcdole/gofeed"

	"newsfeed/internal/content"
	"newsfeed/internal/model"
	"newsfeed/internal/source"
	"newsfeed/internal/source/httpx"
)

func init() {
	source.Register("rss", func() source.Fetcher { return &fetcher{cache: map[string]cacheEntry{}} })
}

type cacheEntry struct {
	etag         string
	lastModified string
}

type fetcher struct {
	mu    sync.Mutex
	cache map[string]cacheEntry
}

// Options:
//
//	url   = "https://example.com/feed.xml"   (or urls = ["...", "..."])
//	limit = 25
func (f *fetcher) Fetch(ctx context.Context, opts source.Options) ([]model.Item, error) {
	urls := opts.Strings("urls")
	if u := opts.String("url", ""); u != "" {
		urls = append(urls, u)
	}
	if len(urls) == 0 {
		return nil, fmt.Errorf("rss: options.url or options.urls is required")
	}
	limit := opts.Int("limit", 25)
	withSummary := opts.Bool("summary", true)

	var include *regexp.Regexp
	if pattern := strings.TrimSpace(opts.String("include", "")); pattern != "" {
		include, err = regexp.Compile(pattern)
		if err != nil {
			return nil, fmt.Errorf("rss: include: %w", err)
		}
	}

	client := httpx.Client(20*time.Second, "newsfeed/0.1 (+rss)", opts.String("proxy", ""))
	var (
		items []model.Item
		errs  []string
		seen  = map[string]bool{}
	)
	for _, url := range urls {
		batch, err := f.fetchOne(ctx, client, url, limit, withSummary)
		if err != nil {
			errs = append(errs, err.Error())
			continue
		}
		for _, it := range batch {
			if seen[it.URL] || (include != nil && !include.MatchString(it.URL)) {
				continue
			}
			seen[it.URL] = true
			items = append(items, it)
		}
	}
	if len(items) == 0 && len(errs) > 0 {
		return nil, fmt.Errorf("rss: %s", strings.Join(errs, "; "))
	}
	return items, nil
}

func (f *fetcher) fetchOne(ctx context.Context, client *http.Client, url string, limit int, withSummary bool) ([]model.Item, error) {
	f.mu.Lock()
	entry := f.cache[url]
	f.mu.Unlock()

	header := http.Header{}
	header.Set("Accept", "application/rss+xml, application/atom+xml, application/xml, text/xml;q=0.9, */*;q=0.8")
	if entry.etag != "" {
		header.Set("If-None-Match", entry.etag)
	}
	if entry.lastModified != "" {
		header.Set("If-Modified-Since", entry.lastModified)
	}

	body, respHeader, err := httpx.Get(ctx, client, url, header)
	if err == httpx.ErrNotModified {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}

	f.mu.Lock()
	f.cache[url] = cacheEntry{etag: respHeader.Get("ETag"), lastModified: respHeader.Get("Last-Modified")}
	f.mu.Unlock()

	feed, err := gofeed.NewParser().ParseString(string(body))
	if err != nil {
		return nil, fmt.Errorf("%s: parse feed: %w", feedHost(url), err)
	}
	out := make([]model.Item, 0, limit)
	for _, it := range feed.Items {
		if it == nil {
			continue
		}
		link := strings.TrimSpace(it.Link)
		if link == "" && len(it.Links) > 0 {
			link = strings.TrimSpace(it.Links[0])
		}
		title := httpx.StripTags(it.Title)
		if title == "" || link == "" {
			continue
		}
		published := time.Now()
		switch {
		case it.PublishedParsed != nil:
			published = *it.PublishedParsed
		case it.UpdatedParsed != nil:
			published = *it.UpdatedParsed
		}
		summary := it.Description
		if strings.TrimSpace(summary) == "" {
			summary = it.Content
		}
		summary = httpx.Truncate(httpx.Text(summary), 380)
		body, bodyFormat := "", ""
		if withSummary {
			rawBody := it.Content
			if strings.TrimSpace(rawBody) == "" {
				rawBody = it.Description
			}
			if strings.Contains(rawBody, "<") {
				body = content.SanitizeHTML(rawBody, link)
				if body != "" {
					bodyFormat = "html"
				}
			} else if rawBody = strings.TrimSpace(rawBody); rawBody != "" {
				body = rawBody
				bodyFormat = "text"
			}
		} else {
			summary = ""
		}

		author := ""
		if it.Author != nil {
			author = strings.TrimSpace(it.Author.Name)
		}
		if author == "" && len(it.Authors) > 0 && it.Authors[0] != nil {
			author = strings.TrimSpace(it.Authors[0].Name)
		}

		tags := make([]string, 0, 3)
		for _, c := range it.Categories {
			if c == "" || len(tags) >= 4 {
				break
			}
			tags = append(tags, c)
		}

		out = append(out, model.Item{
			ExternalID:    firstNonEmpty(it.GUID, link),
			URL:           link,
			Title:         httpx.Truncate(title, 240),
			Summary:       summary,
			Author:        author,
			Image:         imageOf(it),
			Content:       body,
			ContentFormat: bodyFormat,
			Published:     published,
			Tags:          dedupe(tags),
		})
		if len(out) >= limit {
			break
		}
	}
	return out, nil
}

func imageOf(it *gofeed.Item) string {
	if it.Image != nil && it.Image.URL != "" {
		return it.Image.URL
	}
	for _, e := range it.Enclosures {
		if e != nil && strings.HasPrefix(e.Type, "image/") && e.URL != "" {
			return e.URL
		}
	}
	if media, ok := it.Extensions["media"]; ok {
		for _, key := range []string{"content", "thumbnail"} {
			for _, ext := range media[key] {
				if u := ext.Attrs["url"]; u != "" {
					return u
				}
			}
		}
	}
	if itunes, ok := it.Extensions["itunes"]; ok {
		for _, ext := range itunes["image"] {
			if ext.Attrs["href"] != "" {
				return ext.Attrs["href"]
			}
		}
	}
	if m := imgSrc.FindStringSubmatch(it.Content + " " + it.Description); len(m) > 1 {
		return m[1]
	}
	return ""
}

var imgSrc = regexp.MustCompile(`(?i)<img[^>]+src=["']([^"']+)["']`)

func dedupe(in []string) []string {
	seen := map[string]bool{}
	out := in[:0]
	for _, s := range in {
		s = strings.TrimSpace(s)
		if s == "" || seen[strings.ToLower(s)] {
			continue
		}
		seen[strings.ToLower(s)] = true
		out = append(out, s)
	}
	return out
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			return strings.TrimSpace(v)
		}
	}
	return ""
}

func feedHost(raw string) string {
	raw = strings.TrimPrefix(raw, "https://")
	raw = strings.TrimPrefix(raw, "http://")
	if i := strings.IndexByte(raw, '/'); i > 0 {
		return raw[:i]
	}
	return raw
}
