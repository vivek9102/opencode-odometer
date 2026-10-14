//go:build opencode_v2

package app

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestV2PackagePreservesLegacySourceAndOtherCLISettings(t *testing.T) {
	dir := t.TempDir()
	pdir := filepath.Join(dir, "plugins")
	os.MkdirAll(pdir, 0755)
	t.Setenv("OPENCODE_PLUGIN_DIR", pdir)
	legacy := []byte("export const Legacy = async () => ({})")
	old := filepath.Join(pdir, PluginFileName)
	os.WriteFile(old, legacy, 0600)
	config := map[string]any{"theme": map[string]string{"name": "tokyonight"}, "plugins": []any{"other-plugin", map[string]string{"package": filepath.ToSlash(filepath.Join(dir, "odometer-tui.tsx"))}}}
	raw, _ := json.Marshal(config)
	os.WriteFile(filepath.Join(dir, "cli.json"), raw, 0600)
	a, _, _ := newTestApp(t)
	if result := a.EnsurePlugin(); result.Err != nil {
		t.Fatal(result.Err)
	}
	if _, err := os.Stat(old); !os.IsNotExist(err) {
		t.Fatal("legacy adapter remained auto-discoverable")
	}
	backups, _ := filepath.Glob(filepath.Join(dir, "odometer-v1-*.js.backup"))
	if len(backups) != 1 {
		t.Fatal("legacy source not backed up")
	}
	saved, _ := os.ReadFile(backups[0])
	if string(saved) != string(legacy) {
		t.Fatal("legacy backup changed")
	}
	raw, _ = os.ReadFile(filepath.Join(dir, "cli.json"))
	var updated struct {
		Plugins []string          `json:"plugins"`
		Theme   map[string]string `json:"theme"`
	}
	if err := json.Unmarshal(raw, &updated); err != nil {
		t.Fatal(err)
	}
	if len(updated.Plugins) != 2 || updated.Plugins[0] != "other-plugin" || updated.Theme["name"] != "tokyonight" {
		t.Fatalf("unrelated settings changed: %s", raw)
	}
	for _, file := range []string{"index.js", "tui.tsx", "tui-presence.js", "v2-models.js", "package.json"} {
		if _, err := os.Stat(filepath.Join(pdir, "odometer-v2", file)); err != nil {
			t.Fatal(err)
		}
	}
}

func TestV2InvalidCLILeavesExistingInstallationUntouched(t *testing.T) {
	dir := t.TempDir()
	pdir := filepath.Join(dir, "plugins")
	os.MkdirAll(pdir, 0755)
	t.Setenv("OPENCODE_PLUGIN_DIR", pdir)
	old := filepath.Join(pdir, PluginFileName)
	os.WriteFile(old, []byte("legacy bytes"), 0600)
	os.WriteFile(filepath.Join(dir, "cli.json"), []byte("{broken"), 0600)
	a, _, _ := newTestApp(t)
	if a.EnsurePlugin().Err == nil {
		t.Fatal("invalid CLI config accepted")
	}
	raw, _ := os.ReadFile(old)
	if string(raw) != "legacy bytes" {
		t.Fatal("legacy plugin changed before preflight succeeded")
	}
	if _, err := os.Stat(filepath.Join(pdir, "odometer-v2")); !os.IsNotExist(err) {
		t.Fatal("partial V2 package installed")
	}
}

func TestV2RejectsWrongOrUnknownHostBeforeInstalling(t *testing.T) {
	original := probeOpenCodeVersion
	t.Cleanup(func() { probeOpenCodeVersion = original })
	for _, version := range []string{"1.14.39", "1.19.0", "unknown", "12.0.28"} {
		probeOpenCodeVersion = func() (string, error) { return version, nil }
		dir := t.TempDir()
		t.Setenv("OPENCODE_PLUGIN_DIR", filepath.Join(dir, "plugins"))
		a, _, _ := newTestApp(t)
		if st := a.EnsurePlugin(); st.Err == nil {
			t.Fatalf("accepted incompatible version %q", version)
		}
		entries, _ := os.ReadDir(dir)
		if len(entries) != 0 {
			t.Fatal("incompatible build touched OpenCode config")
		}
	}
	probeOpenCodeVersion = func() (string, error) { return "", errors.New("unavailable") }
	if CheckOpenCodeCompatibility() == nil {
		t.Fatal("unknown version must not install")
	}
	for _, version := range []string{"opencode v2.0.28\n", "2.0.28", "2.0.28-beta.1"} {
		if err := validateV2Version(version); err != nil {
			t.Fatal(err)
		}
	}
}
