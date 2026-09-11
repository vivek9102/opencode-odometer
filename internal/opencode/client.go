// Package opencode is a minimal Go client for the OpenCode server.
//
// OpenCode exposes an HTTP API and a Server-Sent-Events (SSE) stream over
// http://127.0.0.1:4096. There is no official Go SDK (the SDK ships as
// TypeScript), so this package speaks the documented wire protocol directly.
//
// The client is read-only for cost data: it listens for message updates on
// the SSE stream (which carry token usage and cost) and can fetch session
// history over REST. It never writes to sessions itself.
package opencode

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"
)

// DefaultBaseURL is where the OpenCode server listens locally.
const DefaultBaseURL = "http://127.0.0.1:4096"

// TokenUsage mirrors the API's tokens object on an assistant message.
type TokenUsage struct {
	Input     int64      `json:"input"`
	Output    int64      `json:"output"`
	Reasoning int64      `json:"reasoning"`
	Cache     CacheUsage `json:"cache"`
}

// CacheUsage is the cache read/write token breakdown.
type CacheUsage struct {
	Read  int64 `json:"read"`
	Write int64 `json:"write"`
}

// AssistantMessage is the subset of the API's assistant message we need to
// price a turn.
type AssistantMessage struct {
	ID          string     `json:"id"`
	SessionID   string     `json:"sessionID"`
	Role        string     `json:"role"`
	ProviderID  string     `json:"providerID"`
	ModelID     string     `json:"modelID"`
	Tokens      TokenUsage `json:"tokens"`
	Cost        float64    `json:"cost"`
	Finish      string     `json:"finish"`
	TimeCreated int64      `json:"timeCreated,omitempty"`
}

// Message is the priced data extracted from an assistant message.
type Message struct {
	ID        string
	SessionID string
	Provider  string
	Model     string
	Tokens    TokenUsage
	Cost      float64
	// Finish is the assistant message's finish reason ("stop", "aborted",
	// "error", "length", ...). Empty when unknown.
	Finish      string
	TimeCreated int64
}

// Event is a decoded SSE event. Only the fields we consume are populated.
type Event struct {
	Type    string
	Message *Message
	// SessionID is available on session-level events and when a message is removed.
	SessionID string
	// MessageID is set on message.removed events so the forwarder can drop
	// (net out) the removed message's recorded cost.
	MessageID string
}

// Client is a thin HTTP+SSE client for the OpenCode server.
type Client struct {
	mu       sync.RWMutex
	baseURL  string
	resolver *Resolver
	http     *http.Client // short-timeout client for REST calls
	sse      *http.Client // no total timeout: the SSE stream is long-lived
}

// New returns a client pointing at the given base URL. An empty URL enables
// discovery: the plugin-published address is used when available, otherwise
// the common local ports are probed.
func New(baseURL string) *Client {
	c := &Client{
		resolver: NewResolver(baseURL),
		http:     &http.Client{Timeout: 30 * time.Second},
		// Client.Timeout covers the whole body read, so a long-lived SSE stream
		// would be hard-killed after 30s of silence. Use a timeout-free client
		// (with only a response-header timeout so a dead server still fails fast).
		sse: &http.Client{Transport: &http.Transport{ResponseHeaderTimeout: 15 * time.Second}},
	}
	if baseURL != "" {
		c.baseURL = strings.TrimRight(baseURL, "/")
	} else {
		c.baseURL = DefaultBaseURL
	}
	return c
}

// BaseURL reports the address currently in use.
func (c *Client) BaseURL() string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.baseURL
}

func (c *Client) setBaseURL(u string) {
	c.mu.Lock()
	c.baseURL = strings.TrimRight(u, "/")
	c.mu.Unlock()
}

// Rediscover re-resolves the server address (the plugin republishes it on
// every OpenCode start, and the port changes between runs). It reports the
// address in use and whether it changed.
func (c *Client) Rediscover() (string, bool) {
	if c.resolver == nil {
		return c.BaseURL(), false
	}
	found, changed := c.resolver.Resolve()
	if found != "" && changed {
		c.setBaseURL(found)
	}
	return c.BaseURL(), changed
}

