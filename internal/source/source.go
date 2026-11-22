// Package source defines the contract every news source implements.
//
// A new source is a package with a factory that registers itself in init:
//
//	func init() { source.Register("example", func() source.Fetcher { return &Fetcher{} }) }
//
// Then the package is imported for its side effect in internal/source/all.
// The scheduler discovers it from the config file, where kind = "example".
package source

import (
	"context"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"sync"

	"newsfeed/internal/model"
)

// Options is the untyped [sources.options] table from the config file.
// Each fetcher reads what it needs and ignores the rest, so adding an option
// never touches the shared interface.
type Options map[string]any

// String returns a trimmed string option or the fallback.
func (o Options) String(key, def string) string {
	switch v := o[key].(type) {
	case string:
		if s := strings.TrimSpace(v); s != "" {
			return s
		}
	case fmt.Stringer:
		return v.String()
	case int64:
		return strconv.FormatInt(v, 10)
	case float64:
		return strconv.FormatFloat(v, 'f', -1, 64)
	}
	return def
}

// Strings accepts a TOML array or a single string.
func (o Options) Strings(key string) []string {
	switch v := o[key].(type) {
	case string:
		if s := strings.TrimSpace(v); s != "" {
			return []string{s}
		}
	case []any:
		out := make([]string, 0, len(v))
		for _, e := range v {
			if s, ok := e.(string); ok && strings.TrimSpace(s) != "" {
				out = append(out, strings.TrimSpace(s))
			}
		}
		return out
	case []string:
		return v
	}
	return nil
}

// Int returns an integer option or the fallback.
func (o Options) Int(key string, def int) int {
	switch v := o[key].(type) {
	case int:
		return v
	case int64:
		return int(v)
	case float64:
		return int(v)
	case string:
		if n, err := strconv.Atoi(strings.TrimSpace(v)); err == nil {
			return n
		}
	}
	return def
}

// Bool returns a boolean option or the fallback.
func (o Options) Bool(key string, def bool) bool {
	switch v := o[key].(type) {
	case bool:
		return v
	case string:
		switch strings.ToLower(strings.TrimSpace(v)) {
		case "true", "yes", "1", "on":
			return true
		case "false", "no", "0", "off":
			return false
		}
	}
	return def
}

// Fetcher pulls a batch of items. It must respect ctx cancellation and
// return an error rather than a partial batch when nothing useful arrived.
type Fetcher interface {
	Fetch(ctx context.Context, opts Options) ([]model.Item, error)
}

// Factory creates a fresh fetcher. Fetchers are per source, so they may keep
// state such as ETag caches between calls.
type Factory func() Fetcher

var (
	mu        sync.RWMutex
	factories = map[string]Factory{}
)

// Register adds a kind to the global registry. It panics on duplicates,
// which can only happen from a programming mistake.
func Register(kind string, f Factory) {
	mu.Lock()
	defer mu.Unlock()
	if _, ok := factories[kind]; ok {
		panic("source: kind registered twice: " + kind)
	}
	factories[kind] = f
}

// New builds a fetcher for kind.
func New(kind string) (Fetcher, error) {
	mu.RLock()
	f, ok := factories[kind]
	mu.RUnlock()
	if !ok {
		return nil, fmt.Errorf("unknown source kind %q (known: %s)", kind, strings.Join(Kinds(), ", "))
	}
	return f(), nil
}

// Known reports whether this build ships a fetcher for the kind.
func Known(kind string) bool {
	mu.RLock()
	_, ok := factories[kind]
	mu.RUnlock()
	return ok
}

// Kinds lists every registered kind, sorted.
func Kinds() []string {
	mu.RLock()
	defer mu.RUnlock()
	out := make([]string, 0, len(factories))
	for k := range factories {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
