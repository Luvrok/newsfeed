// Package httpx is the small HTTP toolkit shared by source fetchers.
package httpx

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	htmlesc "html"
	"io"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"

	"golang.org/x/net/html"
)

// ErrNotModified is returned when a conditional request hits a 304.
var ErrNotModified = errors.New("not modified")

// Client builds a keep-alive client with sane timeouts for feed polling.
// An explicit proxy overrides HTTP_PROXY / HTTPS_PROXY for this client only.
func Client(timeout time.Duration, userAgent, proxy string) *http.Client {
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	proxyFunc := http.ProxyFromEnvironment
	switch proxy = strings.TrimSpace(proxy); {
	case strings.EqualFold(proxy, "direct"):
		proxyFunc = nil
	case proxy != "":
		parsed, err := url.Parse(proxy)
		if err != nil {
			proxyFunc = func(*http.Request) (*url.URL, error) {
				return nil, fmt.Errorf("invalid proxy %q: %w", proxy, err)
			}
		} else {
			proxyFunc = http.ProxyURL(parsed)
		}
	}
	transport := &http.Transport{
		Proxy:                 noProxyLoopback(proxyFunc),
		DialContext:           (&net.Dialer{Timeout: 10 * time.Second, KeepAlive: 30 * time.Second}).DialContext,
		MaxIdleConns:          64,
		MaxIdleConnsPerHost:   8,
		IdleConnTimeout:       90 * time.Second,
		TLSHandshakeTimeout:   10 * time.Second,
		ExpectContinueTimeout: 1 * time.Second,
		ForceAttemptHTTP2:     true,
	}
	return &http.Client{
		Timeout:   timeout,
		Transport: &userAgentTransport{base: transport, ua: userAgent},
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= 5 {
				return errors.New("too many redirects")
			}
			return nil
		},
	}
}

type userAgentTransport struct {
	base http.RoundTripper
	ua   string
}

func (t *userAgentTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if t.ua != "" && req.Header.Get("User-Agent") == "" {
		req = req.Clone(req.Context())
		req.Header.Set("User-Agent", t.ua)
	}
	return t.base.RoundTrip(req)
}

// Request describes a GET. Header entries are optional conditional headers.
func Get(ctx context.Context, client *http.Client, url string, header http.Header) ([]byte, http.Header, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, nil, err
	}
	for k, vs := range header {
		for _, v := range vs {
			req.Header.Add(k, v)
		}
	}
	// Do not set Accept-Encoding by hand: net/http only transparently
	// decompresses gzip when it negotiated the header itself.
	resp, err := client.Do(req)
	if err != nil {
		return nil, nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotModified {
		return nil, resp.Header, ErrNotModified
	}
	body, err := readLimited(resp.Body, 8<<20)
	if err != nil {
		return nil, resp.Header, fmt.Errorf("read %s: %w", url, err)
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		snippet := strings.TrimSpace(string(body))
		if len(snippet) > 180 {
			snippet = snippet[:180] + "..."
		}
		return nil, resp.Header, fmt.Errorf("%s: http %d: %s", hostOf(url), resp.StatusCode, snippet)
	}
	return body, resp.Header, nil
}

// GetJSON fetches url and decodes a JSON body into dst.
func GetJSON(ctx context.Context, client *http.Client, url string, dst any) (http.Header, error) {
	body, header, err := Get(ctx, client, url, nil)
	if err != nil {
		return header, err
	}
	if err := json.Unmarshal(body, dst); err != nil {
		return header, fmt.Errorf("%s: decode json: %w", hostOf(url), err)
	}
	return header, nil
}

func readLimited(r io.Reader, limit int64) ([]byte, error) {
	body, err := io.ReadAll(io.LimitReader(r, limit))
	if err != nil {
		return nil, err
	}
	return body, nil
}

func hostOf(raw string) string {
	if i := strings.Index(raw, "://"); i >= 0 {
		raw = raw[i+3:]
	}
	if i := strings.IndexByte(raw, '/'); i >= 0 {
		raw = raw[:i]
	}
	return raw
}

// Text turns an HTML fragment into readable plain text. It keeps line breaks
// for paragraphs and <br>, drops script/style and collapses whitespace.
func Text(fragment string) string {
	z := html.NewTokenizer(strings.NewReader(fragment))
	var b strings.Builder
	skip := 0
	for {
		tt := z.Next()
		if tt == html.ErrorToken {
			break
		}
		switch tt {
		case html.TextToken:
			if skip == 0 {
				b.Write(z.Text())
			}
		case html.StartTagToken, html.SelfClosingTagToken:
			name, _ := z.TagName()
			switch string(name) {
			case "script", "style":
				if tt == html.StartTagToken {
					skip++
				}
			case "br", "p", "div", "li", "tr", "h1", "h2", "h3", "h4", "blockquote":
				b.WriteByte('\n')
			}
		case html.EndTagToken:
			name, _ := z.TagName()
			switch string(name) {
			case "script", "style":
				if skip > 0 {
					skip--
				}
			case "p", "div", "li", "tr", "h1", "h2", "h3", "h4", "blockquote":
				b.WriteByte('\n')
			}
		}
	}
	return CollapseSpace(htmlesc.UnescapeString(b.String()))
}

var (
	spaceRun  = regexp.MustCompile(`[ \t\r\f\v]+`)
	blankLine = regexp.MustCompile(`\n{3,}`)
	htmlTag   = regexp.MustCompile(`<[^>]*>`)
)

// CollapseSpace trims lines and squeezes repeated whitespace while keeping
// at most one blank line.
func CollapseSpace(s string) string {
	s = strings.ReplaceAll(s, "\r\n", "\n")
	s = strings.ReplaceAll(s, "\u00a0", " ")
	lines := strings.Split(s, "\n")
	out := make([]string, 0, len(lines))
	for _, line := range lines {
		line = strings.TrimSpace(spaceRun.ReplaceAllString(line, " "))
		if line == "" && len(out) > 0 && out[len(out)-1] == "" {
			continue
		}
		out = append(out, line)
	}
	s = strings.TrimSpace(strings.Join(out, "\n"))
	s = blankLine.ReplaceAllString(s, "\n\n")
	return s
}

// StripTags removes markup without parsing, for feeds that double-encode.
func StripTags(s string) string {
	return CollapseSpace(htmlesc.UnescapeString(htmlTag.ReplaceAllString(s, " ")))
}

// Truncate cuts s to n runes, appending an ellipsis when it was longer.
func Truncate(s string, n int) string {
	if n <= 0 {
		return ""
	}
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	cut := n
	for cut > 0 && r[cut-1] == ' ' {
		cut--
	}
	return strings.TrimRight(string(r[:cut]), " ,;:-") + "…"
}

// FirstLine returns the first non-empty line, truncated to n runes.
func FirstLine(s string, n int) string {
	for _, line := range strings.Split(s, "\n") {
		if line = strings.TrimSpace(line); line != "" {
			return Truncate(line, n)
		}
	}
	return ""
}

// noProxyLoopback keeps a local RSSHub or another on-box service out of the
// outbound tunnel, matching the old Miniflux NO_PROXY setup.
func noProxyLoopback(next func(*http.Request) (*url.URL, error)) func(*http.Request) (*url.URL, error) {
	return func(req *http.Request) (*url.URL, error) {
		host := req.URL.Hostname()
		if strings.EqualFold(host, "localhost") {
			return nil, nil
		}
		if ip := net.ParseIP(host); ip != nil && ip.IsLoopback() {
			return nil, nil
		}
		if next == nil {
			return nil, nil
		}
		return next(req)
	}
}
