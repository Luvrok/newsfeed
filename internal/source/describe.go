package source

import (
	"fmt"
	"net"
	"net/url"
	"strings"
)

// FieldType tells the web form how to render an option.
type FieldType string

const (
	FieldText   FieldType = "text"
	FieldNumber FieldType = "number"
	FieldBool   FieldType = "bool"
	FieldSelect FieldType = "select"
	FieldList   FieldType = "list"
	FieldSecret FieldType = "secret"
)

// Field describes one option of a source kind.
type Field struct {
	Key      string    `json:"key"`
	Label    string    `json:"label"`
	Help     string    `json:"help,omitempty"`
	Type     FieldType `json:"type"`
	Required bool      `json:"required,omitempty"`
	Default  any       `json:"default,omitempty"`
	Options  []string  `json:"options,omitempty"`
}

// Descriptor is the metadata the web UI needs to build an add/edit form.
type Descriptor struct {
	Kind        string  `json:"kind"`
	Title       string  `json:"title"`
	Description string  `json:"description"`
	Fields      []Field `json:"fields"`
}

var descriptors = []Descriptor{
	{
		Kind: "rss", Title: "RSS / Atom",
		Description: "Any feed, including RSSHub routes, Google News and Reddit .rss endpoints.",
		Fields: []Field{
			{Key: "url", Label: "Feed URL", Type: FieldText, Required: true, Help: "https://example.com/feed.xml or http://127.0.0.1:1200/..."},
			{Key: "limit", Label: "Items per poll", Type: FieldNumber, Default: 25},
			{Key: "summary", Label: "Store summaries", Type: FieldBool, Default: true, Help: "Disable for feeds whose description is a list of related links."},
			{Key: "proxy", Label: "Network", Type: FieldSelect, Options: []string{"", "direct"}, Help: "direct bypasses the global proxy for this feed."},
		},
	},
	{
		Kind: "telegram", Title: "Telegram channel",
		Description: "Public t.me/s preview. No bot token and no account.",
		Fields: []Field{
			{Key: "channels", Label: "Channels", Type: FieldList, Required: true, Help: "Comma-separated names, e.g. tass_agency, bbcrussian"},
			{Key: "limit", Label: "Posts per channel", Type: FieldNumber, Default: 20},
			{Key: "skip", Label: "Skip phrases", Type: FieldList, Help: "Comma-separated phrases that mark sponsored posts."},
			{Key: "proxy", Label: "Network", Type: FieldSelect, Options: []string{"", "direct"}},
		},
	},
	{
		Kind: "reddit", Title: "Reddit",
		Description: "Public Reddit RSS through your proxy. No OAuth app needed.",
		Fields: []Field{
			{Key: "subreddits", Label: "Subreddits", Type: FieldList, Required: true, Help: "Comma-separated, e.g. worldnews, geopolitics"},
			{Key: "sort", Label: "Sort", Type: FieldSelect, Options: []string{"hot", "new", "top", "rising"}, Default: "hot"},
			{Key: "time", Label: "Top period", Type: FieldSelect, Options: []string{"hour", "day", "week", "month", "year", "all"}, Default: "day"},
			{Key: "limit", Label: "Posts per subreddit", Type: FieldNumber, Default: 10},
			{Key: "proxy", Label: "Network", Type: FieldSelect, Options: []string{"", "direct"}},
		},
	},
	{
		Kind: "hackernews", Title: "Hacker News",
		Description: "Official Firebase API.",
		Fields: []Field{
			{Key: "list", Label: "List", Type: FieldSelect, Options: []string{"top", "new", "best", "ask", "show", "job"}, Default: "top"},
			{Key: "limit", Label: "Items", Type: FieldNumber, Default: 40},
		},
	},
	{
		Kind: "lobsters", Title: "Lobsters",
		Description: "Public JSON of lobste.rs.",
		Fields: []Field{
			{Key: "tag", Label: "Tag", Type: FieldText, Help: "Optional, e.g. go"},
			{Key: "limit", Label: "Items", Type: FieldNumber, Default: 30},
		},
	},
	{
		Kind: "mastodon", Title: "Mastodon",
		Description: "Public hashtag or local timeline of any instance.",
		Fields: []Field{
			{Key: "instance", Label: "Instance", Type: FieldText, Default: "mastodon.social", Required: true},
			{Key: "tag", Label: "Hashtag or local", Type: FieldText, Required: true, Help: "nixos or local"},
			{Key: "limit", Label: "Posts", Type: FieldNumber, Default: 30},
		},
	},
	{
		Kind: "usgs", Title: "USGS earthquakes",
		Description: "Official GeoJSON earthquake feeds.",
		Fields: []Field{
			{Key: "feed", Label: "Magnitude feed", Type: FieldSelect, Options: []string{"significant", "4.5", "2.5", "1.0", "all"}, Default: "2.5"},
			{Key: "period", Label: "Period", Type: FieldSelect, Options: []string{"hour", "day", "week"}, Default: "day"},
			{Key: "limit", Label: "Events", Type: FieldNumber, Default: 30},
		},
	},
	{
		Kind: "github", Title: "GitHub releases",
		Description: "Public releases API, unauthenticated.",
		Fields: []Field{
			{Key: "repos", Label: "Repositories", Type: FieldList, Required: true, Help: "Comma-separated owner/repo"},
			{Key: "limit", Label: "Releases per repository", Type: FieldNumber, Default: 3},
			{Key: "prerelease", Label: "Include prereleases", Type: FieldBool},
		},
	},
	{
		Kind: "gdelt", Title: "GDELT",
		Description: "Free article index; the API allows one request per five seconds.",
		Fields: []Field{
			{Key: "query", Label: "Query", Type: FieldText, Required: true},
			{Key: "timespan", Label: "Timespan", Type: FieldText, Default: "1d"},
			{Key: "max", Label: "Max articles", Type: FieldNumber, Default: 40},
			{Key: "language", Label: "Language", Type: FieldText, Help: "Optional, e.g. english"},
		},
	},
}