// Health reports whether the server is reachable.
func (c *Client) Health() error {
	resp, err := c.http.Get(c.BaseURL() + "/global/health")
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("health: status %d", resp.StatusCode)
	}
	return nil
}

// Session is a minimal session record.
type Session struct {
	ID    string `json:"id"`
	Title string `json:"title"`
	Time  struct {
		Created int64 `json:"created"`
		Updated int64 `json:"updated"`
	} `json:"time"`
}

// Session lists all sessions.
func (c *Client) Session() ([]Session, error) {
	var out []Session
	if err := c.getJSON("/session", &out); err != nil {
		return nil, err
	}
	return out, nil
}

// parseRawAssistant parses an assistant message from multiple possible JSON envelope shapes:
// 1. properties.info
// 2. properties.message
// 3. direct properties (fields at the top level of properties)
type rawAssistant struct {
	ID          string     `json:"id"`
	MessageID   string     `json:"messageID"`
	SessionID   string     `json:"sessionID"`
	Role        string     `json:"role"`
	ProviderID  string     `json:"providerID"`
	Provider    string     `json:"provider"`
	ModelID     string     `json:"modelID"`
	Model       string     `json:"model"`
	Tokens      TokenUsage `json:"tokens"`
	Cost        float64    `json:"cost"`
	Finish      string     `json:"finish"`
	Time        struct {
		Created   int64 `json:"created"`
		Completed int64 `json:"completed"`
	} `json:"time"`
	TimeCreated int64 `json:"timeCreated"`
	CreatedAt   int64 `json:"createdAt"`
}

func (r rawAssistant) toAssistantMessage() (AssistantMessage, bool) {
	id := r.ID
	if id == "" {
		id = r.MessageID
	}
	prov := r.ProviderID
	if prov == "" {
		prov = r.Provider
	}
	mod := r.ModelID
	if mod == "" {
		mod = r.Model
	}
	// If provider is missing but model is "provider/model", split them
	if prov == "" && strings.Contains(mod, "/") {
		parts := strings.SplitN(mod, "/", 2)
		prov = parts[0]
		mod = parts[1]
	} else if prov != "" && strings.HasPrefix(mod, prov+"/") {
		mod = strings.TrimPrefix(mod, prov+"/")
	}

	role := strings.ToLower(r.Role)
	if role != "" && role != "assistant" {
		return AssistantMessage{}, false
	}
	if id == "" && mod == "" && r.Tokens.Input == 0 && r.Tokens.Output == 0 {
		return AssistantMessage{}, false
	}

	tc := r.Time.Created
	if tc == 0 {
		tc = r.TimeCreated
	}
	if tc == 0 {
		tc = r.CreatedAt
	}

	return AssistantMessage{
		ID:          id,
		SessionID:   r.SessionID,
		Role:        "assistant",
		ProviderID:  prov,
		ModelID:     mod,
		Tokens:      r.Tokens,
		Cost:        r.Cost,
		Finish:      r.Finish,
		TimeCreated: tc,
	}, true
}

// Messages fetches every message in a session.
func (c *Client) Messages(sessionID string) ([]Message, error) {
	var rawEntries []json.RawMessage
	if err := c.getJSON("/session/"+sessionID+"/message", &rawEntries); err != nil {
		return nil, err
	}
	out := make([]Message, 0, len(rawEntries))
	for _, raw := range rawEntries {
		// Try { "info": <rawAssistant> }
		var withInfo struct {
			Info rawAssistant `json:"info"`
		}
		if err := json.Unmarshal(raw, &withInfo); err == nil {
			if asst, ok := withInfo.Info.toAssistantMessage(); ok {
				if asst.SessionID == "" {
					asst.SessionID = sessionID
				}
				out = append(out, fromAssistant(asst))
				continue
			}
		}

		// Try { "message": <rawAssistant> }
		var withMsg struct {
			Message rawAssistant `json:"message"`
		}
		if err := json.Unmarshal(raw, &withMsg); err == nil {
			if asst, ok := withMsg.Message.toAssistantMessage(); ok {
				if asst.SessionID == "" {
					asst.SessionID = sessionID
				}
				out = append(out, fromAssistant(asst))
				continue
			}
		}

		// Try direct <rawAssistant>
		var direct rawAssistant
		if err := json.Unmarshal(raw, &direct); err == nil {
			if asst, ok := direct.toAssistantMessage(); ok {
				if asst.SessionID == "" {
					asst.SessionID = sessionID
				}
				out = append(out, fromAssistant(asst))
				continue
			}
		}
	}
	return out, nil
}

