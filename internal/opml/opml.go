// Package opml parses the subscription list exported by feed readers such as
// Miniflux. Only the feeds with an xmlUrl are returned; nested categories are
// flattened into groups.
package opml

import (
	"encoding/xml"
	"fmt"
	"io"
	"strings"
)

// Feed is one flattened outline.
type Feed struct {
	Title    string
	XMLURL   string
	HTMLURL  string
	Category string
}

type document struct {
	Body struct {
		Outlines []outline `xml:"outline"`
	} `xml:"body"`
}

type outline struct {
	Text     string    `xml:"text,attr"`
	Title    string    `xml:"title,attr"`
	Type     string    `xml:"type,attr"`
	XMLURL   string    `xml:"xmlUrl,attr"`
	HTMLURL  string    `xml:"htmlUrl,attr"`
	Category string    `xml:"category,attr"`
	Children []outline `xml:"outline"`
}

// Parse reads an OPML document. It caps the result at 500 feeds.
func Parse(r io.Reader) ([]Feed, error) {
	var doc document
	decoder := xml.NewDecoder(io.LimitReader(r, 5<<20))
	if err := decoder.Decode(&doc); err != nil {
		return nil, fmt.Errorf("parse opml: %w", err)
	}
	var out []Feed
	var walk func(items []outline, category string)
	walk = func(items []outline, category string) {
		for _, item := range items {
			nextCategory := category
			if item.Category != "" {
				nextCategory = strings.TrimSpace(item.Category)
			}
			if item.XMLURL != "" {
				title := strings.TrimSpace(item.Title)
				if title == "" {
					title = strings.TrimSpace(item.Text)
				}
				out = append(out, Feed{
					Title:    title,
					XMLURL:   strings.TrimSpace(item.XMLURL),
					HTMLURL:  strings.TrimSpace(item.HTMLURL),
					Category: nextCategory,
				})
				if len(out) >= 500 {
					return
				}
			}
			if len(item.Children) > 0 {
				childCategory := nextCategory
				if childCategory == "" {
					if title := strings.TrimSpace(item.Title); title != "" {
						childCategory = title
					} else if text := strings.TrimSpace(item.Text); text != "" {
						childCategory = text
					}
				}
				walk(item.Children, childCategory)
			}
		}
	}
	walk(doc.Body.Outlines, "")
	return out, nil
}
