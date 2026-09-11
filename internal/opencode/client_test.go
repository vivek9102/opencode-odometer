package opencode

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestFromAssistant(t *testing.T) {
	a := AssistantMessage{
		ID: "m1", SessionID: "s1", Role: "assistant",
		ProviderID: "acme", ModelID: "sonnet",
		Tokens: TokenUsage{
			Input: 1000, Output: 500, Reasoning: 20,
			Cache: CacheUsage{Read: 3000, Write: 200},
		},
		Cost: 0.5, Finish: "end_turn",
	}
	m := fromAssistant(a)
	if m.ID != "m1" || m.SessionID != "s1" || m.Provider != "acme" || m.Model != "sonnet" {
		t.Errorf("bad identity fields: %+v", m)
	}
	if m.Tokens.Input != 1000 || m.Tokens.Output != 500 || m.Tokens.Cache.Read != 3000 {
		t.Errorf("bad tokens: %+v", m.Tokens)
	}
	if m.Cost != 0.5 {
		t.Errorf("cost = %v, want 0.5", m.Cost)
	}
}

func TestParseEventMessageUpdated(t *testing.T) {
	payload := `{"properties":{"info":{
		"id":"m1","sessionID":"s1","role":"assistant",
		"providerID":"acme","modelID":"sonnet",
		"tokens":{"input":10,"output":20,"reasoning":3,"cache":{"read":30,"write":5}},
		"cost":1.25,"finish":"end_turn"
	}}}`
	ev, err := parseEvent("message.updated", payload)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if ev.Message == nil {
		t.Fatal("expected message")
	}
	if ev.Type != "message.updated" {
		t.Errorf("type = %v", ev.Type)
	}
	if ev.Message.Provider != "acme" || ev.Message.Model != "sonnet" {
		t.Errorf("bad model fields: %+v", ev.Message)
	}
	if ev.Message.Tokens.Cache.Read != 30 || ev.Message.Cost != 1.25 {
		t.Errorf("bad pricing fields: %+v", ev.Message)
	}
}

func TestParseEventIgnoresUserMessage(t *testing.T) {
	payload := `{"properties":{"info":{
		"id":"m2","sessionID":"s1","role":"user","providerID":"","modelID":"",
		"tokens":{"input":0,"output":0,"reasoning":0,"cache":{"read":0,"write":0}},
		"cost":0
	}}}`
	ev, err := parseEvent("message.updated", payload)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if ev.Message != nil {
		t.Error("user message should be ignored")
	}
}

func TestParseEventSessionStatus(t *testing.T) {
	payload := `{"properties":{"sessionID":"abc123"}}`
	ev, err := parseEvent("session.idle", payload)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if ev.SessionID != "abc123" {
		t.Errorf("sessionID = %q, want abc123", ev.SessionID)
	}
}

