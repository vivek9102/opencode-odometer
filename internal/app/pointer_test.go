package app

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestWritePointerIncludesExecutable(t *testing.T) {
	target := filepath.Join(t.TempDir(), "pointer.json")
	t.Setenv("OPENCODE_ODOMETER_POINTER", target)
	executable := filepath.Join("C:", "Downloads", "OpenCode_Odometer.exe")

	if err := WritePointer("data", "budget", "grace", filepath.Dir(executable), executable); err != nil {
		t.Fatalf("WritePointer: %v", err)
	}
	raw, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	var got PointerPayload
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatal(err)
	}
	if got.Executable != executable {
		t.Errorf("executable = %q, want %q", got.Executable, executable)
	}
	if got.AppDir != filepath.Dir(executable) {
		t.Errorf("app_dir = %q, want %q", got.AppDir, filepath.Dir(executable))
	}
}
