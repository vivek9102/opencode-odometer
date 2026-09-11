package opencode

import (
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// ServerPointerFile is where the bundled plugin publishes the live OpenCode
// server URL. The plugin receives `serverUrl` in its PluginInput, so it knows
// the real address even when OpenCode is not on the default port; writing it
// here is the only reliable way for a separate process to find out.
//
// This file has a single writer (the plugin), mirroring the way
// ~/.opencode-odometer.json has a single writer (the odometer).
const ServerPointerFile = ".opencode-odometer-server.json"

// serverPointerStale is how long a published URL is trusted. OpenCode picks a
// fresh port per run, so a pointer left behind by a dead server must expire
// rather than pin us to a port nobody is listening on.
const serverPointerStale = 12 * time.Hour

// CandidatePorts are probed when no URL is configured or published. OpenCode
// defaults to 4096 and increments when that port is taken.
var CandidatePorts = []int{4096, 4097, 4098, 4099, 4100}

// ServerPointer is the document the plugin writes.
type ServerPointer struct {
	Comment   string `json:"_comment"`
	ServerURL string `json:"server_url"`
	PID       int    `json:"pid"`
	Updated   int64  `json:"updated"`
}

// ServerPointerPath returns the absolute path of the plugin-written pointer.
func ServerPointerPath() string {
	home, err := os.UserHomeDir()
	if err != nil {
		home = "."
	}
	return filepath.Join(home, ServerPointerFile)
}

// readServerPointer returns the URL the plugin published, if it is still
// trustworthy: recent enough, and written by a process that is still alive.
//
// Age alone is not enough. A plugin that exited seconds ago leaves a pointer
// that looks fresh but names a port nobody is bound to any more, which sends
// every reconnect to a dead address.
func readServerPointer() string {
	raw, err := os.ReadFile(ServerPointerPath())
	if err != nil {
		return ""
	}
	var p ServerPointer
	if err := json.Unmarshal(raw, &p); err != nil {
		return ""
	}
	if p.ServerURL == "" {
		return ""
	}
	if p.Updated > 0 {
		age := time.Since(time.Unix(p.Updated, 0))
		if age > serverPointerStale {
			return ""
		}
	}
	if p.PID > 0 && !processAlive(p.PID) {
		return ""
	}
	return normalizeLoopback(strings.TrimRight(p.ServerURL, "/"))
}

// normalizeLoopback rewrites a "localhost" host to 127.0.0.1.
//
// On Windows, "localhost" resolves to ::1 first, and Go will wait on that
// before trying IPv4. OpenCode binds 127.0.0.1 only, so every request pays a
// long IPv6 timeout before succeeding — or fails outright. Pinning the literal
// IPv4 address avoids the stall entirely.
func normalizeLoopback(u string) string {
	for _, scheme := range []string{"http://", "https://"} {
		if strings.HasPrefix(u, scheme+"localhost") {
			return scheme + "127.0.0.1" + strings.TrimPrefix(u, scheme+"localhost")
		}
	}
	return u
}

// probe reports whether an OpenCode server answers at the given base URL.
//
// /session is used rather than /global/health because it exists across
// versions and confirms we are talking to something that can actually serve
// the data we need — a bare TCP connect would happily match any listener.
func probe(baseURL string, timeout time.Duration) bool {
	c := &http.Client{Timeout: timeout}
	resp, err := c.Get(strings.TrimRight(baseURL, "/") + "/session")
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	return resp.StatusCode == http.StatusOK
}

// portOpen is a fast pre-check so we only pay for an HTTP round trip on ports
// that actually have a listener.
func portOpen(port int, timeout time.Duration) bool {
	conn, err := net.DialTimeout("tcp",
		fmt.Sprintf("127.0.0.1:%d", port), timeout)
	if err != nil {
		return false
	}
	_ = conn.Close()
	return true
}

// Discover resolves the OpenCode server base URL, in priority order:
//
//  1. an explicitly configured URL (OPENCODE_URL / config) — always wins
//  2. the URL published by the plugin (authoritative when OpenCode is running)
//  3. a probe of the common local ports
//
// It returns an empty string when nothing is reachable, which the caller
// surfaces to the user as "disconnected" rather than retrying blindly.
func Discover(configured string) string {
	if configured != "" {
		return strings.TrimRight(configured, "/")
	}
	if u := readServerPointer(); u != "" && probe(u, 2*time.Second) {
		return u
	}
	for _, port := range CandidatePorts {
		if !portOpen(port, 150*time.Millisecond) {
			continue
		}
		u := fmt.Sprintf("http://127.0.0.1:%d", port)
		if probe(u, 2*time.Second) {
			return u
		}
	}
	// Nothing reachable. Fall back to the published URL even if unproven, so
	// a server still starting up is retried at the right address.
	if u := readServerPointer(); u != "" {
		return u
	}
	return ""
}

// Resolver keeps a client pointed at a live server. The base URL is re-resolved
// whenever the connection drops, because OpenCode may restart on another port.
type Resolver struct {
	mu         sync.RWMutex
	configured string
	current    string
}

// NewResolver returns a Resolver seeded with an optionally configured URL.
func NewResolver(configured string) *Resolver {
	return &Resolver{configured: strings.TrimRight(configured, "/")}
}

// Current returns the last resolved URL without probing.
func (r *Resolver) Current() string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if r.current != "" {
		return r.current
	}
	return r.configured
}

// Resolve re-discovers the server URL and reports it along with whether it
// changed since the last call.
func (r *Resolver) Resolve() (url string, changed bool) {
	found := Discover(r.configured)
	r.mu.Lock()
	defer r.mu.Unlock()
	if found == "" {
		return r.current, false
	}
	changed = found != r.current
	r.current = found
	return found, changed
}