func TestHealthOK(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"healthy":true}`))
	}))
	defer srv.Close()
	c := New(srv.URL)
	if err := c.Health(); err != nil {
		t.Fatalf("Health: %v", err)
	}
}

func TestHealthDown(t *testing.T) {
	c := New("http://127.0.0.1:1") // nothing listening
	if err := c.Health(); err == nil {
		t.Fatal("expected error when server is down")
	}
}

func TestMessagesFiltersNonAssistantAndDecodes(t *testing.T) {
	user := map[string]any{
		"info": map[string]any{"id": "u1", "sessionID": "s1", "role": "user"},
	}
	asst := map[string]any{
		"info": map[string]any{
			"id": "a1", "sessionID": "s1", "role": "assistant",
			"providerID": "acme", "modelID": "opus",
			"tokens": map[string]any{"input": 100, "output": 50, "reasoning": 0,
				"cache": map[string]any{"read": 10, "write": 5}},
			"cost": 2.5,
		},
	}
	raw, _ := json.Marshal([]map[string]any{user, asst})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/session/s1/message" {
			_, _ = w.Write(raw)
			return
		}
		http.NotFound(w, r)
	}))
	defer srv.Close()

	c := New(srv.URL)
	msgs, err := c.Messages("s1")
	if err != nil {
		t.Fatalf("Messages: %v", err)
	}
	if len(msgs) != 1 {
		t.Fatalf("expected 1 assistant message, got %d", len(msgs))
	}
	if msgs[0].Model != "opus" || msgs[0].Tokens.Input != 100 {
		t.Errorf("bad decode: %+v", msgs[0])
	}
}

func TestSSEStream(t *testing.T) {
	events := []string{
		"event: message.created",
		"data: {\"properties\":{\"info\":{\"id\":\"m1\",\"sessionID\":\"s1\",\"role\":\"assistant\",\"providerID\":\"acme\",\"modelID\":\"sonnet\",\"tokens\":{\"input\":1,\"output\":2,\"reasoning\":0,\"cache\":{\"read\":3,\"write\":0}},\"cost\":0.1,\"finish\":\"end_turn\"}}}",
		"",
	}
	body := strings.Join(events, "\n")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/event" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte(body))
	}))
	defer srv.Close()

	c := New(srv.URL)
	var got []Message
	err := c.Subscribe(func(ev Event) error {
		if ev.Message != nil {
			got = append(got, *ev.Message)
		}
		return nil
	})
	if err == nil {
		t.Fatal("expected EOF after stream close")
	}
	if len(got) != 1 {
		t.Fatalf("expected 1 message, got %d", len(got))
	}
	if got[0].ID != "m1" || got[0].Tokens.Input != 1 {
		t.Errorf("bad sse decode: %+v", got[0])
	}
}

func TestSSEStreamWithoutEventHeader(t *testing.T) {
	// OpenCode sometimes sends SSE lines with only `data: {"type": "message.updated", ...}`
	events := []string{
		"data: {\"type\":\"message.updated\",\"properties\":{\"info\":{\"id\":\"m2\",\"sessionID\":\"s1\",\"role\":\"assistant\",\"providerID\":\"openai\",\"modelID\":\"gpt-4o\",\"tokens\":{\"input\":100,\"output\":200,\"reasoning\":0,\"cache\":{\"read\":50,\"write\":0}},\"cost\":0.005,\"finish\":\"stop\"}}}",
		"",
	}
	body := strings.Join(events, "\n")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte(body))
	}))
	defer srv.Close()

	c := New(srv.URL)
	var got []Message
	_ = c.Subscribe(func(ev Event) error {
		if ev.Message != nil {
			got = append(got, *ev.Message)
		}
		return nil
	})
	if len(got) != 1 {
		t.Fatalf("expected 1 message from data-only SSE, got %d", len(got))
	}
	if got[0].ID != "m2" || got[0].Provider != "openai" || got[0].Model != "gpt-4o" {
		t.Errorf("bad decode: %+v", got[0])
	}
}

func TestSSEStreamDirectProperties(t *testing.T) {
	// Properties with message fields directly at top-level
	events := []string{
		"data: {\"type\":\"message.created\",\"properties\":{\"id\":\"m3\",\"sessionID\":\"s2\",\"role\":\"assistant\",\"provider\":\"anthropic\",\"model\":\"anthropic/claude-3-5-sonnet\",\"tokens\":{\"input\":50,\"output\":75,\"reasoning\":0,\"cache\":{\"read\":0,\"write\":0}},\"cost\":0.001}}",
		"",
	}
	body := strings.Join(events, "\n")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte(body))
	}))
	defer srv.Close()

	c := New(srv.URL)
	var got []Message
	_ = c.Subscribe(func(ev Event) error {
		if ev.Message != nil {
			got = append(got, *ev.Message)
		}
		return nil
	})
	if len(got) != 1 {
		t.Fatalf("expected 1 message, got %d", len(got))
	}
	if got[0].ID != "m3" || got[0].Provider != "anthropic" || got[0].Model != "claude-3-5-sonnet" {
		t.Errorf("bad decode / normalization: %+v", got[0])
	}
}

func TestAbort(t *testing.T) {
	var called string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost && strings.HasPrefix(r.URL.Path, "/session/s1/abort") {
			called = r.URL.Path
			w.WriteHeader(http.StatusNoContent)
			return
		}
		http.NotFound(w, r)
	}))
	defer srv.Close()

	c := New(srv.URL)
	if err := c.Abort("s1"); err != nil {
		t.Fatalf("Abort: %v", err)
	}
	if called != "/session/s1/abort" {
		t.Errorf("abort path = %q", called)
	}
}
