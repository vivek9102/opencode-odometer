package app

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
)

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
	if err := CheckOpenCodeCompatibility(); err != nil {
		return PluginStatus{Err: err}
	}
	dir := pluginDir()
	if dir == "" {
		return PluginStatus{Err: fmt.Errorf("cannot resolve home directory")}
	}
	if IntegrationV2 {
		return a.ensureV2Package(dir)
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

func (a *App) ensureV2Package(dir string) PluginStatus {
	target := filepath.Join(dir, "odometer-v2")
	path := filepath.Join(target, "index.js")
	status := PluginStatus{Path: path}
	if _, _, err := prepareCLIPlugin(filepath.Dir(dir), target); err != nil {
		status.Err = err
		return status
	}
	old, err := os.ReadFile(path)
	status.Installed = os.IsNotExist(err)
	status.Updated = err == nil && string(old) != string(pluginSource)
	files := map[string][]byte{
		"index.js":        pluginSource,
		"tui.tsx":         tuiSource,
		"tui-presence.js": presenceSource,
		"v2-models.js":    v2ModelSource,
		"v2-sessions.js":  v2SessionSource,
		"package.json":    []byte(`{"name":"opencode-odometer-v2","private":true,"type":"module","exports":{".":"./index.js","./tui":"./tui.tsx"}}`),
	}
	for name, body := range files {
		file := filepath.Join(target, name)
		if old, err := os.ReadFile(file); err == nil && string(old) == string(body) {
			continue
		}
		if err := atomicExperienceFile(file, body); err != nil {
			status.Err = err
			return status
		}
	}
	// The V1 adapter (including locally patched variants) must not run beside
	// the V2 adapter. Preserve its bytes outside the auto-discovery directory.
	legacy := filepath.Join(dir, PluginFileName)
	if body, err := os.ReadFile(legacy); err == nil {
		sum := sha256.Sum256(body)
		backup := filepath.Join(filepath.Dir(dir), "odometer-v1-"+hex.EncodeToString(sum[:8])+".js.backup")
		if err := atomicExperienceFile(backup, body); err != nil {
			status.Err = err
			return status
		}
		if err := os.Remove(legacy); err != nil {
			status.Err = err
			return status
		}
	}
	status.Err = registerCLIPlugin(filepath.Dir(dir), target)
	return status
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
	return registerTerminalPlugin(target, filepath.Join(target, "odometer-tui.tsx"))
}
