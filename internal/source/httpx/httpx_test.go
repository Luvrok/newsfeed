package httpx

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

func TestClientUsesProxy(t *testing.T) {
	var hits atomic.Int32
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		if r.URL.String() != "http://example.com/x" {
			t.Errorf("proxy got %q, want the absolute target URL", r.URL.String())
		}
		w.Header().Set("Content-Type", "text/plain")
		_, _ = w.Write([]byte("proxied"))
	}))
	defer proxy.Close()

	client := Client(5*time.Second, "newsfeed-test", proxy.URL)
	body, _, err := Get(context.Background(), client, "http://example.com/x", nil)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if string(body) != "proxied" {
		t.Fatalf("got %q, want proxied", body)
	}
	if hits.Load() != 1 {
		t.Fatalf("proxy was hit %d times, want 1", hits.Load())
	}
}

func TestClientRejectsBadProxy(t *testing.T) {
	client := Client(time.Second, "newsfeed-test", "://not a proxy")
	_, _, err := Get(context.Background(), client, "http://example.com/x", nil)
	if err == nil {
		t.Fatal("expected an error for an invalid proxy")
	}
}
