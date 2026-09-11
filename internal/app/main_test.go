package app

import (
	"os"
	"path/filepath"
	"testing"
)

// TestMain redirects the plugin discovery pointer and the plugin install
// directory into a temp dir for the whole package.
//
// Both live at fixed per-user paths, and app.New writes the pointer. Without
// this, running the test suite repoints the plugin inside a live OpenCode at a
// temp directory that is deleted on cleanup, so the user's odometer silently
// stops recording spend until the app is restarted.
func TestMain(m *testing.M) {
	tmp, err := os.MkdirTemp("", "odo-test-home")
	if err != nil {
		panic(err)
	}
	os.Setenv("OPENCODE_ODOMETER_POINTER", filepath.Join(tmp, ".opencode-odometer.json"))
	os.Setenv("OPENCODE_PLUGIN_DIR", filepath.Join(tmp, "plugins"))
	code := m.Run()
	os.RemoveAll(tmp)
	os.Exit(code)
}
