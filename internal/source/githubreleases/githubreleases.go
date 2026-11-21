// Package githubreleases watches releases of public GitHub repositories.
package githubreleases

import (
	"context"
	"fmt"
	"regexp"
	"strings"
	"time"

	"newsfeed/internal/model"
	"newsfeed/internal/source"
	"newsfeed/internal/source/httpx"
)

func init() {
	source.Register("github", func() source.Fetcher { return &fetcher{} })
}

// Options:
//
//	repos    = ["ollama/ollama", "ggml-org/llama.cpp"]
//	limit    = 3       # releases per repository
//	prerelease = false
func (f *fetcher) Fetch(ctx context.Context, opts source.Options) ([]model.Item, error) {
	repos := opts.Strings("repos")
	if len(repos) == 0 {
		return nil, fmt.Errorf("github: options.repos is required")
	}
	limit := opts.Int("limit", 3)
	includePre := opts.Bool("prerelease", false)

	client := httpx.Client(20*time.Second, "newsfeed/0.1 (+github)", opts.String("proxy", ""))
	var (
		out  []model.Item
		errs []string
	)
	for _, repo := range repos {
		endpoint := fmt.Sprintf("https://api.github.com/repos/%s/releases?per_page=%d", repo, limit)
		var releases []release
		if _, err := httpx.GetJSON(ctx, client, endpoint, &releases); err != nil {
			errs = append(errs, err.Error())
			continue
		}
		for _, r := range releases {
			if r.Draft || (r.Prerelease && !includePre) {
				continue
			}
			title := strings.TrimSpace(r.Name)
			tag := strings.TrimSpace(r.TagName)
			switch {
			case title == "":
				title = repo + " " + tag
			case tag != "" && !strings.Contains(title, tag):
				title = repo + " " + tag + " — " + title
			default:
				title = repo + " " + title
			}
			tags := []string{repo}
			if r.Prerelease {
				tags = append(tags, "prerelease")
			}
			published := r.PublishedAt
			if published.IsZero() {
				published = r.CreatedAt
			}
			body := stripMarkdown(r.Body)
			bodyFormat := ""
			if body != "" {
				bodyFormat = "text"
			}
			out = append(out, model.Item{
				ExternalID:    fmt.Sprint(r.ID),
				URL:           r.HTMLURL,
				Title:         httpx.Truncate(title, 200),
				Summary:       httpx.Truncate(body, 360),
				Author:        r.Author.Login,
				Content:       body,
				ContentFormat: bodyFormat,
				Published:     published,
				Tags:          tags,
			})
		}
	}
	if len(out) == 0 && len(errs) > 0 {
		return nil, fmt.Errorf("github: %s", strings.Join(errs, "; "))
	}
	return out, nil
}

type release struct {
	ID          int64     `json:"id"`
	HTMLURL     string    `json:"html_url"`
	TagName     string    `json:"tag_name"`
	Name        string    `json:"name"`
	Body        string    `json:"body"`
	Draft       bool      `json:"draft"`
	Prerelease  bool      `json:"prerelease"`
	CreatedAt   time.Time `json:"created_at"`
	PublishedAt time.Time `json:"published_at"`
	Author      struct {
		Login string `json:"login"`
	} `json:"author"`
}

var (
	codeBlock  = regexp.MustCompile("(?s)```.*?```")
	inlineCode = regexp.MustCompile("`([^`]*)`")
	mdImage    = regexp.MustCompile(`!\[[^\]]*\]\([^)]*\)`)
	mdLink     = regexp.MustCompile(`\[([^\]]*)\]\([^)]*\)`)
	mdHeading  = regexp.MustCompile(`(?m)^#{1,6}\s*`)
	mdQuote    = regexp.MustCompile(`(?m)^>\s?`)
	mdRule     = regexp.MustCompile(`(?m)^[-*_]{3,}\s*$`)
)

func stripMarkdown(s string) string {
	s = codeBlock.ReplaceAllString(s, "")
	s = mdImage.ReplaceAllString(s, "")
	s = mdLink.ReplaceAllString(s, "$1")
	s = inlineCode.ReplaceAllString(s, "$1")
	s = mdHeading.ReplaceAllString(s, "")
	s = mdQuote.ReplaceAllString(s, "")
	s = mdRule.ReplaceAllString(s, "")
	s = strings.ReplaceAll(s, "**", "")
	s = strings.ReplaceAll(s, "__", "")
	return httpx.CollapseSpace(s)
}

type fetcher struct{}
