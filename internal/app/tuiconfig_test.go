package app

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRegisterTUIPluginPreservesSettingsAndComments(t *testing.T) {
	for _, body := range []string{
		`{}`, `{"theme":"dark"}`, `{"plugin":[],"theme":"dark"}`,
		"{\n// Keep this comment\n\"theme\":\"https://example.com/a\",\"plugin\":[\"other-plugin\",],\"nested\":{\"plugin\":[]},\n}",
	} {
		t.Run(body, func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, "tui.jsonc")
			os.WriteFile(path, []byte(body), 0600)
			entry := filepath.Join(dir, "odometer-tui.tsx")
			if err := registerTUIPlugin(dir, entry); err != nil {
				t.Fatal(err)
			}
			raw, _ := os.ReadFile(path)
			var cfg map[string]any
			if err := json.Unmarshal(jsoncForInspection(raw), &cfg); err != nil {
				t.Fatalf("invalid result: %s: %v", raw, err)
			}
			if strings.Contains(body, "Keep this comment") && !strings.Contains(string(raw), "Keep this comment") {
				t.Fatal("comment lost")
			}
			if strings.Contains(body, "other-plugin") && !strings.Contains(string(raw), "other-plugin") {
				t.Fatal("other plugin lost")
			}
			if err := registerTUIPlugin(dir, entry); err != nil {
				t.Fatal(err)
			}
			again, _ := os.ReadFile(path)
			if string(again) != string(raw) {
				t.Fatal("registration is not idempotent")
			}
		})
	}
}

func TestRegisterTUIPluginDoesNotOverwriteMalformedConfig(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "tui.json")
	body := []byte(`{"plugin":`)
	os.WriteFile(path, body, 0600)
	if err := registerTUIPlugin(dir, filepath.Join(dir, "odometer-tui.tsx")); err == nil {
		t.Fatal("malformed settings accepted")
	}
	raw, _ := os.ReadFile(path)
	if string(raw) != string(body) {
		t.Fatal("malformed settings overwritten")
	}
}
