// Package telegram reads the public web preview of Telegram channels
// (t.me/s/<channel>). No bot token and no account are needed.
package telegram

import (
	"context"
	"fmt"
	"net/http"
	"regexp"
	"strings"
	"time"
	"unicode"

	"golang.org/x/net/html"

	"newsfeed/internal/model"
	"newsfeed/internal/source"
	"newsfeed/internal/source/httpx"
)

func init() {
	source.Register("telegram", func() source.Fetcher { return &fetcher{} })
}

// Options:
//
//	channels = ["tass_agency", "bbcrussian"]
//	limit    = 20
func (f *fetcher) Fetch(ctx context.Context, opts source.Options) ([]model.Item, error) {
	channels := opts.Strings("channels")
	if len(channels) == 0 {
		return nil, fmt.Errorf("telegram: options.channels is required")
	}
	limit := opts.Int("limit", 20)
	skip := opts.Strings("skip")
	client := httpx.Client(20*time.Second, "Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/131.0 Safari/537.36", opts.String("proxy", ""))

	var (
		out  []model.Item
		errs []string
	)
	for _, raw := range channels {
		channel := normalizeChannel(raw)
		if channel == "" {
			continue
		}
		items, err := fetchChannel(ctx, client, channel, limit, skip)
		if err != nil {
			errs = append(errs, err.Error())
			continue
		}
		out = append(out, items...)
	}
	if len(out) == 0 && len(errs) > 0 {
		return nil, fmt.Errorf("telegram: %s", strings.Join(errs, "; "))
	}
	return out, nil
}

func fetchChannel(ctx context.Context, client *http.Client, channel string, limit int, skip []string) ([]model.Item, error) {
	url := "https://t.me/s/" + channel
	body, _, err := httpx.Get(ctx, client, url, http.Header{"Accept": {"text/html"}})
	if err != nil {
		return nil, err
	}
	doc, err := html.Parse(strings.NewReader(string(body)))
	if err != nil {
		return nil, fmt.Errorf("telegram/%s: parse html: %w", channel, err)
	}

	channelTitle := ""
	if n := findFirst(doc, "tgme_channel_info_header_title"); n != nil {
		channelTitle = nodeText(n)
	}
	if channelTitle == "" {
		channelTitle = channel
	}

	var out []model.Item
	walk(doc, func(n *html.Node) {
		if n.Type != html.ElementNode || n.Data != "div" || !hasClass(n, "tgme_widget_message") {
			return
		}
		post := attr(n, "data-post")
		if post == "" {
			return
		}
		textNode := findFirst(n, "js-message_text")
		if textNode == nil {
			return
		}
		text := nodeText(textNode)
		if text == "" || containsAny(text, skip) {
			return
		}

		published := time.Now()
		if t := findFirst(n, "time"); t != nil {
			if v := attr(t, "datetime"); v != "" {
				if parsed, err := time.Parse(time.RFC3339, v); err == nil {
					published = parsed
				}
			}
		}

		image := ""
		if a := findByClassAndTag(n, "a", "tgme_widget_message_photo_wrap"); a != nil {
			image = bgURL(attr(a, "style"))
		}

		tags := []string{}
		if v := findFirst(n, "tgme_widget_message_views"); v != nil {
			if views := nodeText(v); views != "" {
				tags = append(tags, views+" views")
			}
		}

		cleaned := cleanText(text)
		if cleaned == "" {
			return
		}
		title, summary := split(cleaned)
		body := cleaned
		if _, rest, ok := strings.Cut(cleaned, "\n"); ok {
			body = strings.TrimSpace(rest)
		}
		bodyFormat := "text"
		if body == "" || body == title {
			body = ""
			bodyFormat = ""
		}
		out = append(out, model.Item{
			ExternalID:    post,
			URL:           "https://t.me/" + post,
			Title:         title,
			Summary:       summary,
			Author:        channelTitle,
			Image:         image,
			Content:       body,
			ContentFormat: bodyFormat,
			Published:     published,
			Tags:          tags,
		})
	})

	if len(out) > limit {
		out = out[len(out)-limit:]
	}
	return out, nil
}

var cta = regexp.MustCompile(`(?i)^(подпишись|подписывайтесь|подписаться|читайте|следите|subscribe|follow us|join us)`)

