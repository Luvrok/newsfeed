package reddit

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"newsfeed/internal/source"
)

func TestFetchPublicRSS(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/r/golang/.rss", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("limit") != "5" {
			t.Errorf("limit query = %q, want 5", r.URL.Query().Get("limit"))
		}
		w.Header().Set("Content-Type", "application/rss+xml")
		fmt.Fprint(w, `<?xml version="1.0"?>
<rss version="2.0"><channel><title>r/golang</title>
<item>
  <title>Go 1.27 is out</title>
  <link>https://www.reddit.com/r/golang/comments/abc/go_127_is_out/</link>
  <guid>t3_abc</guid>
  <author>/u/gopher</author>
  <pubDate>Sat, 21 Nov 2026 20:00:00 GMT</pubDate>
  <description><![CDATA[<p>Release notes and a <a href="https://go.dev/blog/go1.27">link</a>.</p><img src="https://preview.redd.it/x.jpg?width=640&amp;s=abc">]]></description>
</item>
</channel></rss>`)
	})
	server := httptest.NewServer(mux)
	defer server.Close()

	f := &fetcher{}
	items, err := f.Fetch(context.Background(), source.Options{
		"subreddits": []any{"golang"},
		"sort":       "hot",
		"limit":      5,
		"base_url":   server.URL,
	})
	if err != nil {
		t.Fatalf("fetch: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("got %d items, want 1", len(items))
	}
	it := items[0]
	if it.Title != "Go 1.27 is out" {
		t.Fatalf("title: %q", it.Title)
	}
	if it.Author != "u/gopher" {
		t.Fatalf("author: %q", it.Author)
	}
	if !strings.Contains(strings.Join(it.Tags, ","), "r/golang") {
		t.Fatalf("tags: %v", it.Tags)
	}
	if !strings.Contains(it.Content, "preview.redd.it") {
		t.Fatalf("content image lost: %q", it.Content)
	}
	if it.Image != "https://preview.redd.it/x.jpg?width=640&s=abc" {
		t.Fatalf("image: %q", it.Image)
	}
}

func TestFetchBuildsTopURL(t *testing.T) {
	got := redditURL("https://www.reddit.com", "worldnews", "top", "week", 10)
	want := "https://www.reddit.com/r/worldnews/top/.rss?t=week&limit=10"
	if got != want {
		t.Fatalf("redditURL = %q, want %q", got, want)
	}
}