// Abort cancels a running session (the same path the Esc key / plugin uses).
// The canonical endpoint is POST /session/{sessionID}/abort; a 404 indicates
// the server does not expose an abort handler, which surfaces as an error.
func (c *Client) Abort(sessionID string) error {
	req, err := http.NewRequest(http.MethodPost,
		c.BaseURL()+"/session/"+sessionID+"/abort", nil)
	if err != nil {
		return err
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	io.Copy(io.Discard, resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusNoContent {
		return fmt.Errorf("abort: status %d", resp.StatusCode)
	}
	return nil
}

// getJSON performs a GET and decodes a JSON response into out.
func (c *Client) getJSON(path string, out any) error {
	resp, err := c.http.Get(c.BaseURL() + path)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("GET %s: status %d", path, resp.StatusCode)
	}
	return json.NewDecoder(resp.Body).Decode(out)
}

// Subscribe opens the SSE event stream and executes handler for each event.
// It blocks until the stream closes, the handler returns an error, or the
// server disconnects. A server that is not running surfaces as an error.
func (c *Client) Subscribe(handler func(Event) error) error {
	return c.SubscribeWithContext(context.Background(), handler)
}

// SubscribeWithContext opens the SSE event stream with context cancellation support.
func (c *Client) SubscribeWithContext(ctx context.Context, handler func(Event) error) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.BaseURL()+"/event", nil)
	if err != nil {
		return err
	}
	resp, err := c.sse.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("event stream: status %d", resp.StatusCode)
	}

	sc := bufio.NewScanner(resp.Body)
	sc.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)

	curType, curData := "", ""
	flush := func() error {
		defer func() {
			curType, curData = "", ""
		}()
		if curData == "" {
			return nil
		}
		eventType := curType
		if eventType == "" {
			// Extract type from JSON data payload: { "type": "...", ... }
			var env struct {
				Type  string `json:"type"`
				Event string `json:"event"`
			}
			if err := json.Unmarshal([]byte(curData), &env); err == nil {
				if env.Type != "" {
					eventType = env.Type
				} else if env.Event != "" {
					eventType = env.Event
				}
			}
		}
		if eventType == "" {
			return nil
		}
		if ev, err := parseEvent(eventType, curData); err == nil {
			if ev.Type != "" || ev.Message != nil || ev.SessionID != "" {
				return handler(ev)
			}
		}
		return nil
	}

	for sc.Scan() {
		line := sc.Text()
		if line == "" {
			if err := flush(); err != nil {
				return err
			}
			continue
		}
		if strings.HasPrefix(line, "event:") {
			curType = strings.TrimSpace(strings.TrimPrefix(line, "event:"))
		} else if strings.HasPrefix(line, "data:") {
			d := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
			if curData == "" {
				curData = d
			} else {
				curData += "\n" + d
			}
		} else if curData != "" && !strings.HasPrefix(line, ":") && !strings.HasPrefix(line, "id:") && !strings.HasPrefix(line, "retry:") {
			curData += "\n" + strings.TrimSpace(line)
		}
	}
	if err := flush(); err != nil {
		return err
	}
	if err := sc.Err(); err != nil {
		return err
	}
	return io.EOF
}