// cleanText drops the channel's call-to-action tail before the post is
// stored as the article body.
func cleanText(text string) string {
	lines := strings.Split(text, "\n")
	for i, line := range lines {
		line = strings.TrimLeftFunc(strings.TrimSpace(line), func(r rune) bool {
			return !unicode.IsLetter(r) && !unicode.IsDigit(r)
		})
		if cta.MatchString(line) {
			lines = lines[:i]
			break
		}
	}
	for len(lines) > 0 && strings.TrimSpace(lines[len(lines)-1]) == "" {
		lines = lines[:len(lines)-1]
	}
	return strings.TrimSpace(strings.Join(lines, "\n"))
}

// split makes the first line a headline and keeps the rest as a summary.
func split(text string) (string, string) {
	lines := strings.Split(text, "\n")
	if len(lines) == 0 {
		return "", ""
	}
	title := httpx.Truncate(cleanTitle(lines[0]), 140)
	rest := strings.TrimSpace(strings.Join(lines[1:], "\n"))
	return title, httpx.Truncate(rest, 420)
}

// cleanTitle removes leading channel emoji and bullets such as "❗️" or "⚡"
// while keeping an opening quote, which editors use on purpose.
func cleanTitle(line string) string {
	line = strings.TrimSpace(line)
	cleaned := strings.TrimLeftFunc(line, func(r rune) bool {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			return false
		}
		switch r {
		case '"', '\'', '«', '»', '“', '„', '(', '[', '—', '-':
			return false
		}
		return true
	})
	cleaned = strings.TrimSpace(cleaned)
	if cleaned == "" {
		return line
	}
	return cleaned
}

func normalizeChannel(raw string) string {
	raw = strings.TrimSpace(raw)
	raw = strings.TrimPrefix(raw, "https://t.me/")
	raw = strings.TrimPrefix(raw, "http://t.me/")
	raw = strings.TrimPrefix(raw, "t.me/")
	raw = strings.TrimPrefix(raw, "/s/")
	raw = strings.TrimPrefix(raw, "s/")
	raw = strings.TrimPrefix(raw, "@")
	raw = strings.Trim(raw, "/")
	return raw
}

func bgURL(style string) string {
	m := bgImage.FindStringSubmatch(style)
	if len(m) < 2 {
		return ""
	}
	u := strings.TrimSpace(m[1])
	if strings.HasPrefix(u, "//") {
		u = "https:" + u
	}
	return u
}

var bgImage = regexp.MustCompile(`(?i)background-image\s*:\s*url\(['"]?([^'")]+)`)

func walk(n *html.Node, fn func(*html.Node)) {
	fn(n)
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		walk(c, fn)
	}
}

func hasClass(n *html.Node, class string) bool {
	for _, f := range strings.Fields(attr(n, "class")) {
		if f == class {
			return true
		}
	}
	return false
}

func attr(n *html.Node, key string) string {
	for _, a := range n.Attr {
		if a.Key == key {
			return a.Val
		}
	}
	return ""
}

func findFirst(n *html.Node, class string) *html.Node {
	var found *html.Node
	walk(n, func(c *html.Node) {
		if found == nil && c.Type == html.ElementNode && hasClass(c, class) {
			found = c
		}
	})
	return found
}

func findByClassAndTag(n *html.Node, tag, class string) *html.Node {
	var found *html.Node
	walk(n, func(c *html.Node) {
		if found == nil && c.Type == html.ElementNode && c.Data == tag && hasClass(c, class) {
			found = c
		}
	})
	return found
}

func nodeText(n *html.Node) string {
	var b strings.Builder
	var rec func(*html.Node)
	rec = func(c *html.Node) {
		if c.Type == html.TextNode {
			b.WriteString(c.Data)
		}
		if c.Type == html.ElementNode {
			switch c.Data {
			case "br":
				b.WriteByte('\n')
			case "p", "div", "blockquote", "li":
				b.WriteByte('\n')
			}
		}
		for child := c.FirstChild; child != nil; child = child.NextSibling {
			rec(child)
		}
		if c.Type == html.ElementNode {
			switch c.Data {
			case "p", "div", "blockquote", "li":
				b.WriteByte('\n')
			}
		}
	}
	rec(n)
	return httpx.CollapseSpace(b.String())
}

type fetcher struct{}

func containsAny(text string, needles []string) bool {
	text = strings.ToLower(text)
	for _, needle := range needles {
		if needle = strings.ToLower(strings.TrimSpace(needle)); needle != "" && strings.Contains(text, needle) {
			return true
		}
	}
	return false
}
