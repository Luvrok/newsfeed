package store

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"newsfeed/internal/model"
)

func testStore(t *testing.T) *Store {
	t.Helper()
	st, err := Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	ctx := context.Background()
	if err := st.SeedSources(ctx, []model.Source{
		{ID: "a", Kind: "rss", Label: "A", Group: "test", Interval: time.Minute},
		{ID: "b", Kind: "rss", Label: "B", Group: "test", Interval: time.Minute},
	}); err != nil {
		t.Fatalf("sync sources: %v", err)
	}
	return st
}

func TestCanonicalURL(t *testing.T) {
	tests := []struct {
		in, want string
		ok       bool
	}{
		{"https://Example.COM/story/?utm_source=x&utm_medium=y#top", "https://example.com/story", true},
		{"http://example.com:80/a/", "http://example.com/a", true},
		{"https://example.com/", "https://example.com", true},
		{"https://example.com:443/a?b=2&a=1", "https://example.com/a?a=1&b=2", true},
		{"ftp://example.com/a", "", false},
		{"mailto:someone@example.com", "", false},
		{"/relative/path", "", false},
	}
	for _, tc := range tests {
		got, ok := canonicalURL(tc.in)
		if ok != tc.ok || got != tc.want {
			t.Errorf("canonicalURL(%q) = %q, %v; want %q, %v", tc.in, got, ok, tc.want, tc.ok)
		}
	}
}

func TestInsertItemsDeduplicates(t *testing.T) {
	st := testStore(t)
	ctx := context.Background()
	now := time.Now().Truncate(time.Second)

	items := []model.Item{
		{URL: "https://example.com/story?utm_source=x", Title: "Alpha story", Published: now},
		{URL: "https://example.com/story#comments", Title: "Alpha story again", Published: now},
		{URL: "https://example.com/other", Title: "Beta story", Published: now},
	}
	got, err := st.InsertItems(ctx, "a", items)
	if err != nil {
		t.Fatalf("insert: %v", err)
	}
	if got != 2 {
		t.Fatalf("first insert stored %d items, want 2", got)
	}

	got, err = st.InsertItems(ctx, "a", items)
	if err != nil {
		t.Fatalf("second insert: %v", err)
	}
	if got != 0 {
		t.Fatalf("second insert stored %d items, want 0", got)
	}

	// The same normalized headline from another source is a duplicate too.
	got, err = st.InsertItems(ctx, "b", []model.Item{
		{URL: "https://b.example/beta", Title: "Beta,  story!", Published: now},
	})
	if err != nil {
		t.Fatalf("cross-source insert: %v", err)
	}
	if got != 0 {
		t.Fatalf("cross-source duplicate stored %d items, want 0", got)
	}
}

func TestFeedPagination(t *testing.T) {
	st := testStore(t)
	ctx := context.Background()
	base := time.Now().Add(-5 * time.Hour).Truncate(time.Second)
	items := make([]model.Item, 0, 5)
	for i := 0; i < 5; i++ {
		items = append(items, model.Item{
			URL:       "https://example.com/" + string(rune('a'+i)),
			Title:     "Story " + string(rune('a'+i)),
			Published: base.Add(time.Duration(i) * time.Minute),
		})
	}
	if _, err := st.InsertItems(ctx, "a", items); err != nil {
		t.Fatalf("insert: %v", err)
	}

	first, hasMore, err := st.Feed(ctx, model.FeedQuery{Limit: 2})
	if err != nil {
		t.Fatalf("feed: %v", err)
	}
	if !hasMore || len(first) != 2 {
		t.Fatalf("first page: %d items, hasMore=%v", len(first), hasMore)
	}
	if first[0].Title != "Story e" || first[1].Title != "Story d" {
		t.Fatalf("unexpected first page: %q, %q", first[0].Title, first[1].Title)
	}

	last := first[len(first)-1]
	second, hasMore, err := st.Feed(ctx, model.FeedQuery{
		Limit:  2,
		Before: &model.Cursor{PublishedAt: last.PublishedAt.Unix(), ID: last.ID},
	})
	if err != nil {
		t.Fatalf("second page: %v", err)
	}
	if !hasMore || len(second) != 2 || second[0].Title != "Story c" {
		t.Fatalf("unexpected second page: %+v hasMore=%v", second, hasMore)
	}

	last = second[len(second)-1]
	third, hasMore, err := st.Feed(ctx, model.FeedQuery{
		Limit:  2,
		Before: &model.Cursor{PublishedAt: last.PublishedAt.Unix(), ID: last.ID},
	})
	if err != nil {
		t.Fatalf("third page: %v", err)
	}
	if hasMore || len(third) != 1 || third[0].Title != "Story a" {
		t.Fatalf("unexpected third page: %+v hasMore=%v", third, hasMore)
	}
}

func TestByIDReturnsContent(t *testing.T) {
	st := testStore(t)
	ctx := context.Background()
	now := time.Now().Truncate(time.Second)
	items := []model.Item{{
		URL: "https://example.com/article", Title: "A story", Summary: "short",
		Content: "<p>the body</p>", ContentFormat: "html", Published: now,
	}}
	if _, err := st.InsertItems(ctx, "a", items); err != nil {
		t.Fatalf("insert: %v", err)
	}
	feed, _, err := st.Feed(ctx, model.FeedQuery{Limit: 1})
	if err != nil || len(feed) != 1 {
		t.Fatalf("feed: %v %d", err, len(feed))
	}
	item, err := st.ByID(ctx, feed[0].ID)
	if err != nil {
		t.Fatalf("by id: %v", err)
	}
	if item.Content != "<p>the body</p>" || item.ContentFormat != "html" {
		t.Fatalf("content lost: %+v", item)
	}
}