// parseEvent decodes a known event into our Event struct.
func parseEvent(eventType, data string) (Event, error) {
	ev := Event{Type: eventType}

	switch eventType {
	case "message.updated", "message.created":
		var payload struct {
			Properties struct {
				SessionID string        `json:"sessionID"`
				Info      *rawAssistant `json:"info"`
				Message   *rawAssistant `json:"message"`
				rawAssistant
			} `json:"properties"`
			SessionID string        `json:"sessionID"`
			Info      *rawAssistant `json:"info"`
			Message   *rawAssistant `json:"message"`
			rawAssistant
		}
		if err := json.Unmarshal([]byte(data), &payload); err != nil {
			return ev, err
		}

		candidates := []*rawAssistant{
			payload.Properties.Info,
			payload.Properties.Message,
			&payload.Properties.rawAssistant,
			payload.Info,
			payload.Message,
			&payload.rawAssistant,
		}

		for _, cand := range candidates {
			if cand == nil {
				continue
			}
			if asst, ok := cand.toAssistantMessage(); ok {
				if asst.SessionID == "" {
					asst.SessionID = payload.Properties.SessionID
				}
				if asst.SessionID == "" {
					asst.SessionID = payload.SessionID
				}
				m := fromAssistant(asst)
				ev.Message = &m
				ev.SessionID = m.SessionID
				return ev, nil
			}
		}

	case "message.removed":
		var payload struct {
			Properties struct {
				SessionID string `json:"sessionID"`
				MessageID string `json:"messageID"`
			} `json:"properties"`
			SessionID string `json:"sessionID"`
			MessageID string `json:"messageID"`
		}
		_ = json.Unmarshal([]byte(data), &payload)
		ev.SessionID = payload.Properties.SessionID
		if ev.SessionID == "" {
			ev.SessionID = payload.SessionID
		}
		ev.MessageID = payload.Properties.MessageID
		if ev.MessageID == "" {
			ev.MessageID = payload.MessageID
		}

	case "session.created", "session.updated", "session.status", "session.idle":
		var payload struct {
			Properties struct {
				SessionID string `json:"sessionID"`
				ID        string `json:"id"`
			} `json:"properties"`
			SessionID string `json:"sessionID"`
			ID        string `json:"id"`
		}
		_ = json.Unmarshal([]byte(data), &payload)
		sid := payload.Properties.SessionID
		if sid == "" {
			sid = payload.Properties.ID
		}
		if sid == "" {
			sid = payload.SessionID
		}
		if sid == "" {
			sid = payload.ID
		}
		ev.SessionID = sid

	default:
		// Attempt to extract sessionID, messageID or assistant message from any other event
		var payload struct {
			Properties struct {
				SessionID string        `json:"sessionID"`
				MessageID string        `json:"messageID"`
				ID        string        `json:"id"`
				Info      *rawAssistant `json:"info"`
				Message   *rawAssistant `json:"message"`
				rawAssistant
			} `json:"properties"`
			SessionID string        `json:"sessionID"`
			MessageID string        `json:"messageID"`
			ID        string        `json:"id"`
			Info      *rawAssistant `json:"info"`
			Message   *rawAssistant `json:"message"`
			rawAssistant
		}
		if err := json.Unmarshal([]byte(data), &payload); err == nil {
			candidates := []*rawAssistant{
				payload.Properties.Info,
				payload.Properties.Message,
				&payload.Properties.rawAssistant,
				payload.Info,
				payload.Message,
				&payload.rawAssistant,
			}
			for _, cand := range candidates {
				if cand == nil {
					continue
				}
				if asst, ok := cand.toAssistantMessage(); ok {
					if asst.SessionID == "" {
						asst.SessionID = payload.Properties.SessionID
					}
					if asst.SessionID == "" {
						asst.SessionID = payload.SessionID
					}
					m := fromAssistant(asst)
					ev.Message = &m
					ev.SessionID = m.SessionID
					return ev, nil
				}
			}

			sid := payload.Properties.SessionID
			if sid == "" {
				sid = payload.SessionID
			}
			if sid == "" && (strings.HasPrefix(eventType, "session.") || strings.HasPrefix(eventType, "session_")) {
				if payload.Properties.ID != "" {
					sid = payload.Properties.ID
				} else if payload.ID != "" {
					sid = payload.ID
				}
			}
			ev.SessionID = sid
			mid := payload.Properties.MessageID
			if mid == "" {
				mid = payload.MessageID
			}
			ev.MessageID = mid
		}
	}
	return ev, nil
}

func fromAssistant(a AssistantMessage) Message {
	return Message{
		ID:          a.ID,
		SessionID:   a.SessionID,
		Provider:    a.ProviderID,
		Model:       a.ModelID,
		Tokens:      a.Tokens,
		Cost:        a.Cost,
		Finish:      a.Finish,
		TimeCreated: a.TimeCreated,
	}
}
