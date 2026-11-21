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
	"sync"
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
	ua := opts.String("user_agent", "linux:newsfeed:0.2 (by /u/Luvrok)")
	client := httpx.Client(25*time.Second, ua, opts.String("proxy", ""))
	parser := gofeed.NewParser()

	var (
		out  []model.Item
		errs []string
	)
	for _, sub := range subs {
		if err := paceReddit(ctx); err != nil {
			return out, err
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

// Reddit answers a burst with 429. All reddit sources share this gate, so
// eleven subreddits from the seed are fetched over about a minute instead of
// one burst.
var redditGate struct {
	mu   sync.Mutex
	next time.Time
}

func paceReddit(ctx context.Context) error {
	redditGate.mu.Lock()
	defer redditGate.mu.Unlock()
	wait := time.Until(redditGate.next)
	if wait > 0 {
		timer := time.NewTimer(wait)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
	}
	redditGate.next = time.Now().Add(60 * time.Second)
	return nil
}
