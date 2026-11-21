// Package reddit reads subreddits through the official OAuth API.
//
// Reddit removed anonymous JSON access for most clients, so this source needs
// a free "script" app: https://www.reddit.com/prefs/apps. Put the id and
// secret in REDDIT_CLIENT_ID / REDDIT_CLIENT_SECRET (or in the source
// options) and the source will mint its own token.
package reddit

import (
	"context"
	"encoding/json"
	"fmt"
	"html"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"

	"newsfeed/internal/content"
	"newsfeed/internal/model"
	"newsfeed/internal/source"
	"newsfeed/internal/source/httpx"
)

func init() {
	source.Register("reddit", func() source.Fetcher { return &fetcher{} })
}

type fetcher struct {
	mu      sync.Mutex
	token   string
	expires time.Time
}

// Options:
//
//	subreddits = ["golang", "nixos"]
//	sort       = "hot"        # hot | new | top | rising
//	time       = "day"        # for sort = "top": hour | day | week | month | year | all
//	limit      = 25           # per subreddit, 1-100
//	include_nsfw     = false
//	include_stickied = false
//	user_agent = "linux:newsfeed:0.1 (by /u/yourname)"
func (f *fetcher) Fetch(ctx context.Context, opts source.Options) ([]model.Item, error) {
	clientID := first(opts.String("client_id", ""), os.Getenv("REDDIT_CLIENT_ID"))
	clientSecret := first(opts.String("client_secret", ""), os.Getenv("REDDIT_CLIENT_SECRET"))
	if clientID == "" || clientSecret == "" {
		return nil, fmt.Errorf("reddit: REDDIT_CLIENT_ID and REDDIT_CLIENT_SECRET are required (create a free script app at reddit.com/prefs/apps)")
	}
	subs := opts.Strings("subreddits")
	if len(subs) == 0 {
		return nil, fmt.Errorf("reddit: options.subreddits is required")
	}
	sortBy := opts.String("sort", "hot")
	switch sortBy {
	case "hot", "new", "top", "rising":
	default:
		sortBy = "hot"
	}
	limit := opts.Int("limit", 25)
	if limit < 1 || limit > 100 {
		limit = 25
	}
	ua := opts.String("user_agent", "linux:newsfeed:0.1 (by /u/newsfeed)")
	base := strings.TrimRight(opts.String("base_url", "https://oauth.reddit.com"), "/")
	tokenURL := opts.String("token_url", "https://www.reddit.com/api/v1/access_token")
	client := httpx.Client(20*time.Second, ua, opts.String("proxy", ""))

	token, err := f.accessToken(ctx, client, tokenURL, clientID, clientSecret)
	if err != nil {
		return nil, err
	}

	var (
		out  []model.Item
		errs []string
	)
	for _, sub := range subs {
		endpoint := fmt.Sprintf("%s/r/%s/%s?limit=%d&raw_json=1", base, url.PathEscape(sub), sortBy, limit)
		if sortBy == "top" {
			endpoint += "&t=" + opts.String("time", "day")
		}
		body, err := f.get(ctx, client, endpoint, token, ua)
		if err != nil {
			errs = append(errs, err.Error())
			continue
		}
		var listing listingResponse
		if err := json.Unmarshal(body, &listing); err != nil {
			errs = append(errs, fmt.Sprintf("reddit/%s: decode: %v", sub, err))
			continue
		}
		for _, child := range listing.Data.Children {
			post := child.Data
			if post.ID == "" || post.Title == "" || post.Stickied || (post.Over18 && !opts.Bool("include_nsfw", false)) {
				continue
			}
			link := strings.TrimSpace(post.URL)
			if post.IsSelf || link == "" {
				link = "https://www.reddit.com" + post.Permalink
			}
			if link == "" {
				continue
			}
			image := ""
			if len(post.Preview.Images) > 0 {
				image = htmlUnescape(post.Preview.Images[0].Source.URL)
			}
			if image == "" && strings.HasPrefix(post.Thumbnail, "http") {
				image = htmlUnescape(post.Thumbnail)
			}
			summary := httpx.Truncate(httpx.Text(post.SelfText), 320)
			bodyHTML := content.SanitizeHTML(post.SelfTextHTML, link)
			bodyFormat := ""
			if bodyHTML != "" {
				bodyFormat = "html"
			} else if summary == "" && strings.TrimSpace(post.SelfText) != "" {
				bodyHTML = strings.TrimSpace(post.SelfText)
				bodyFormat = "text"
			}
			tags := []string{"r/" + post.Subreddit}
			if post.Score != 0 {
				tags = append(tags, fmt.Sprintf("%d points", post.Score))
			}
			if post.NumComments != 0 {
				tags = append(tags, fmt.Sprintf("%d comments", post.NumComments))
			}
			if post.LinkFlairText != "" {
				tags = append(tags, post.LinkFlairText)
			}
			out = append(out, model.Item{
				ExternalID:    post.Name,
				URL:           link,
				Title:         post.Title,
				Summary:       summary,
				Author:        "u/" + post.Author,
				Image:         image,
				Content:       bodyHTML,
				ContentFormat: bodyFormat,
				Published:     time.Unix(int64(post.CreatedUTC), 0),
				Tags:          tags,
			})
		}
	}
	if len(out) == 0 && len(errs) > 0 {
		return nil, fmt.Errorf("reddit: %s", strings.Join(errs, "; "))
	}
	return out, nil
}

