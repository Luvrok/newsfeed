package opml

import (
	"strings"
	"testing"
)

func TestParseNestedCategories(t *testing.T) {
	doc := `<?xml version="1.0"?>
<opml version="2.0"><body>
  <outline text="World">
    <outline type="rss" text="BBC" xmlUrl="https://example.com/bbc.xml"/>
  </outline>
  <outline text="Tech"><outline type="rss" text="Habr" xmlUrl="https://example.com/habr.xml"/></outline>
  <outline type="rss" text="Loose" xmlUrl="https://example.com/loose.xml"/>
</body></opml>`
	feeds, err := Parse(strings.NewReader(doc))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(feeds) != 3 {
		t.Fatalf("got %d feeds, want 3", len(feeds))
	}
	if feeds[0].Category != "World" || feeds[1].Category != "Tech" || feeds[2].Category != "" {
		t.Fatalf("categories: %+v", feeds)
	}
}
