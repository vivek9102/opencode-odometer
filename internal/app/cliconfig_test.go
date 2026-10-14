package app

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCLIRegistrationPreservesOtherPluginsAndSettings(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "cli.json")
	original := `{"theme":{"name":"tokyonight"},"plugins":["other-plugin"],"tabs":{"mode":"on"}}`
	os.WriteFile(path, []byte(original), 0600)
	entry := filepath.Join(dir, "odometer-tui.tsx")
	for i := 0; i < 2; i++ {
		if err := registerCLIPlugin(dir, entry); err != nil {
			t.Fatal(err)
		}
	}
	raw, _ := os.ReadFile(path)
	var cfg struct {
		Plugins []string          `json:"plugins"`
		Theme   map[string]string `json:"theme"`
		Tabs    map[string]string `json:"tabs"`
	}
	if err := json.Unmarshal(raw, &cfg); err != nil {
		t.Fatal(err)
	}
	if len(cfg.Plugins) != 2 || cfg.Theme["name"] != "tokyonight" || cfg.Tabs["mode"] != "on" {
		t.Fatalf("settings changed: %s", raw)
	}
	if _, err := os.Stat(filepath.Join(dir, "tui.json")); !os.IsNotExist(err) {
		t.Fatal("V2 registration wrote V1 config")
	}
	os.WriteFile(path, []byte(`{"plugins":"invalid"}`), 0600)
	if err := registerCLIPlugin(dir, entry); err == nil {
		t.Fatal("invalid array overwritten")
	}
	raw, _ = os.ReadFile(path)
	if !strings.Contains(string(raw), `"invalid"`) {
		t.Fatal("invalid config changed")
	}
}
