package app

import (
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
)

// pluginSource is the OpenCode plugin, compiled into the binary.
//
// The odometer cannot see spend on its own here: OpenCode serves its API
// in-process, so there is no port to attach to and the plugin's event spool is
// the only data path. Shipping the exe without it produces an app that starts,
// looks healthy and reports nothing.
//
//go:embed odometer_plugin.js
var pluginSource []byte

//go:embed odometer_tui.tsx
var tuiSource []byte

//go:embed tui_presence.js
var presenceSource []byte

// PluginFileName is the name the plugin must have inside the plugins dir.
const PluginFileName = "odometer.js"

// pluginDir returns OpenCode's plugin directory for the current user.
func pluginDir() string {
	if d := os.Getenv("OPENCODE_PLUGIN_DIR"); d != "" {
		return d
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	// Same location on every platform OpenCode supports.
	return filepath.Join(home, ".config", "opencode", "plugins")
}

// PluginStatus describes what EnsurePlugin did.
type PluginStatus struct {
	Path      string
	Installed bool // written for the first time
	Updated   bool // replaced an older copy
	Err       error
}

// EnsurePlugin writes the embedded plugin into OpenCode's plugin directory
// when it is absent or differs from the embedded copy.
//
// Content is compared by hash rather than timestamp: a user may have copied
// the file by hand, and rewriting an identical file on every start would churn
// the directory OpenCode watches.
//
// A hand-modified plugin is still overwritten, because a stale copy silently
// breaks ingest and the file is not intended as a user-editable surface.
func (a *App) EnsurePlugin() PluginStatus {
	dir := pluginDir()
	if dir == "" {
		return PluginStatus{Err: fmt.Errorf("cannot resolve home directory")}
	}
	path := filepath.Join(dir, PluginFileName)
	if err := a.ensureTUIPlugin(dir); err != nil {
		return PluginStatus{Path: path, Err: err}
	}

	want := sha256.Sum256(pluginSource)
	if existing, err := os.ReadFile(path); err == nil {
		if got := sha256.Sum256(existing); got == want {
			return PluginStatus{Path: path}
		}
		if err := os.WriteFile(path, pluginSource, 0o644); err != nil {
			return PluginStatus{Path: path, Err: err}
		}
		a.logEvent("plugin updated at %s (sha %s)", path, hex.EncodeToString(want[:8]))
		return PluginStatus{Path: path, Updated: true}
	}

	if err := os.MkdirAll(dir, 0o755); err != nil {
		return PluginStatus{Path: path, Err: err}
	}
	if err := os.WriteFile(path, pluginSource, 0o644); err != nil {
		return PluginStatus{Path: path, Err: err}
	}
	a.logEvent("plugin installed at %s (%s)", path, runtime.GOOS)
	return PluginStatus{Path: path, Installed: true}
}

func (a *App) ensureTUIPlugin(dir string) error {
	// OpenCode 1.14.39 needs an explicit TUI registration, independent of
	// server plugin discovery. Place the companion beside tui.json.
	target := filepath.Dir(dir)
	files := map[string][]byte{
		"odometer-tui.tsx": tuiSource,
		"tui-presence.js":  presenceSource,
	}
	for name, body := range files {
		path := filepath.Join(target, name)
		if old, err := os.ReadFile(path); err == nil && string(old) == string(body) {
			continue
		}
		if err := atomicExperienceFile(path, body); err != nil {
			return err
		}
	}
	return registerTUIPlugin(target, filepath.Join(target, "odometer-tui.tsx"))
}