// Descriptors returns every source kind in a stable order.
func Descriptors() []Descriptor {
	out := make([]Descriptor, len(descriptors))
	copy(out, descriptors)
	return out
}

// DescriptorFor finds the form metadata for a kind.
func DescriptorFor(kind string) (Descriptor, bool) {
	for _, d := range descriptors {
		if d.Kind == kind {
			return d, true
		}
	}
	return Descriptor{}, false
}

// ValidateOptions checks the options that come from the web UI. Config seeds
// are trusted, but UI input is not.
func ValidateOptions(kind string, opts Options, allowPrivate bool) error {
	if _, ok := DescriptorFor(kind); !ok {
		return fmt.Errorf("unknown source kind %q", kind)
	}
	switch kind {
	case "rss":
		urls := opts.Strings("urls")
		if u := opts.String("url", ""); u != "" {
			urls = append(urls, u)
		}
		if len(urls) == 0 {
			return fmt.Errorf("feed URL is required")
		}
		for _, raw := range urls {
			if err := validateHTTPURL(raw, allowPrivate); err != nil {
				return err
			}
		}
	case "telegram":
		channels := opts.Strings("channels")
		if len(channels) == 0 {
			if c := opts.String("channel", ""); c != "" {
				channels = []string{c}
			}
		}
		if len(channels) == 0 {
			return fmt.Errorf("at least one channel is required")
		}
	case "reddit":
		subs := opts.Strings("subreddits")
		if len(subs) == 0 {
			if s := opts.String("subreddit", ""); s != "" {
				subs = []string{s}
			}
		}
		if len(subs) == 0 {
			return fmt.Errorf("at least one subreddit is required")
		}
		switch opts.String("sort", "hot") {
		case "hot", "new", "top", "rising":
		default:
			return fmt.Errorf("sort must be hot, new, top or rising")
		}
	case "mastodon":
		if opts.String("instance", "") == "" {
			return fmt.Errorf("instance is required")
		}
		if opts.String("tag", "") == "" {
			return fmt.Errorf("tag or \"local\" is required")
		}
	case "github":
		if len(opts.Strings("repos")) == 0 {
			return fmt.Errorf("at least one repository is required")
		}
	case "gdelt":
		if opts.String("query", "") == "" {
			return fmt.Errorf("query is required")
		}
	}
	return nil
}

func validateHTTPURL(raw string, allowPrivate bool) error {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") {
		return fmt.Errorf("%q is not an http(s) URL", raw)
	}
	if allowPrivate {
		return nil
	}
	host := strings.ToLower(u.Hostname())
	if host == "" || host == "localhost" || strings.HasSuffix(host, ".local") || strings.HasSuffix(host, ".internal") {
		return fmt.Errorf("%q points to a private host; enable fetch.allow_private_networks for RSSHub", raw)
	}
	if ip := net.ParseIP(host); ip != nil {
		if ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() || ip.IsUnspecified() {
			return fmt.Errorf("%q points to a private address; enable fetch.allow_private_networks for RSSHub", raw)
		}
		return nil
	}
	ips, err := net.LookupIP(host)
	if err != nil {
		return nil // let the fetch fail and show the real error
	}
	for _, ip := range ips {
		if ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() || ip.IsUnspecified() {
			return fmt.Errorf("%q resolves to a private address; enable fetch.allow_private_networks for RSSHub", raw)
		}
	}
	return nil
}
