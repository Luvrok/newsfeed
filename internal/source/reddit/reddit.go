// Package reddit reads public Reddit RSS. It deliberately avoids OAuth: the
// feed endpoint is free, has no app registration and works through the same
// proxy as the rest of the fetchers. Reddit may rate limit bursts, so a source
// with several subreddits spaces its requests.
package reddit

import (
	"context"
	"fmt"
	"html"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/mmcdole/gofeed"

	"newsfeed/internal/content"
	"newsfeed/internal/model"
	"newsfeed/internal/source"
	"newsfeed/internal/source/httpx"
)

func init() {
	source.Register("reddit", func() source.Fetcher { return &fetcher{} })
}

type fetcher struct{}

// Options:
//
//	subreddits = ["worldnews", "geopolitics"]
//	sort       = "hot"        # hot | new | top | rising
//	time       = "day"        # for sort = "top": hour | day | week | month | year | all
//	limit      = 10           # per subreddit
//	user_agent = "..."
//	proxy      = ""          # per-source override; "direct" bypasses the global proxy
func (f *fetcher) Fetch(ctx context.Context, opts source.Options) ([]model.Item, error) {
	subs := opts.Strings("subreddits")
	if len(subs) == 0 {
		if sub := opts.String("subreddit", ""); sub != "" {
			subs = []string{sub}
		}
	}
	if len(subs) == 0 {
		return nil, fmt.Errorf("reddit: options.subreddits is required")
	}
	sortBy := opts.String("sort", "hot")
	switch sortBy {
	case "hot", "new", "top", "rising":
	default:
		sortBy = "hot"
	}
	timeRange := opts.String("time", "day")
	limit := opts.Int("limit", 10)
	if limit < 1 || limit > 100 {
		limit = 10
	}
	base := strings.TrimRight(opts.String("base_url", "https://www.reddit.com"), "/")
	ua := opts.String("user_agent", "Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/131.0 Safari/537.36")
	client := httpx.Client(25*time.Second, ua, opts.String("proxy", ""))
	parser := gofeed.NewParser()

	var (
		out  []model.Item
		errs []string
	)
	for i, sub := range subs {
		if i > 0 {
			select {
			case <-ctx.Done():
				return out, ctx.Err()
			case <-time.After(900 * time.Millisecond):
			}
		}
		link := redditURL(base, sub, sortBy, timeRange, limit)
		body, _, err := httpx.Get(ctx, client, link, http.Header{
			"Accept": {"application/atom+xml, application/rss+xml, application/xml, text/xml"},
		})
		if err != nil {
			errs = append(errs, err.Error())
			continue
		}
		feed, err := parser.ParseString(string(body))
		if err != nil {
			errs = append(errs, fmt.Sprintf("reddit/%s: parse: %v", sub, err))
			continue
		}
		count := 0
		for _, item := range feed.Items {
			if item == nil || item.Title == "" || item.Link == "" {
				continue
			}
			published := time.Now()
			switch {
			case item.PublishedParsed != nil:
				published = *item.PublishedParsed
			case item.UpdatedParsed != nil:
				published = *item.UpdatedParsed
			}
			bodyHTML := content.SanitizeHTML(item.Description, item.Link)
			if bodyHTML == "" {
				bodyHTML = content.SanitizeHTML(item.Content, item.Link)
			}
			bodyFormat := ""
			if bodyHTML != "" {
				bodyFormat = "html"
			}
			summary := httpx.Truncate(httpx.Text(item.Description+" "+item.Content), 320)
			image := firstImage(item.Description + " " + item.Content)
			author := ""
			if item.Author != nil {
				author = strings.TrimPrefix(strings.TrimSpace(item.Author.Name), "/u/")
				if author != "" {
					author = "u/" + author
				}
			}
			tags := []string{"r/" + sub}
			for _, category := range item.Categories {
				if category != "" && len(tags) < 4 {
					tags = append(tags, category)
				}
			}
			out = append(out, model.Item{
				ExternalID:    item.GUID,
				URL:           item.Link,
				Title:         item.Title,
				Summary:       summary,
				Author:        author,
				Image:         image,
				Content:       bodyHTML,
				ContentFormat: bodyFormat,
				Published:     published,
				Tags:          tags,
			})
			count++
			if count >= limit {
				break
			}
		}
	}
	if len(out) == 0 && len(errs) > 0 {
		return nil, fmt.Errorf("reddit: %s", strings.Join(errs, "; "))
	}
	return out, nil
}

func redditURL(base, sub, sortBy, timeRange string, limit int) string {
	sub = strings.Trim(strings.TrimSpace(sub), "/")
	sub = strings.TrimPrefix(sub, "r/")
	sub = strings.TrimPrefix(sub, "@")
	path := "/r/" + url.PathEscape(sub)
	switch sortBy {
	case "new", "rising":
		path += "/" + sortBy
	case "top":
		path += "/top"
	}
	link := base + path + "/.rss?limit=" + fmt.Sprint(limit)
	if sortBy == "top" {
		link += "&t=" + url.QueryEscape(timeRange)
	}
	return link
}

func firstImage(markup string) string {
	idx := strings.Index(strings.ToLower(markup), "<img")
	if idx < 0 {
		return ""
	}
	rest := markup[idx:]
	start := strings.Index(rest, "src=\"")
	if start < 0 {
		start = strings.Index(rest, "src='")
	}
	if start < 0 {
		return ""
	}
	start += 5
	quote := rest[start-1]
	end := strings.IndexByte(rest[start:], quote)
	if end < 0 {
		return ""
	}
	return html.UnescapeString(strings.TrimSpace(rest[start : start+end]))
}
