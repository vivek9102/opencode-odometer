package app

import (
	"os"
	"path/filepath"
	"testing"
)

// TestEmbeddedPluginMatchesSource guards against the embedded copy drifting
// from plugin/odometer.js.
//
// The duplicate exists because //go:embed cannot reach outside its package
// directory. Without this check, editing the real plugin would leave the
// binary shipping - and self-installing - an older one, which is worse than
// not installing at all: it silently overwrites a good plugin with a stale one.
func TestEmbeddedPluginMatchesSource(t *testing.T) {
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	src, err := os.ReadFile(filepath.Join(root, "plugin", ServerSourceName))
	if err != nil {
		t.Skipf("source plugin not available: %v", err)
	}
	if string(src) != string(pluginSource) {
		t.Fatal("internal/app/odometer_plugin.js is out of date.\n" +
			"Run: copy plugin\\odometer.js internal\\app\\odometer_plugin.js")
	}
	for name, body := range map[string][]byte{TerminalSourceName: tuiSource, "tui-presence.js": presenceSource} {
		raw, err := os.ReadFile(filepath.Join(root, "plugin", name))
		if err != nil {
			t.Fatal(err)
		}
		if string(raw) != string(body) {
			t.Fatalf("embedded %s is stale", name)
		}
	}
	if IntegrationV2 {
		raw, err := os.ReadFile(filepath.Join(root, "plugin", "v2-models.js"))
		if err != nil || string(raw) != string(v2ModelSource) {
			t.Fatal("embedded V2 model normalizer is stale")
		}
	}
}
