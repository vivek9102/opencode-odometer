package app

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestCleanupOnlyOldUnreferencedTerminalProtocol(t *testing.T) {
	a, _, _ := newTestApp(t)
	dir := a.experienceDir()
	now := time.Now()
	old := now.Add(-8 * 24 * time.Hour)
	fixtures := map[string]string{
		"switch-status-finished.json":  `{"id":"finished","status":"confirmed"}`,
		"abort-status-failed.json":     `{"id":"failed","status":"failed"}`,
		"switch-status-active.json":    `{"id":"active","status":"confirmed"}`,
		"switch-session-chat.json":     `{"id":"active","persistent":true,"key":"test/chat"}`,
		"continue-status-waiting.json": `{"id":"waiting","status":"prepared"}`,
		"continue-status-pending.json": `{"id":"pending","status":"failed"}`,
		"continue-999-pending.json":    `{"id":"pending","instance":"owner"}`,
		"models-dead.json":             `{"pid":2147483647,"updated":1}`,
		"models-live.json":             fmt.Sprintf(`{"pid":%d,"updated":1}`, os.Getpid()),
		"tui-closed.json":              `{"id":"closed","closed":true,"updated":1}`,
		"tui-owner.json":               `{"id":"owner","closed":true,"updated":1}`,
		"switch-status-malformed.json": `not-json`,
		"switch-status-recent.json":    `{"id":"recent","status":"confirmed"}`,
		"events.jsonl":                 `unread`, "budget.json": `{"enabled":true}`, "user.csv": "export",
	}
	for name, body := range fixtures {
		path := filepath.Join(dir, name)
		if e := os.WriteFile(path, []byte(body), 0600); e != nil {
			t.Fatal(e)
		}
		if name != "switch-status-recent.json" {
			if e := os.Chtimes(path, old, old); e != nil {
				t.Fatal(e)
			}
		}
	}
	outside := filepath.Join(t.TempDir(), "switch-status-outside.json")
	if e := os.WriteFile(outside, []byte(`{"id":"outside","status":"failed"}`), 0600); e != nil {
		t.Fatal(e)
	}
	if removed := a.CleanupProtocolFiles(now); removed != 4 {
		t.Fatal("unexpected cleanup count", removed)
	}
	deleted := map[string]bool{"switch-status-finished.json": true, "abort-status-failed.json": true, "models-dead.json": true, "tui-closed.json": true}
	for name := range fixtures {
		_, e := os.Stat(filepath.Join(dir, name))
		if deleted[name] && !os.IsNotExist(e) || !deleted[name] && e != nil {
			t.Fatalf("incorrect treatment %s: %v", name, e)
		}
	}
	if _, e := os.Stat(outside); e != nil {
		t.Fatal("outside directory touched", e)
	}
	if removed := a.CleanupProtocolFiles(now); removed != 0 {
		t.Fatal("cleanup not idempotent", removed)
	}
}
