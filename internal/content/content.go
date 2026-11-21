// Package content turns feed markup into safe article HTML and builds
// stable image proxy URLs. The service never scrapes the publisher's page:
// the detail view only shows what the source itself delivered in its feed
// or API response.
package content

import (
	"fmt"
	"net/url"
	"strings"

	"golang.org/x/net/html"

	"newsfeed/internal/source/httpx"
)

// ImageURL wraps an external image in the local proxy. The browser in a
// restricted network talks only to this service, and the server fetches the
// picture through its own route.
func ImageURL(raw string, width int) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return ""
	}
	link := "/api/v1/image?u=" + url.QueryEscape(raw)
	if width > 0 {
		link += fmt.Sprintf("&w=%d", width)
	}
	return link
}

var allowedTags = map[string]bool{
	"p": true, "div": true, "br": true, "hr": true,
	"strong": true, "b": true, "em": true, "i": true, "u": true, "s": true, "del": true, "ins": true,
	"blockquote": true, "pre": true, "code": true,
	"ul": true, "ol": true, "li": true,
	"h1": true, "h2": true, "h3": true, "h4": true, "h5": true, "h6": true,
	"a": true, "img": true,
	"figure": true, "figcaption": true,
	"table": true, "thead": true, "tbody": true, "tfoot": true, "tr": true, "td": true, "th": true, "caption": true,
}

var droppedTags = map[string]bool{
	"script": true, "style": true, "iframe": true, "frame": true, "frameset": true,
	"object": true, "embed": true, "applet": true, "form": true, "input": true,
	"button": true, "select": true, "textarea": true, "option": true,
	"svg": true, "math": true, "canvas": true, "map": true, "area": true,
	"link": true, "meta": true, "base": true, "noscript": true,
	"video": true, "audio": true, "source": true, "track": true,
}

// SanitizeHTML keeps a conservative subset of article markup. Relative URLs
// are resolved against baseURL, images are rewritten through the proxy and
// every link opens the original in a new tab.
func SanitizeHTML(fragment, baseURL string) string {
	fragment = strings.TrimSpace(fragment)
	if fragment == "" {
		return ""
	}
	if len(fragment) > 250_000 {
		fragment = fragment[:250_000]
	}
	var base *url.URL
	if baseURL != "" {
		base, _ = url.Parse(baseURL)
	}

	doc, err := html.Parse(strings.NewReader("<div>" + fragment + "</div>"))
	if err != nil {
		return ""
	}
	wrapper := findWrapper(doc)
	if wrapper == nil {
		return ""
	}
	sanitizeChildren(wrapper, base)

	var b strings.Builder
	for child := wrapper.FirstChild; child != nil; child = child.NextSibling {
		if err := html.Render(&b, child); err != nil {
			return ""
		}
	}
	out := strings.TrimSpace(b.String())
	if len(out) > 250_000 {
		out = out[:250_000]
	}
	return out
}

func findWrapper(doc *html.Node) *html.Node {
	for child := doc.FirstChild; child != nil; child = child.NextSibling {
		if child.Type == html.ElementNode && child.Data == "html" {
			for body := child.FirstChild; body != nil; body = body.NextSibling {
				if body.Type == html.ElementNode && body.Data == "body" {
					for div := body.FirstChild; div != nil; div = div.NextSibling {
						if div.Type == html.ElementNode && div.Data == "div" {
							return div
						}
					}
				}
			}
		}
	}
	return nil
}

func sanitizeChildren(parent *html.Node, base *url.URL) {
	for child := parent.FirstChild; child != nil; {
		next := child.NextSibling
		switch child.Type {
		case html.CommentNode, html.DoctypeNode:
			parent.RemoveChild(child)
		case html.ElementNode:
			tag := strings.ToLower(child.Data)
			switch {
			case droppedTags[tag]:
				parent.RemoveChild(child)
			case !allowedTags[tag]:
				sanitizeChildren(child, base)
				unwrap(parent, child)
			case tag == "a":
				if !cleanLink(child, base) {
					sanitizeChildren(child, base)
					unwrap(parent, child)
				} else {
					sanitizeChildren(child, base)
				}
			case tag == "img":
				if !cleanImage(child, base) {
					parent.RemoveChild(child)
				}
			default:
				cleanAttrs(child)
				sanitizeChildren(child, base)
			}
		}
		child = next
	}
}

func cleanLink(n *html.Node, base *url.URL) bool {
	var href, title string
	for _, attr := range n.Attr {
		switch strings.ToLower(attr.Key) {
		case "href":
			href = attr.Val
		case "title":
			title = attr.Val
		}
	}
	resolved := resolveURL(base, href)
	if resolved == "" {
		return false
	}
	attrs := []html.Attribute{
		{Key: "href", Val: resolved},
		{Key: "target", Val: "_blank"},
		{Key: "rel", Val: "noopener noreferrer nofollow"},
	}
	if title != "" {
		attrs = append(attrs, html.Attribute{Key: "title", Val: title})
	}
	n.Attr = attrs
	return true
}

func cleanImage(n *html.Node, base *url.URL) bool {
	var src, alt, title string
	for _, attr := range n.Attr {
		switch strings.ToLower(attr.Key) {
		case "src", "data-src":
			if src == "" || attr.Key == "src" {
				src = attr.Val
			}
		case "alt":
			alt = attr.Val
		case "title":
			title = attr.Val
		}
	}
	resolved := resolveURL(base, src)
	if resolved == "" {
		return false
	}
	attrs := []html.Attribute{
		{Key: "src", Val: ImageURL(resolved, 1280)},
		{Key: "alt", Val: alt},
		{Key: "loading", Val: "lazy"},
		{Key: "decoding", Val: "async"},
		{Key: "referrerpolicy", Val: "no-referrer"},
	}
	if title != "" {
		attrs = append(attrs, html.Attribute{Key: "title", Val: title})
	}
	n.Attr = attrs
	return true
}

func cleanAttrs(n *html.Node) {
	var keep []html.Attribute
	for _, attr := range n.Attr {
		switch strings.ToLower(attr.Key) {
		case "colspan", "rowspan":
			keep = append(keep, attr)
		}
	}
	n.Attr = keep
}

func resolveURL(base *url.URL, raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	u, err := url.Parse(raw)
	if err != nil {
		return ""
	}
	if base != nil {
		u = base.ResolveReference(u)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return ""
	}
	if u.Host == "" {
		return ""
	}
	return u.String()
}

func unwrap(parent, node *html.Node) {
	for child := node.FirstChild; child != nil; {
		next := child.NextSibling
		node.RemoveChild(child)
		parent.InsertBefore(child, node)
		child = next
	}
	parent.RemoveChild(node)
}

// PlainText is a convenience wrapper used by fetchers that only have markup.
func PlainText(markup string) string {
	return httpx.Text(markup)
}
