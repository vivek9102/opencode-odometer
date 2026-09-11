package opencode

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"
)

// stubServer stands in for a running OpenCode instance.
func stubServer(t *testing.T) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/session", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `[]`)
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

// withPointer writes a server pointer into a temporary HOME for the test.
func withPointer(t *testing.T, url string, updated int64) {
	t.Helper()
	home := t.TempDir()
	// Discover reads the pointer relative to the user's home directory.
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)

	if url == "" {
		return
	}
	doc, err := json.Marshal(ServerPointer{
		ServerURL: url,
		PID:       os.Getpid(),
		Updated:   updated,
	})
	if err != nil {
		t.Fatalf("marshal pointer: %v", err)
	}
	if err := os.WriteFile(ServerPointerPath(), doc, 0o644); err != nil {
		t.Fatalf("write pointer: %v", err)
	}
}

// TestDiscoverUsesPublishedURL covers the main fix: OpenCode does not always
// listen on 4096, so the odometer must use the address the plugin published.
func TestDiscoverUsesPublishedURL(t *testing.T) {
	srv := stubServer(t)
	withPointer(t, srv.URL, time.Now().Unix())

	if got := Discover(""); got != srv.URL {
		t.Errorf("Discover() = %q, want the published %q", got, srv.URL)
	}
}

// TestDiscoverIgnoresStalePointer ensures a pointer left behind by a dead
// server does not pin the odometer to a port nobody is listening on.
func TestDiscoverIgnoresStalePointer(t *testing.T) {
	srv := stubServer(t)
	withPointer(t, srv.URL, time.Now().Add(-48*time.Hour).Unix())

	if got := Discover(""); got == srv.URL {
		t.Errorf("stale pointer should be ignored, got %q", got)
	}
}

// TestDiscoverPrefersConfiguredURL: an explicit setting must always win, so a
// user pointing at a remote server is never silently redirected.
func TestDiscoverPrefersConfiguredURL(t *testing.T) {
	srv := stubServer(t)
	withPointer(t, srv.URL, time.Now().Unix())

	const configured = "http://example.invalid:1234"
	if got := Discover(configured); got != configured {
		t.Errorf("Discover(%q) = %q, configured URL must win", configured, got)
	}
}

// TestClientRediscover verifies the client re-points at the published address,
// which is what lets it recover when OpenCode restarts on another port.
func TestClientRediscover(t *testing.T) {
	srv := stubServer(t)
	withPointer(t, srv.URL, time.Now().Unix())

	c := New("")
	url, changed := c.Rediscover()
	if url != srv.URL {
		t.Errorf("Rediscover() = %q, want %q", url, srv.URL)
	}
	if !changed {
		t.Error("first resolution should report a change")
	}
	if c.BaseURL() != srv.URL {
		t.Errorf("BaseURL() = %q, want %q", c.BaseURL(), srv.URL)
	}

	// The client must now actually work against the discovered address.
	if _, err := c.Session(); err != nil {
		t.Errorf("Session() after rediscovery: %v", err)
	}

	if _, changed := c.Rediscover(); changed {
		t.Error("re-resolving the same URL should not report a change")
	}
}
