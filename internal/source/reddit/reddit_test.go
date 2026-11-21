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

func TestFetchMapsListing(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v1/access_token", func(w http.ResponseWriter, r *http.Request) {
		if user, pass, ok := r.BasicAuth(); !ok || user != "id" || pass != "secret" {
			http.Error(w, "bad auth", http.StatusUnauthorized)
			return
		}
		fmt.Fprint(w, `{"access_token":"token","expires_in":3600}`)
	})
	mux.HandleFunc("/r/golang/hot", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer token" {
			http.Error(w, "no token", http.StatusUnauthorized)
			return
		}
		fmt.Fprint(w, `{"data":{"children":[{"kind":"t3","data":{
			"id":"abc","name":"t3_abc","title":"Go 1.27 is out","url":"https://go.dev/blog/go1.27",
			"permalink":"/r/golang/comments/abc/","author":"gopher","subreddit":"golang",
			"created_utc":1795291200,"score":42,"num_comments":7,"is_self":false,
			"thumbnail":"https://b.thumbs.redditmedia.com/x.jpg",
			"preview":{"images":[{"source":{"url":"https://preview.reddit.com/x.jpg?width=1200&amp;s=abc"}}]}
		}}]}}`)
	})
	server := httptest.NewServer(mux)
	defer server.Close()

	f := &fetcher{}
	items, err := f.Fetch(context.Background(), source.Options{
		"subreddits":    []any{"golang"},
		"client_id":     "id",
		"client_secret": "secret",
		"base_url":      server.URL,
		"token_url":     server.URL + "/api/v1/access_token",
	})
	if err != nil {
		t.Fatalf("fetch: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("got %d items, want 1", len(items))
	}
	it := items[0]
	if it.Title != "Go 1.27 is out" || it.URL != "https://go.dev/blog/go1.27" {
		t.Fatalf("unexpected item: %+v", it)
	}
	if it.Image != "https://preview.reddit.com/x.jpg?width=1200&s=abc" {
		t.Fatalf("image not unescaped: %q", it.Image)
	}
	if !strings.Contains(strings.Join(it.Tags, ","), "r/golang") {
		t.Fatalf("subreddit tag missing: %v", it.Tags)
	}
}

func TestFetchRequiresCredentials(t *testing.T) {
	f := &fetcher{}
	_, err := f.Fetch(context.Background(), source.Options{"subreddits": []any{"golang"}})
	if err == nil || !strings.Contains(err.Error(), "REDDIT_CLIENT_ID") {
		t.Fatalf("expected a credentials error, got %v", err)
	}
}