func (f *fetcher) accessToken(ctx context.Context, client *http.Client, endpoint, id, secret string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.token != "" && time.Now().Before(f.expires) {
		return f.token, nil
	}
	form := url.Values{"grant_type": {"client_credentials"}}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(form.Encode()))
	if err != nil {
		return "", err
	}
	req.SetBasicAuth(id, secret)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("reddit: token: %w", err)
	}
	defer resp.Body.Close()
	var body struct {
		AccessToken string `json:"access_token"`
		ExpiresIn   int    `json:"expires_in"`
		Error       string `json:"error"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return "", fmt.Errorf("reddit: token: http %d: %w", resp.StatusCode, err)
	}
	if resp.StatusCode != http.StatusOK || body.AccessToken == "" {
		msg := body.Error
		if msg == "" {
			msg = resp.Status
		}
		return "", fmt.Errorf("reddit: token: %s", msg)
	}
	ttl := body.ExpiresIn
	if ttl <= 0 {
		ttl = 3600
	}
	f.token = body.AccessToken
	f.expires = time.Now().Add(time.Duration(ttl)*time.Second - time.Minute)
	return f.token, nil
}

func (f *fetcher) get(ctx context.Context, client *http.Client, endpoint, token, ua string) ([]byte, error) {
	body, _, err := httpx.Get(ctx, client, endpoint, http.Header{
		"Authorization": {"Bearer " + token},
		"User-Agent":    {ua},
	})
	if err == nil {
		return body, nil
	}
	if strings.Contains(err.Error(), "http 401") {
		f.mu.Lock()
		f.token = ""
		f.expires = time.Time{}
		f.mu.Unlock()
	}
	return nil, err
}

func first(values ...string) string {
	for _, v := range values {
		if v = strings.TrimSpace(v); v != "" {
			return v
		}
	}
	return ""
}

func htmlUnescape(s string) string {
	return html.UnescapeString(s)
}

type listingResponse struct {
	Data struct {
		Children []struct {
			Kind string `json:"kind"`
			Data post   `json:"data"`
		} `json:"children"`
	} `json:"data"`
}

type post struct {
	ID            string  `json:"id"`
	Name          string  `json:"name"`
	Title         string  `json:"title"`
	URL           string  `json:"url"`
	Permalink     string  `json:"permalink"`
	SelfText      string  `json:"selftext"`
	SelfTextHTML  string  `json:"selftext_html"`
	Author        string  `json:"author"`
	Subreddit     string  `json:"subreddit"`
	CreatedUTC    float64 `json:"created_utc"`
	Score         int     `json:"score"`
	NumComments   int     `json:"num_comments"`
	Over18        bool    `json:"over_18"`
	Stickied      bool    `json:"stickied"`
	IsSelf        bool    `json:"is_self"`
	Thumbnail     string  `json:"thumbnail"`
	LinkFlairText string  `json:"link_flair_text"`
	Preview       struct {
		Images []struct {
			Source struct {
				URL string `json:"url"`
			} `json:"source"`
		} `json:"images"`
	} `json:"preview"`
}
