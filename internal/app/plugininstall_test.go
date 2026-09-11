package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestEnsurePluginInstallsAndIsIdempotent covers the self-install path: a bare
// executable must be able to put the plugin in place, because OpenCode serves
// its API in-process and the plugin spool is the only source of spend data.
func TestEnsurePluginInstallsAndIsIdempotent(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("OPENCODE_PLUGIN_DIR", filepath.Join(dir, "plugins"))

	a, _, _ := newTestApp(t)

	st := a.EnsurePlugin()
	if st.Err != nil {
		t.Fatalf("EnsurePlugin: %v", st.Err)
	}
	if !st.Installed {
		t.Fatal("expected a first-time install")
	}
	raw, err := os.ReadFile(st.Path)
	if err != nil {
		t.Fatalf("plugin not written: %v", err)
	}
	if !strings.Contains(string(raw), "odometer") {
		t.Error("written plugin does not look like the odometer plugin")
	}

	// Re-running must not rewrite an identical file: OpenCode watches this
	// directory, so needless churn could trigger reloads.
	st2 := a.EnsurePlugin()
	if st2.Err != nil {
		t.Fatalf("second EnsurePlugin: %v", st2.Err)
	}
	if st2.Installed || st2.Updated {
		t.Errorf("identical plugin should be left alone, got %+v", st2)
	}
}

// TestEnsurePluginReplacesStaleCopy covers upgrades: a plugin left over from an
// older build silently breaks ingest, so content must be compared.
func TestEnsurePluginReplacesStaleCopy(t *testing.T) {
	dir := t.TempDir()
	pdir := filepath.Join(dir, "plugins")
	t.Setenv("OPENCODE_PLUGIN_DIR", pdir)
	if err := os.MkdirAll(pdir, 0o755); err != nil {
		t.Fatal(err)
	}
	stale := filepath.Join(pdir, PluginFileName)
	if err := os.WriteFile(stale, []byte("// old version\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	a, _, _ := newTestApp(t)
	st := a.EnsurePlugin()
	if st.Err != nil {
		t.Fatalf("EnsurePlugin: %v", st.Err)
	}
	if !st.Updated {
		t.Error("stale plugin should have been replaced")
	}
	raw, _ := os.ReadFile(stale)
	if strings.Contains(string(raw), "old version") {
		t.Error("stale content survived the update")
	}
}
