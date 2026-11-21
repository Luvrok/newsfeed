package content

import (
	"strings"
	"testing"
)

func TestSanitizeHTMLRemovesDangerousMarkup(t *testing.T) {
	in := `<p onclick="alert(1)">Hello <script>alert(2)</script>` +
		`<a href="javascript:alert(3)">bad</a> <a href="/story">read</a>` +
		`<img src="/photo.jpg" onerror="alert(4)"><iframe src="https://evil"></iframe></p>`
	out := SanitizeHTML(in, "https://example.com/news/1")

	for _, bad := range []string{"<script", "onclick", "onerror", "javascript:", "<iframe"} {
		if strings.Contains(out, bad) {
			t.Errorf("output still contains %q: %s", bad, out)
		}
	}
	if !strings.Contains(out, `href="https://example.com/story"`) {
		t.Errorf("relative link was not resolved: %s", out)
	}
	if !strings.Contains(out, `target="_blank"`) {
		t.Errorf("link does not open in a new tab: %s", out)
	}
	if !strings.Contains(out, `src="/api/v1/image?u=`) {
		t.Errorf("image was not proxied: %s", out)
	}
	if !strings.Contains(out, "read") || !strings.Contains(out, "Hello") {
		t.Errorf("text content was lost: %s", out)
	}
}

func TestSanitizeHTMLUnwrapsUnknownTags(t *testing.T) {
	out := SanitizeHTML(`<custom-tag><strong>kept</strong> text</custom-tag>`, "")
	if strings.Contains(out, "custom-tag") {
		t.Fatalf("unknown tag was kept: %s", out)
	}
	if !strings.Contains(out, "<strong>kept</strong> text") {
		t.Fatalf("content was lost: %s", out)
	}
}

func TestImageURL(t *testing.T) {
	got := ImageURL("https://cdn.example.com/a b.jpg?x=1&y=2", 0)
	if !strings.HasPrefix(got, "/api/v1/image?u=") {
		t.Fatalf("unexpected proxy url: %s", got)
	}
	for _, bad := range []string{"", "javascript:alert(1)", "data:image/png;base64,xx", "/relative.jpg"} {
		if got := ImageURL(bad, 0); got != "" {
			t.Errorf("ImageURL(%q) = %q, want empty", bad, got)
		}
	}
}
